package provisioner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

// captureHandler collects log messages so a test can assert on them.
type captureHandler struct {
	mu       sync.Mutex
	messages []string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, r.Message)

	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// dryRunMessages returns, in order, the DRY-RUN lines a full provisioning run
// produces against an empty server.
func dryRunMessages(t *testing.T, yaml string) []string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	handler := &captureHandler{}
	previous := slog.Default()

	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(previous)

	if err := New(NewDryRunAdapter(newFakeAPI()), cfg).Run(context.Background()); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()

	var out []string

	for _, m := range handler.messages {
		if strings.HasPrefix(m, "DRY-RUN: ") {
			out = append(out, strings.TrimPrefix(m, "DRY-RUN: "))
		}
	}

	return out
}

// goldenConfig exercises every resource kind the provisioner can create, so the
// golden list below covers the whole dry-run surface.
const goldenConfig = `
realms:
  - realm: "golden"
    enabled: true
    organizationsEnabled: true
    attributes:
      frontendUrl: "https://id.example.com"
    acrLoaMap:
      gold: 2
    authenticationFlows:
      - alias: "browser-step-up"
        executions:
          - provider: "auth-cookie"
            requirement: "ALTERNATIVE"
          - subflow: "loa-gold"
            requirement: "CONDITIONAL"
            executions:
              - provider: "conditional-level-of-authentication"
                requirement: "REQUIRED"
                config:
                  alias: "gold-condition"
                  loa-condition-level: "2"
    authenticationBindings:
      browserFlow: "browser-step-up"
    clientScopes:
      - name: "orders:read"
        type: "optional"
        protocolMappers:
          - name: "orders-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
    clients:
      - clientId: "web"
        serviceAccountsEnabled: true
        optionalClientScopes:
          - "orders:read"
        protocolMappers:
          - name: "web-audience"
            protocol: "openid-connect"
            protocolMapper: "oidc-audience-mapper"
        clientRoles:
          - name: "web-admin"
        serviceAccountRoles:
          realm:
            - "app-admin"
    roles:
      - name: "app-admin"
    groups:
      - name: "engineering"
        realmRoles:
          - "app-admin"
        subGroups:
          - name: "backend"
    users:
      - username: "alice"
        password: "pw"
        roles:
          realm:
            - "app-admin"
        groups:
          - "/engineering"
    organizations:
      - name: "acme"
        domains:
          - name: "acme.com"
        members:
          - "alice"
        groups:
          - name: "org-eng"
            members:
              - "alice"
`

// TestDryRunGoldenLog pins the whole dry-run surface.
//
// Dry-run's behaviour *is* what it logs: nothing is written, so the log is the
// only observable output. Asserting the ordered message list is therefore the
// only way to prove a refactor of the adapter or the port changed nothing. A
// deliberate change to dry-run should update this list; an accidental one
// should fail here.
func TestDryRunGoldenLog(t *testing.T) {
	want := []string{
		"would create realm",
		"would create authentication flow",
		"would add authentication execution",
		"would add authentication subflow",
		"would add authentication execution",
		"would update realm",
		"would create client scope",
		"would create client scope protocol mapper",
		"would assign client scope to realm optionals",
		"would create client",
		"would create protocol mapper",
		"would create client role",
		"would assign optional client scope",
		"would create realm role",
		"would assign realm roles",
		"would create group",
		"would assign realm roles to group",
		"would create subgroup",
		"would create user",
		"would reset user password",
		"would assign realm roles",
		"would add user to group",
		"would create organization",
		"would add user to organization",
		"would create organization group",
		"would add user to organization group",
	}

	got := dryRunMessages(t, goldenConfig)

	if len(got) != len(want) {
		t.Errorf("expected %d DRY-RUN messages, got %d", len(want), len(got))
	}

	for i := range max(len(got), len(want)) {
		var g, w string
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}

		if g != w {
			t.Errorf("message %d:\n  got  %q\n  want %q", i, g, w)
		}
	}
}
