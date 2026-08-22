package compat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"keycloak-provisioner/internal/config"
)

type fakeServerInfo struct {
	doc map[string]any
	err error
	// providers maps an authentication kind to the provider ids the server
	// offers. Nil means the default set used by tests that do not care.
	providers map[string][]string
}

func (f fakeServerInfo) GetServerInfo(context.Context) (map[string]any, error) {
	return f.doc, f.err
}

func (f fakeServerInfo) GetAuthenticationProviders(_ context.Context, _, kind string) ([]map[string]any, error) {
	ids, ok := f.providers[kind]
	if !ok && f.providers == nil && kind == "authenticator-providers" {
		// A sensible default so tests focused on versions and features do not
		// have to enumerate providers they never use.
		ids = []string{"auth-cookie", "auth-otp-form", "conditional-level-of-authentication"}
	}

	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"id": id})
	}

	return out, nil
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
	if problems[0].Capability != "organization groups" {
		t.Errorf("unexpected requirement: %s", problems[0].Capability)
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

		if r.MinVersion == "" && r.Feature == "" {
			t.Errorf("%s: has neither a MinVersion nor a Feature, so it gates nothing", r.Name)
		}
		if r.MinVersion != "" {
			if _, err := ParseVersion(r.MinVersion); err != nil {
				t.Errorf("%s: unparseable MinVersion %q: %v", r.Name, r.MinVersion, err)
			}
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

// TestCheckStandardTokenExchangeFeatureDisabled covers the flag that governs
// standard token exchange. With it off, Keycloak still stores
// standard.token.exchange.enabled=true and provisioning succeeds — the setting
// is simply inert, which is why the gate has to catch it.
func TestCheckStandardTokenExchangeFeatureDisabled(t *testing.T) {
	cfg := &config.Config{Realms: []config.Realm{{
		Realm:   "test",
		Clients: []config.Client{{ClientID: "web", StandardTokenExchangeEnabled: boolPtr(true)}},
	}}}

	info := ServerInfo{
		RawVersion: "26.6.4",
		Version:    Version{26, 6, 4},
		Parsed:     true,
		Features:   map[string]bool{"TOKEN_EXCHANGE_STANDARD_V2": false},
	}

	problems := Check(cfg, info)
	if len(problems) != 1 {
		t.Fatalf("expected the disabled feature to be reported, got %v", problems)
	}
	if !strings.Contains(problems[0].Reason, "TOKEN_EXCHANGE_STANDARD_V2") {
		t.Errorf("reason should name the feature, got %q", problems[0].Reason)
	}

	// The legacy preview feature is unrelated and must not be consulted.
	info.Features = map[string]bool{"TOKEN_EXCHANGE_STANDARD_V2": true, "TOKEN_EXCHANGE": false}
	if problems := Check(cfg, info); len(problems) != 0 {
		t.Errorf("the legacy TOKEN_EXCHANGE preview must not matter, got %v", problems)
	}
}

// TestCheckStepUpAuthentication covers a requirement with no version floor:
// only the feature flag decides.
func TestCheckStepUpAuthentication(t *testing.T) {
	cfg := &config.Config{Realms: []config.Realm{{
		Realm:     "test",
		AcrLoaMap: map[string]int{"gold": 2},
		Clients:   []config.Client{{ClientID: "web", AcrLoaMap: map[string]int{"gold": 2}}},
	}}}

	disabled := ServerInfo{
		RawVersion: "26.6.4",
		Version:    Version{26, 6, 4},
		Parsed:     true,
		Features:   map[string]bool{"STEP_UP_AUTHENTICATION": false},
	}

	problems := Check(cfg, disabled)
	if len(problems) != 1 {
		t.Fatalf("expected step-up to be reported, got %v", problems)
	}
	if len(problems[0].Paths) != 2 {
		t.Errorf("both the realm and the client map should be named, got %v", problems[0].Paths)
	}

	// No version floor, so even an old server passes when the feature is on.
	enabled := ServerInfo{
		RawVersion: "26.0.0",
		Version:    Version{26, 0, 0},
		Parsed:     true,
		Features:   map[string]bool{"STEP_UP_AUTHENTICATION": true},
	}
	if problems := Check(cfg, enabled); len(problems) != 0 {
		t.Errorf("step-up has no version floor, got %v", problems)
	}
}

// statusErr models what the Keycloak client returns for an unexpected status.
type statusErr struct{ code int }

func (e statusErr) Error() string   { return fmt.Sprintf("unexpected status %d", e.code) }
func (e statusErr) StatusCode() int { return e.code }

// errOnProviders answers server info but refuses the provider lists, the way a
// least-privilege admin account does: create-realm is enough to provision but
// not to read /authentication/*-providers.
type errOnProviders struct {
	doc map[string]any
	err error
}

func (e errOnProviders) GetServerInfo(context.Context) (map[string]any, error) {
	return e.doc, nil
}

func (e errOnProviders) GetAuthenticationProviders(context.Context, string, string) ([]map[string]any, error) {
	if e.err != nil {
		return nil, e.err
	}

	return nil, statusErr{code: 403}
}

// TestVerifySkipsProviderChecksWhenForbidden pins that a check the account
// cannot perform is skipped rather than failing the run. Aborting would break
// setups that provision perfectly well, which is worse than not checking.
func TestVerifySkipsProviderChecksWhenForbidden(t *testing.T) {
	reader := errOnProviders{doc: serverInfoDoc("26.6.4", map[string]bool{"ORGANIZATION": true})}

	cfg := &config.Config{Realms: []config.Realm{{
		Realm: "test",
		AuthenticationFlows: []config.AuthenticationFlow{{
			Alias:      "f",
			Executions: []config.AuthenticationExecution{{Provider: "anything-at-all"}},
		}},
	}}}

	if err := Verify(context.Background(), reader, cfg); err != nil {
		t.Fatalf("a forbidden provider list must not fail the run: %v", err)
	}
}

// TestVerifyStillReportsVersionProblemsWhenProvidersForbidden confirms the
// checks degrade independently: losing one does not lose the other.
func TestVerifyStillReportsVersionProblemsWhenProvidersForbidden(t *testing.T) {
	reader := errOnProviders{doc: serverInfoDoc("26.2.0", map[string]bool{"ORGANIZATION": true})}

	err := Verify(context.Background(), reader, orgGroupConfig())
	if err == nil {
		t.Fatal("the version problem should still be reported")
	}
	if !strings.Contains(err.Error(), "organization groups") {
		t.Errorf("unexpected error: %v", err)
	}
}

// allFail refuses everything, as an account with no admin rights at all would.
type allFail struct{ err error }

func (a allFail) GetServerInfo(context.Context) (map[string]any, error) {
	if a.err != nil {
		return nil, a.err
	}

	return nil, statusErr{code: 403}
}

func (a allFail) GetAuthenticationProviders(context.Context, string, string) ([]map[string]any, error) {
	if a.err != nil {
		return nil, a.err
	}

	return nil, statusErr{code: 403}
}

func TestVerifySkipsEverythingWhenServerInfoForbidden(t *testing.T) {
	if err := Verify(context.Background(), allFail{}, orgGroupConfig()); err != nil {
		t.Fatalf("an unreadable server must not fail the run: %v", err)
	}
}

// TestVerifyPropagatesNonPermissionErrors pins the other half: a failure that
// is not the server refusing the request means something is actually wrong, and
// hiding it would surface later as a partial run with a murkier cause.
func TestVerifyPropagatesNonPermissionErrors(t *testing.T) {
	t.Run("server info", func(t *testing.T) {
		reader := allFail{err: errors.New("dial tcp: connection refused")}

		if err := Verify(context.Background(), reader, orgGroupConfig()); err == nil {
			t.Fatal("a network failure must not be silently skipped")
		}
	})

	t.Run("provider lists", func(t *testing.T) {
		reader := errOnProviders{
			doc: serverInfoDoc("26.6.4", map[string]bool{"ORGANIZATION": true}),
			err: statusErr{code: 500},
		}

		err := Verify(context.Background(), reader, orgGroupConfig())
		if err == nil {
			t.Fatal("a 5xx must not be silently skipped")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected the underlying error, got: %v", err)
		}
	})
}

func TestIsPermissionDenied(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{statusErr{code: 401}, true},
		{statusErr{code: 403}, true},
		{statusErr{code: 404}, false},
		{statusErr{code: 500}, false},
		{fmt.Errorf("wrapped: %w", statusErr{code: 403}), true},
		{errors.New("connection refused"), false},
		{nil, false},
	}

	for _, tt := range tests {
		if got := isPermissionDenied(tt.err); got != tt.want {
			t.Errorf("isPermissionDenied(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}
