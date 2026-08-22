package compat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"keycloak-provisioner/internal/config"
)

// ServerInfoReader reads the Keycloak server info document. *client.Client is
// the production implementation; it is a one-method port so this package does
// not depend on the whole admin API.
type ServerInfoReader interface {
	GetServerInfo(ctx context.Context) (map[string]any, error)
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
}

// Problem is one capability the server cannot satisfy.
type Problem struct {
	Requirement Requirement
	// Paths are the config locations that rely on the capability.
	Paths []string
	// Reason says whether the version is too old or the feature is disabled.
	Reason string
}

func (p Problem) Error() string {
	return fmt.Sprintf("%s: %s (used at %s)", p.Requirement.Name, p.Reason, strings.Join(p.Paths, ", "))
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

	info := ServerInfo{Features: map[string]bool{}}

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
			problems = append(problems, Problem{Requirement: req, Paths: paths, Reason: reason})
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
func Verify(ctx context.Context, reader ServerInfoReader, cfg *config.Config) error {
	info, err := ReadServerInfo(ctx, reader)
	if err != nil {
		return err
	}

	if !info.Parsed {
		slog.Warn("Could not parse the Keycloak version; skipping version checks",
			"version", info.RawVersion)
	} else {
		slog.Info("Detected Keycloak version", "version", info.RawVersion)
	}

	problems := Check(cfg, info)
	if len(problems) == 0 {
		return nil
	}

	for _, p := range problems {
		slog.Error("Unsupported by this Keycloak server",
			"capability", p.Requirement.Name,
			"reason", p.Reason,
			"usedAt", strings.Join(p.Paths, ", "))
	}

	messages := make([]string, 0, len(problems))
	for _, p := range problems {
		messages = append(messages, p.Error())
	}

	return fmt.Errorf("config is not supported by this Keycloak server:\n  %s", strings.Join(messages, "\n  "))
}
