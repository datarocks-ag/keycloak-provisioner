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

// scopeMappingServer serves the endpoints a scope mapping reconcile touches and
// records what was posted. inScope seeds the roles Keycloak already has in the
// owner's scope, keyed by the request path.
type scopeMappingServer struct {
	mu      sync.Mutex
	inScope map[string][]map[string]any
	posted  map[string][]string
}

func newScopeMappingServer(t *testing.T, inScope map[string][]map[string]any) (*scopeMappingServer, string) {
	t.Helper()

	rec := &scopeMappingServer{inScope: inScope, posted: map[string][]string{}}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			clientID := r.URL.Query().Get("clientId")
			if clientID == "missing-app" {
				json.NewEncoder(w).Encode([]map[string]any{})

				return
			}
			json.NewEncoder(w).Encode([]map[string]any{{"id": clientID + "-uuid", "clientId": clientID}})
		},
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			if name == "ghost" {
				w.WriteHeader(http.StatusNotFound)

				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": name + "-id", "name": name, "composite": false})
		},
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			if name == "ghost" {
				w.WriteHeader(http.StatusNotFound)

				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": name + "-id", "name": name, "containerId": r.PathValue("uuid")})
		},
		"GET /admin/realms/{realm}/{owner}/{id}/scope-mappings/": func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			json.NewEncoder(w).Encode(rec.inScope[r.URL.Path])
		},
		"POST /admin/realms/{realm}/{owner}/{id}/scope-mappings/": func(w http.ResponseWriter, r *http.Request) {
			var roles []map[string]any
			if err := json.NewDecoder(r.Body).Decode(&roles); err != nil {
				t.Errorf("decoding posted roles: %v", err)
			}

			rec.mu.Lock()
			for _, role := range roles {
				rec.posted[r.URL.Path] = append(rec.posted[r.URL.Path], role["name"].(string))
			}
			rec.mu.Unlock()

			w.WriteHeader(http.StatusNoContent)
		},
	})
	t.Cleanup(server.Close)

	return rec, server.URL
}

func TestEnsureScopeMappingsAddsMissingRoles(t *testing.T) {
	rec, url := newScopeMappingServer(t, nil)
	p := New(newTestClient(t, url), nil)

	owner := clientScopeOwner("bff-uuid", "bff")
	roles := &config.UserRoles{
		Realm:   []string{"offline_access"},
		Clients: map[string][]string{"sandbox-ledger": {"reader", "writer"}},
	}

	if err := p.ensureScopeMappings(context.Background(), "test-realm", owner, roles); err != nil {
		t.Fatalf("ensureScopeMappings: %v", err)
	}

	realmPath := "/admin/realms/test-realm/clients/bff-uuid/scope-mappings/realm"
	if got := rec.posted[realmPath]; len(got) != 1 || got[0] != "offline_access" {
		t.Errorf("realm scope mappings posted = %v, want [offline_access]", got)
	}

	clientPath := "/admin/realms/test-realm/clients/bff-uuid/scope-mappings/clients/sandbox-ledger-uuid"
	if got := rec.posted[clientPath]; len(got) != 2 {
		t.Errorf("client scope mappings posted = %v, want reader and writer", got)
	}
}

// TestEnsureScopeMappingsSkipsRolesAlreadyInScope is the idempotence case: a
// second run must post nothing at all, not re-post what is already there.
func TestEnsureScopeMappingsSkipsRolesAlreadyInScope(t *testing.T) {
	rec, url := newScopeMappingServer(t, map[string][]map[string]any{
		"/admin/realms/test-realm/clients/bff-uuid/scope-mappings/realm": {
			{"id": "offline_access-id", "name": "offline_access"},
		},
		"/admin/realms/test-realm/clients/bff-uuid/scope-mappings/clients/sandbox-ledger-uuid": {
			{"id": "reader-id", "name": "reader"},
		},
	})
	p := New(newTestClient(t, url), nil)

	roles := &config.UserRoles{
		Realm:   []string{"offline_access"},
		Clients: map[string][]string{"sandbox-ledger": {"reader", "writer"}},
	}

	if err := p.ensureScopeMappings(context.Background(), "test-realm", clientScopeOwner("bff-uuid", "bff"), roles); err != nil {
		t.Fatalf("ensureScopeMappings: %v", err)
	}

	if got := rec.posted["/admin/realms/test-realm/clients/bff-uuid/scope-mappings/realm"]; got != nil {
		t.Errorf("a realm role already in scope must not be posted again, got %v", got)
	}

	clientPath := "/admin/realms/test-realm/clients/bff-uuid/scope-mappings/clients/sandbox-ledger-uuid"
	if got := rec.posted[clientPath]; len(got) != 1 || got[0] != "writer" {
		t.Errorf("only the missing role should be posted, got %v", got)
	}
}

// TestEnsureScopeMappingsOwnerKinds proves a client scope reaches its own
// endpoint rather than the client one — the two differ by a single path
// segment, and getting it wrong would write to whichever resource happened to
// share the id.
func TestEnsureScopeMappingsOwnerKinds(t *testing.T) {
	rec, url := newScopeMappingServer(t, nil)
	p := New(newTestClient(t, url), nil)

	owner := clientScopeScopeOwner("scope-uuid", "ledger-access")
	roles := &config.UserRoles{Clients: map[string][]string{"sandbox-ledger": {"reader"}}}

	if err := p.ensureScopeMappings(context.Background(), "test-realm", owner, roles); err != nil {
		t.Fatalf("ensureScopeMappings: %v", err)
	}

	want := "/admin/realms/test-realm/client-scopes/scope-uuid/scope-mappings/clients/sandbox-ledger-uuid"
	if got := rec.posted[want]; len(got) != 1 || got[0] != "reader" {
		t.Errorf("client scope owner posted to %v, want a single reader at %s", rec.posted, want)
	}
}

func TestEnsureScopeMappingsNilLeavesScopeAlone(t *testing.T) {
	rec, url := newScopeMappingServer(t, nil)
	p := New(newTestClient(t, url), nil)

	if err := p.ensureScopeMappings(context.Background(), "test-realm", clientScopeOwner("bff-uuid", "bff"), nil); err != nil {
		t.Fatalf("ensureScopeMappings: %v", err)
	}

	if len(rec.posted) != 0 {
		t.Errorf("an unset scopeMappings must touch nothing, posted %v", rec.posted)
	}
}

// TestEnsureScopeMappingsMissingRoleErrors covers both role kinds: a role named
// in the config that the realm does not have is a config error, not drift, and
// leaving it silently out of scope would surface much later as a token missing
// a claim.
func TestEnsureScopeMappingsMissingRoleErrors(t *testing.T) {
	cases := map[string]struct {
		roles *config.UserRoles
		want  []string
	}{
		"realm role": {
			roles: &config.UserRoles{Realm: []string{"ghost"}},
			want:  []string{"ghost", "test-realm"},
		},
		"client role": {
			roles: &config.UserRoles{Clients: map[string][]string{"sandbox-ledger": {"ghost"}}},
			want:  []string{"ghost", "sandbox-ledger", "test-realm"},
		},
		"client itself missing": {
			roles: &config.UserRoles{Clients: map[string][]string{"missing-app": {"reader"}}},
			want:  []string{"missing-app", "clients", "bff"},
		},
	}

	for label, tc := range cases {
		t.Run(label, func(t *testing.T) {
			_, url := newScopeMappingServer(t, nil)
			p := New(newTestClient(t, url), nil)

			err := p.ensureScopeMappings(context.Background(), "test-realm", clientScopeOwner("bff-uuid", "bff"), tc.roles)
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got: %v", want, err)
				}
			}
		})
	}
}
