package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

func TestEnsureAuthenticationFlowSkipsExistingWithoutMutating(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "f-1", "alias": "browser-step-up", "builtIn": false},
			})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not create a flow that already exists")
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not touch executions of an existing flow")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{
		Alias:      "browser-step-up",
		Executions: []config.AuthenticationExecution{{Provider: "auth-cookie", Requirement: "REQUIRED"}},
	}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}
}

func TestEnsureAuthenticationFlowRejectsBuiltIn(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "f-1", "alias": "browser", "builtIn": true},
			})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "browser"}

	err := p.ensureAuthenticationFlow(context.Background(), "test", f)
	if err == nil {
		t.Fatal("expected an error for a built-in flow")
	}
	if !strings.Contains(err.Error(), "copyFrom") {
		t.Errorf("error should point at copyFrom, got: %v", err)
	}
}

func TestEnsureAuthenticationFlowCopiesFromSource(t *testing.T) {
	var mu sync.Mutex
	var copied map[string]any
	var copiedFrom string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "f-1", "alias": "browser", "builtIn": true},
			})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			t.Error("copyFrom must copy, not create from scratch")
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/copy": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			copiedFrom = r.PathValue("alias")
			json.NewDecoder(r.Body).Decode(&copied)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "browser-step-up", CopyFrom: "browser"}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if copiedFrom != "browser" || copied["newName"] != "browser-step-up" {
		t.Errorf("unexpected copy: from=%q body=%v", copiedFrom, copied)
	}
}

func TestEnsureAuthenticationFlowMissingCopySourceErrors(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "new-flow", CopyFrom: "does-not-exist"}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err == nil {
		t.Fatal("expected an error when copyFrom source is missing")
	}
}

func TestEnsureAuthenticationFlowCreatesExecutionsInDeclaredOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			provider, _ := body["provider"].(string)
			mu.Lock()
			defer mu.Unlock()
			order = append(order, r.PathValue("alias")+":"+provider)
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/flow": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			alias, _ := body["alias"].(string)
			mu.Lock()
			defer mu.Unlock()
			order = append(order, r.PathValue("alias")+":subflow="+alias)
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{
		Alias: "step-up",
		Executions: []config.AuthenticationExecution{
			{Provider: "auth-cookie"},
			{Provider: "identity-provider-redirector"},
			{Subflow: "loa-gold", Executions: []config.AuthenticationExecution{
				{Provider: "conditional-level-of-authentication"},
				{Provider: "auth-otp-form"},
			}},
		},
	}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	want := []string{
		"step-up:auth-cookie",
		"step-up:identity-provider-redirector",
		"step-up:subflow=loa-gold",
		"loa-gold:conditional-level-of-authentication",
		"loa-gold:auth-otp-form",
	}
	if len(order) != len(want) {
		t.Fatalf("expected %d calls, got %d: %v", len(want), len(order), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("call %d: expected %q, got %q", i, want[i], order[i])
		}
	}
}

func TestEnsureAuthenticationFlowSetsRequirementAndConfig(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any
	var execConfig map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "ex-1", "providerId": "conditional-level-of-authentication", "requirement": "DISABLED"},
			})
		},
		"PUT /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
		"POST /admin/realms/{realm}/authentication/executions/{id}/config": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&execConfig)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{
		Alias: "step-up",
		Executions: []config.AuthenticationExecution{{
			Provider:    "conditional-level-of-authentication",
			Requirement: "REQUIRED",
			Config: map[string]string{
				"alias":               "gold-condition",
				"loa-condition-level": "2",
			},
		}},
	}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("ensureAuthenticationFlow: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if updated["requirement"] != "REQUIRED" {
		t.Errorf("expected requirement REQUIRED, got %v", updated["requirement"])
	}
	if updated["id"] != "ex-1" {
		t.Errorf("update should carry the execution id, got %v", updated["id"])
	}
	if execConfig["alias"] != "gold-condition" {
		t.Errorf("expected config alias gold-condition, got %v", execConfig["alias"])
	}
	cfgMap, ok := execConfig["config"].(map[string]any)
	if !ok || cfgMap["loa-condition-level"] != "2" {
		t.Errorf("unexpected execution config: %v", execConfig["config"])
	}
	if _, ok := cfgMap["alias"]; ok {
		t.Error("alias names the config and must not be a config entry")
	}
}

func TestEnsureAuthenticationBindingsNilDoesNothing(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not update the realm when no bindings are configured")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	if err := p.ensureAuthenticationBindings(context.Background(), "test", nil); err != nil {
		t.Fatalf("ensureAuthenticationBindings: %v", err)
	}
	if err := p.ensureAuthenticationBindings(context.Background(), "test", &config.AuthenticationBindings{}); err != nil {
		t.Fatalf("ensureAuthenticationBindings (empty): %v", err)
	}
}

func TestEnsureAuthenticationBindingsSendsAliases(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	bindings := &config.AuthenticationBindings{
		BrowserFlow:     "browser-step-up",
		DirectGrantFlow: "direct grant",
	}

	if err := p.ensureAuthenticationBindings(context.Background(), "test", bindings); err != nil {
		t.Fatalf("ensureAuthenticationBindings: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if body["browserFlow"] != "browser-step-up" || body["directGrantFlow"] != "direct grant" {
		t.Errorf("unexpected bindings body: %v", body)
	}
	if _, ok := body["registrationFlow"]; ok {
		t.Error("unset bindings must be omitted")
	}
}

func TestResolveFlowBindingOverridesResolvesAliasToID(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "flow-uuid-1", "alias": "browser-step-up"},
			})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	resolved, err := p.resolveFlowBindingOverrides(context.Background(), "test", "web",
		map[string]string{"browser": "browser-step-up"})
	if err != nil {
		t.Fatalf("resolveFlowBindingOverrides: %v", err)
	}

	// The client representation takes flow IDs here, not aliases.
	if resolved["browser"] != "flow-uuid-1" {
		t.Errorf("expected flow id, got %v", resolved["browser"])
	}
}

func TestResolveFlowBindingOverridesUnknownAliasErrors(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	_, err := p.resolveFlowBindingOverrides(context.Background(), "test", "web",
		map[string]string{"browser": "missing-flow"})
	if err == nil {
		t.Fatal("expected an error for an unknown flow alias")
	}
}

func TestResolveFlowBindingOverridesEmptySkipsLookup(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not list flows when no overrides are configured")
			w.WriteHeader(http.StatusOK)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	resolved, err := p.resolveFlowBindingOverrides(context.Background(), "test", "web", nil)
	if err != nil {
		t.Fatalf("resolveFlowBindingOverrides: %v", err)
	}
	if resolved != nil {
		t.Errorf("expected nil, got %v", resolved)
	}
}

// TestEnsureAuthenticationFlowMissingCopySourceWarns covers the dry-run case:
// when the flow listing cannot show the copy source — a realm that does not
// exist yet has none of its built-in flows visible — the run must continue and
// let the copy call be the authority.
func TestEnsureAuthenticationFlowMissingCopySourceWarns(t *testing.T) {
	var mu sync.Mutex
	copied := false

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/copy": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			copied = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	f := config.AuthenticationFlow{Alias: "browser-step-up", CopyFrom: "browser"}

	if err := p.ensureAuthenticationFlow(context.Background(), "test", f); err != nil {
		t.Fatalf("an unlistable copy source must not abort the run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !copied {
		t.Error("the copy should still be attempted")
	}
}
