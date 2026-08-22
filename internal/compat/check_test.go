package compat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"keycloak-provisioner/internal/config"
)

type fakeServerInfo struct {
	doc map[string]any
	err error
}

func (f fakeServerInfo) GetServerInfo(context.Context) (map[string]any, error) {
	return f.doc, f.err
}

func serverInfoDoc(version string, features map[string]bool) map[string]any {
	entries := make([]any, 0, len(features))
	for name, enabled := range features {
		entries = append(entries, map[string]any{"name": name, "enabled": enabled})
	}

	return map[string]any{
		"systemInfo": map[string]any{"version": version},
		"features":   entries,
	}
}

func boolPtr(b bool) *bool { return &b }

// orgConfig is a config that uses organizations but not organization groups.
func orgConfig() *config.Config {
	return &config.Config{Realms: []config.Realm{{
		Realm:                "test",
		OrganizationsEnabled: boolPtr(true),
		Organizations:        []config.Organization{{Name: "acme"}},
	}}}
}

// orgGroupConfig additionally uses organization groups.
func orgGroupConfig() *config.Config {
	return &config.Config{Realms: []config.Realm{{
		Realm:                "test",
		OrganizationsEnabled: boolPtr(true),
		Organizations: []config.Organization{{
			Name:   "acme",
			Groups: []config.OrganizationGroup{{Name: "engineering"}},
		}},
	}}}
}

func TestReadServerInfo(t *testing.T) {
	reader := fakeServerInfo{doc: serverInfoDoc("26.6.4", map[string]bool{
		"ORGANIZATION": true,
		"SCRIPTS":      false,
	})}

	info, err := ReadServerInfo(context.Background(), reader)
	if err != nil {
		t.Fatalf("ReadServerInfo: %v", err)
	}

	if !info.Parsed || info.Version != (Version{26, 6, 4}) {
		t.Errorf("unexpected version: %+v", info)
	}
	if info.RawVersion != "26.6.4" {
		t.Errorf("raw version: %q", info.RawVersion)
	}
	if !info.Features["ORGANIZATION"] || info.Features["SCRIPTS"] {
		t.Errorf("unexpected features: %v", info.Features)
	}
}

func TestReadServerInfoUnparseableVersionIsNotAnError(t *testing.T) {
	reader := fakeServerInfo{doc: serverInfoDoc("quarkus-dev", nil)}

	info, err := ReadServerInfo(context.Background(), reader)
	if err != nil {
		t.Fatalf("an unreadable version must not be an error: %v", err)
	}
	if info.Parsed {
		t.Error("expected Parsed to be false")
	}
	if info.RawVersion != "quarkus-dev" {
		t.Errorf("raw version should be preserved, got %q", info.RawVersion)
	}
}

func TestReadServerInfoPropagatesReadError(t *testing.T) {
	reader := fakeServerInfo{err: errors.New("boom")}

	if _, err := ReadServerInfo(context.Background(), reader); err == nil {
		t.Fatal("expected the read error to propagate")
	}
}

func TestCheckPassesOnNewEnoughServer(t *testing.T) {
	info := ServerInfo{
		RawVersion: "26.6.4",
		Version:    Version{26, 6, 4},
		Parsed:     true,
		Features:   map[string]bool{"ORGANIZATION": true},
	}

	if problems := Check(orgGroupConfig(), info); len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestCheckRejectsVersionTooOld(t *testing.T) {
	info := ServerInfo{
		RawVersion: "26.2.0",
		Version:    Version{26, 2, 0},
		Parsed:     true,
		Features:   map[string]bool{"ORGANIZATION": true},
	}

	problems := Check(orgGroupConfig(), info)
	if len(problems) != 1 {
		t.Fatalf("expected exactly the organization groups problem, got %v", problems)
	}
	if problems[0].Requirement.Name != "organization groups" {
		t.Errorf("unexpected requirement: %s", problems[0].Requirement.Name)
	}
	if !strings.Contains(problems[0].Reason, "26.6") {
		t.Errorf("reason should name the required version, got %q", problems[0].Reason)
	}
	if len(problems[0].Paths) != 1 || !strings.Contains(problems[0].Paths[0], "organizations[0].groups") {
		t.Errorf("problem should point at the config path, got %v", problems[0].Paths)
	}
}

func TestCheckRejectsDisabledFeature(t *testing.T) {
	// New enough by version, but the feature is switched off on this server —
	// the case a version-only check cannot catch.
	info := ServerInfo{
		RawVersion: "26.6.4",
		Version:    Version{26, 6, 4},
		Parsed:     true,
		Features:   map[string]bool{"ORGANIZATION": false},
	}

	problems := Check(orgConfig(), info)
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if !strings.Contains(problems[0].Reason, "disabled") {
		t.Errorf("reason should say the feature is disabled, got %q", problems[0].Reason)
	}
}

func TestCheckIgnoresUnusedCapabilities(t *testing.T) {
	// A config that uses none of the gated capabilities passes even against an
	// ancient server with everything switched off.
	info := ServerInfo{
		RawVersion: "20.0.0",
		Version:    Version{20, 0, 0},
		Parsed:     true,
		Features:   map[string]bool{"ORGANIZATION": false},
	}

	cfg := &config.Config{Realms: []config.Realm{{
		Realm:   "test",
		Clients: []config.Client{{ClientID: "web"}},
	}}}

	if problems := Check(cfg, info); len(problems) != 0 {
		t.Errorf("expected no problems for a config using nothing gated, got %v", problems)
	}
}

func TestCheckSkipsVersionComparisonWhenUnparsed(t *testing.T) {
	// The version could not be read, but the feature list still applies.
	info := ServerInfo{
		RawVersion: "quarkus-dev",
		Parsed:     false,
		Features:   map[string]bool{"ORGANIZATION": true},
	}

	if problems := Check(orgGroupConfig(), info); len(problems) != 0 {
		t.Errorf("unparsed versions must not be judged, got %v", problems)
	}
}

func TestCheckReportsUnknownFeatureAsAvailable(t *testing.T) {
	// A server that does not list the feature at all predates it; the version
	// comparison is what catches that, not a missing map entry.
	info := ServerInfo{
		RawVersion: "26.6.4",
		Version:    Version{26, 6, 4},
		Parsed:     true,
		Features:   map[string]bool{},
	}

	if problems := Check(orgGroupConfig(), info); len(problems) != 0 {
		t.Errorf("an unreported feature must not be treated as disabled, got %v", problems)
	}
}

func TestCheckStandardTokenExchange(t *testing.T) {
	cfg := &config.Config{Realms: []config.Realm{{
		Realm:   "test",
		Clients: []config.Client{{ClientID: "web", StandardTokenExchangeEnabled: boolPtr(true)}},
	}}}

	old := ServerInfo{RawVersion: "26.0.0", Version: Version{26, 0, 0}, Parsed: true}
	if problems := Check(cfg, old); len(problems) != 1 {
		t.Fatalf("26.0 should be too old for standard token exchange, got %v", problems)
	}

	// The legacy TOKEN_EXCHANGE preview feature being off must not matter:
	// standard token exchange is not behind it.
	ok := ServerInfo{
		RawVersion: "26.2.0",
		Version:    Version{26, 2, 0},
		Parsed:     true,
		Features:   map[string]bool{"TOKEN_EXCHANGE": false},
	}
	if problems := Check(cfg, ok); len(problems) != 0 {
		t.Errorf("26.2 with the legacy preview off should pass, got %v", problems)
	}
}

func TestVerifyReturnsErrorNamingEveryProblem(t *testing.T) {
	reader := fakeServerInfo{doc: serverInfoDoc("26.0.0", map[string]bool{"ORGANIZATION": true})}

	cfg := orgGroupConfig()
	cfg.Realms[0].Clients = []config.Client{{ClientID: "web", StandardTokenExchangeEnabled: boolPtr(true)}}

	err := Verify(context.Background(), reader, cfg)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"organization groups", "standard token exchange", "26.6", "26.2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestVerifyPassesOnSupportedServer(t *testing.T) {
	reader := fakeServerInfo{doc: serverInfoDoc("26.6.4", map[string]bool{"ORGANIZATION": true})}

	if err := Verify(context.Background(), reader, orgGroupConfig()); err != nil {
		t.Fatalf("expected the check to pass: %v", err)
	}
}

// TestRequirementsAreWellFormed guards the table itself: a typo in a minimum
// version would otherwise surface as a confusing runtime failure.
func TestRequirementsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}

	for _, r := range Requirements {
		if r.Name == "" {
			t.Error("a requirement has no name")
		}
		if seen[r.Name] {
			t.Errorf("duplicate requirement name %q", r.Name)
		}
		seen[r.Name] = true

		if _, err := ParseVersion(r.MinVersion); err != nil {
			t.Errorf("%s: unparseable MinVersion %q: %v", r.Name, r.MinVersion, err)
		}
		if r.Uses == nil {
			t.Errorf("%s: has no Uses predicate", r.Name)
		}
		if r.Since == "" {
			t.Errorf("%s: has no Since note recording where the floor comes from", r.Name)
		}
	}
}

// TestCheckIgnoresDisabledStandardTokenExchange pins that turning the feature
// off is not "using" it: the attribute an older Keycloak does not recognise is
// harmless, so gating on it would refuse a config that works.
func TestCheckIgnoresDisabledStandardTokenExchange(t *testing.T) {
	cfg := &config.Config{Realms: []config.Realm{{
		Realm:   "test",
		Clients: []config.Client{{ClientID: "web", StandardTokenExchangeEnabled: boolPtr(false)}},
	}}}

	old := ServerInfo{RawVersion: "26.0.0", Version: Version{26, 0, 0}, Parsed: true}

	if problems := Check(cfg, old); len(problems) != 0 {
		t.Errorf("disabling token exchange must not require 26.2, got %v", problems)
	}

	// Unset is likewise not a use.
	cfg.Realms[0].Clients[0].StandardTokenExchangeEnabled = nil
	if problems := Check(cfg, old); len(problems) != 0 {
		t.Errorf("an unset field must not require 26.2, got %v", problems)
	}
}
