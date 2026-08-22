package compat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"keycloak-provisioner/internal/config"
)

// ServerInfoReader reads the Keycloak server info document.
type ServerInfoReader interface {
	GetServerInfo(ctx context.Context) (map[string]any, error)
}

// ServerReader is everything this package needs from Keycloak. *client.Client
// is the production implementation; it is kept narrow so this package does not
// depend on the whole admin API.
type ServerReader interface {
	ServerInfoReader
	// GetAuthenticationProviders lists the providers of one authentication
	// kind, such as "authenticator-providers".
	GetAuthenticationProviders(ctx context.Context, realm, kind string) ([]map[string]any, error)
}

// ServerInfo is the part of Keycloak's server info this package needs.
type ServerInfo struct {
	// RawVersion is the version string exactly as the server reported it.
	RawVersion string
	// Version is RawVersion parsed, and Parsed reports whether that succeeded.
	Version Version
	Parsed  bool
	// Features maps a Keycloak feature name to whether it is enabled.
	Features map[string]bool
	// ProtocolMappers maps a protocol to the mapper type ids valid for it.
	ProtocolMappers map[string]map[string]bool
}

// Problem is one thing the server cannot satisfy: a capability whose version
// or feature requirement is unmet, or a provider the config names that the
// server does not offer.
type Problem struct {
	// Capability is the requirement name, or the provider id that is missing.
	Capability string
	// Paths are the config locations that rely on it.
	Paths []string
	// Reason says why the server cannot satisfy it.
	Reason string
}

func (p Problem) Error() string {
	return fmt.Sprintf("%s: %s (used at %s)", p.Capability, p.Reason, strings.Join(p.Paths, ", "))
}

// ReadServerInfo fetches and interprets the server info document.
//
// A version string it cannot parse is not treated as an error: custom and
// nightly builds report shapes this package should not be the judge of, and
// refusing to run because of one would be worse than not checking. Parsed is
// set to false and version comparisons are skipped; feature checks still apply.
func ReadServerInfo(ctx context.Context, reader ServerInfoReader) (ServerInfo, error) {
	raw, err := reader.GetServerInfo(ctx)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("reading server info: %w", err)
	}

	info := ServerInfo{
		Features:        map[string]bool{},
		ProtocolMappers: map[string]map[string]bool{},
	}

	if system, ok := raw["systemInfo"].(map[string]any); ok {
		info.RawVersion, _ = system["version"].(string)
	}

	if info.RawVersion != "" {
		if v, err := ParseVersion(info.RawVersion); err == nil {
			info.Version = v
			info.Parsed = true
		}
	}

	features, _ := raw["features"].([]any)
	for _, entry := range features {
		f, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		name, nameOK := f["name"].(string)
		enabled, enabledOK := f["enabled"].(bool)

		if nameOK && enabledOK {
			info.Features[name] = enabled
		}
	}

	types, _ := raw["protocolMapperTypes"].(map[string]any)
	for protocol, entry := range types {
		mappers, ok := entry.([]any)
		if !ok {
			continue
		}

		ids := map[string]bool{}

		for _, m := range mappers {
			mapper, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := mapper["id"].(string); ok {
				ids[id] = true
			}
		}

		info.ProtocolMappers[protocol] = ids
	}

	return info, nil
}

// Check returns every capability the config uses that this server cannot
// satisfy. An empty result means the config is safe to apply.
func Check(cfg *config.Config, info ServerInfo) []Problem {
	var problems []Problem

	for _, req := range Requirements {
		paths := req.Uses(cfg)
		if len(paths) == 0 {
			continue
		}

		if reason := req.unsatisfiedBy(info); reason != "" {
			problems = append(problems, Problem{Capability: req.Name, Paths: paths, Reason: reason})
		}
	}

	return problems
}

// unsatisfiedBy returns why the server cannot satisfy this requirement, or ""
// when it can.
func (r Requirement) unsatisfiedBy(info ServerInfo) string {
	if info.Parsed && r.MinVersion != "" {
		// A malformed MinVersion is a bug in the table rather than a problem
		// with the server, so it must not silently pass: treat it as a
		// mismatch and name it.
		min, err := ParseVersion(r.MinVersion)
		if err != nil {
			return fmt.Sprintf("requirement table has an unparseable minimum version %q", r.MinVersion)
		}

		if !info.Version.AtLeast(min) {
			return fmt.Sprintf("requires Keycloak %s or newer, server is %s", r.MinVersion, info.RawVersion)
		}
	}

	if r.Feature == "" {
		return ""
	}

	// An absent feature name means the server did not report it at all, which
	// happens on releases predating the feature. Only an explicit false is
	// reported as disabled; anything else has already been covered by the
	// version comparison above.
	if enabled, known := info.Features[r.Feature]; known && !enabled {
		return fmt.Sprintf("requires the %s server feature, which is disabled on this server", r.Feature)
	}

	return ""
}

// Verify checks the config against the server and returns an error naming every
// mismatch, or nil when there is nothing to report. It logs what it found so a
// successful check is visible too.
//
// A check it cannot perform is skipped with a warning rather than failing the
// run. The admin account only needs enough rights to provision; reading the
// server's capabilities takes more, and a least-privilege account — one holding
// create-realm and nothing else — is refused the provider lists. Aborting there
// would break setups that provision perfectly well, which is a worse outcome
// than not checking.
func Verify(ctx context.Context, reader ServerReader, cfg *config.Config) error {
	var problems []Problem

	info, err := ReadServerInfo(ctx, reader)
	switch {
	case err != nil:
		slog.Warn("Could not read the Keycloak server info; skipping version and feature checks", "error", err)
	case !info.Parsed:
		slog.Warn("Could not parse the Keycloak version; skipping version checks", "version", info.RawVersion)
	default:
		slog.Info("Detected Keycloak version", "version", info.RawVersion)
	}

	if err == nil {
		problems = append(problems, Check(cfg, info)...)
	}

	// Ask the server what it actually offers, which catches a provider missing
	// for any reason — a disabled feature, a version difference, or a typo —
	// without anyone having to record the mapping.
	caps, capsErr := ReadCapabilities(ctx, reader, info)
	if capsErr != nil {
		slog.Warn("Could not read the server's provider lists; skipping provider validation",
			"error", capsErr)
	} else {
		problems = append(problems, CheckCapabilities(cfg, caps)...)
	}

	if len(problems) == 0 {
		return nil
	}

	for _, p := range problems {
		slog.Error("Unsupported by this Keycloak server",
			"capability", p.Capability,
			"reason", p.Reason,
			"usedAt", strings.Join(p.Paths, ", "))
	}

	messages := make([]string, 0, len(problems))
	for _, p := range problems {
		messages = append(messages, p.Error())
	}

	return fmt.Errorf("config is not supported by this Keycloak server:\n  %s", strings.Join(messages, "\n  "))
}
