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

// permServer serves the endpoints a management permission reconcile touches and
// records every write, so a test can assert both what changed and what did not.
//
// The seeds mirror the three pieces of server state that decide the reconcile:
// whether permissions are on, which policies already exist, and which are
// already attached to the permission.
type permServer struct {
	mu sync.Mutex

	permSeed

	// Recorded writes.
	clientLookups int
	enabledPUTs   int
	createdPolicy []map[string]any
	updatedPolicy []map[string]any
	updatedPerm   []map[string]any
}

// permSeed is the server state a test starts from. It is separate from
// permServer so a test can pass it by value without copying the mutex.
type permSeed struct {
	// enabled is whether fine-grained permissions are already on for the target.
	enabled bool
	// policies are the client policies the resource server already holds,
	// keyed by name.
	policies map[string]map[string]any
	// associated are the policy ids already attached to the token-exchange
	// permission.
	associated []map[string]any
	// permission is the scope permission's own representation.
	permission map[string]any
}

const (
	targetUUID     = "target-uuid"
	tokenExchangeP = "perm-token-exchange"
)

// affirmativePermission is the scope permission as this provisioner leaves it.
func affirmativePermission() map[string]any {
	return map[string]any{
		"id":               tokenExchangeP,
		"name":             "token-exchange.permission.client." + targetUUID,
		"type":             "scope",
		"logic":            "POSITIVE",
		"decisionStrategy": "AFFIRMATIVE",
	}
}

func newPermServer(t *testing.T, seed permSeed) (*permServer, string) {
	t.Helper()

	rec := &permServer{permSeed: seed}
	if rec.policies == nil {
		rec.policies = map[string]map[string]any{}
	}
	if rec.permission == nil {
		rec.permission = map[string]any{
			"id":               tokenExchangeP,
			"name":             "token-exchange.permission.client." + targetUUID,
			"type":             "scope",
			"logic":            "POSITIVE",
			"decisionStrategy": "UNANIMOUS",
		}
	}

	// scopePermissions as Keycloak reports it once permissions are enabled.
	scopePermissions := map[string]any{
		"view":                   "perm-view",
		"manage":                 "perm-manage",
		"configure":              "perm-configure",
		"map-roles":              "perm-map-roles",
		"map-roles-client-scope": "perm-map-roles-client-scope",
		"map-roles-composite":    "perm-map-roles-composite",
		"token-exchange":         tokenExchangeP,
	}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			rec.clientLookups++
			rec.mu.Unlock()

			clientID := r.URL.Query().Get("clientId")
			if clientID == "ghost" {
				json.NewEncoder(w).Encode([]map[string]any{})

				return
			}
			json.NewEncoder(w).Encode([]map[string]any{{"id": clientID + "-uuid", "clientId": clientID}})
		},

		"GET /admin/realms/{realm}/clients/{uuid}/management/permissions": func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			defer rec.mu.Unlock()

			if !rec.enabled {
				json.NewEncoder(w).Encode(map[string]any{"enabled": false})

				return
			}
			json.NewEncoder(w).Encode(map[string]any{"enabled": true, "scopePermissions": scopePermissions})
		},

		"PUT /admin/realms/{realm}/clients/{uuid}/management/permissions": func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			rec.enabledPUTs++
			rec.enabled = true
			rec.mu.Unlock()

			json.NewEncoder(w).Encode(map[string]any{"enabled": true, "scopePermissions": scopePermissions})
		},

		// Keycloak matches the name as a substring, and the reconciler must
		// cope with that, so this deliberately does too.
		"GET /admin/realms/{realm}/clients/{uuid}/authz/resource-server/policy/client": func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query().Get("name")

			rec.mu.Lock()
			defer rec.mu.Unlock()

			out := []map[string]any{}

			for name, policy := range rec.policies {
				if strings.Contains(name, query) {
					out = append(out, policy)
				}
			}

			json.NewEncoder(w).Encode(out)
		},

		"POST /admin/realms/{realm}/clients/{uuid}/authz/resource-server/policy/client": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding created policy: %v", err)
			}

			name, _ := body["name"].(string)

			rec.mu.Lock()
			if _, exists := rec.policies[name]; exists {
				rec.mu.Unlock()
				w.WriteHeader(http.StatusConflict)

				return
			}

			rec.createdPolicy = append(rec.createdPolicy, body)

			created := map[string]any{"id": name + "-id", "name": name, "type": "client", "clients": body["clients"]}
			rec.policies[name] = created
			rec.mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(created)
		},

		"PUT /admin/realms/{realm}/clients/{uuid}/authz/resource-server/policy/client/{id}": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding updated policy: %v", err)
			}

			rec.mu.Lock()
			rec.updatedPolicy = append(rec.updatedPolicy, body)
			rec.mu.Unlock()

			// Measured: the authz endpoints answer 201 where the rest of the
			// admin API answers 204.
			w.WriteHeader(http.StatusCreated)
		},

		"GET /admin/realms/{realm}/clients/{uuid}/authz/resource-server/policy/{id}/associatedPolicies": func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			defer rec.mu.Unlock()

			if rec.associated == nil {
				json.NewEncoder(w).Encode([]map[string]any{})

				return
			}
			json.NewEncoder(w).Encode(rec.associated)
		},

		"GET /admin/realms/{realm}/clients/{uuid}/authz/resource-server/permission/scope/{id}": func(w http.ResponseWriter, r *http.Request) {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			json.NewEncoder(w).Encode(rec.permission)
		},

		"PUT /admin/realms/{realm}/clients/{uuid}/authz/resource-server/permission/scope/{id}": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding updated permission: %v", err)
			}

			rec.mu.Lock()
			rec.updatedPerm = append(rec.updatedPerm, body)
			rec.mu.Unlock()

			w.WriteHeader(http.StatusCreated)
		},
	})
	t.Cleanup(server.Close)

	return rec, server.URL
}

// tokenExchangeTo builds a client granting the token-exchange scope to the
// named clients.
func tokenExchangeTo(granted ...string) config.Client {
	return config.Client{
		ClientID: "target",
		ManagementPermissions: &config.ManagementPermissions{
			Scopes: map[string]config.ManagementPermissionScope{
				"token-exchange": {Clients: granted},
			},
		},
	}
}

func runPermissions(t *testing.T, url string, c config.Client) error {
	t.Helper()

	return runPermissionsIndexed(t, url, c, clientUUIDIndex{})
}

func runPermissionsIndexed(t *testing.T, url string, c config.Client, clients clientUUIDIndex) error {
	t.Helper()

	p := New(newTestClient(t, url), &config.Config{})

	return p.ensureManagementPermissions(context.Background(), "test", targetUUID, c, clients)
}

// stringsIn reads a JSON array of strings out of a decoded request body.
func stringsIn(t *testing.T, v any) []string {
	t.Helper()

	return stringsFrom(v)
}

func TestManagementPermissionsEnablesCreatesAndAttaches(t *testing.T) {
	rec, url := newPermServer(t, permSeed{enabled: false})

	if err := runPermissions(t, url, tokenExchangeTo("bff")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if rec.enabledPUTs != 1 {
		t.Errorf("expected permissions to be enabled once, got %d PUTs", rec.enabledPUTs)
	}

	if len(rec.createdPolicy) != 1 {
		t.Fatalf("expected one policy to be created, got %d", len(rec.createdPolicy))
	}

	created := rec.createdPolicy[0]
	if got := created["name"]; got != "keycloak-provisioner.token-exchange.target" {
		t.Errorf("policy name = %v", got)
	}
	if got := stringsIn(t, created["clients"]); len(got) != 1 || got[0] != "bff-uuid" {
		t.Errorf("policy clients = %v, want the granted client's UUID", got)
	}

	if len(rec.updatedPerm) != 1 {
		t.Fatalf("expected the permission to be updated once, got %d", len(rec.updatedPerm))
	}

	perm := rec.updatedPerm[0]
	if got := stringsIn(t, perm["policies"]); len(got) != 1 || got[0] != "keycloak-provisioner.token-exchange.target-id" {
		t.Errorf("attached policies = %v", got)
	}

	// UNANIMOUS would make a permission carrying more than one policy grant
	// only when every policy passes, which is not what a grant means here.
	if got := perm["decisionStrategy"]; got != "AFFIRMATIVE" {
		t.Errorf("decisionStrategy = %v, want AFFIRMATIVE", got)
	}

	// Fields the reconciler does not manage must survive the write, since the
	// endpoint replaces the whole representation.
	if got := perm["logic"]; got != "POSITIVE" {
		t.Errorf("logic = %v, want the existing value to be preserved", got)
	}
}

// TestManagementPermissionsIsIdempotent pins the property the whole feature
// exists for: a realm already in the configured state is not rewritten.
func TestManagementPermissionsIsIdempotent(t *testing.T) {
	const policyName = "keycloak-provisioner.token-exchange.target"

	rec, url := newPermServer(t, permSeed{
		enabled: true,
		policies: map[string]map[string]any{
			policyName: {"id": policyName + "-id", "name": policyName, "clients": []any{"bff-uuid"}},
		},
		associated: []map[string]any{{"id": policyName + "-id", "name": policyName}},
		// A realm this provisioner has already applied is AFFIRMATIVE. Seeding
		// Keycloak's UNANIMOUS default here would describe a realm in the broken
		// state the reconciler exists to repair — see
		// TestManagementPermissionsRepairsDecisionStrategy.
		permission: affirmativePermission(),
	})

	if err := runPermissions(t, url, tokenExchangeTo("bff")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if rec.enabledPUTs != 0 {
		t.Errorf("permissions already enabled, but they were PUT %d times", rec.enabledPUTs)
	}
	if len(rec.createdPolicy) != 0 {
		t.Errorf("policy already present, but %d were created", len(rec.createdPolicy))
	}
	if len(rec.updatedPolicy) != 0 {
		t.Errorf("policy already correct, but %d updates were sent", len(rec.updatedPolicy))
	}
	if len(rec.updatedPerm) != 0 {
		t.Errorf("policy already attached, but %d permission updates were sent", len(rec.updatedPerm))
	}
}

// TestManagementPermissionsClientListIsAuthoritative is the deliberate
// exception to this tool's additive rule: the provisioner owns the policy it
// named, so dropping a client from the config withdraws that grant.
func TestManagementPermissionsClientListIsAuthoritative(t *testing.T) {
	const policyName = "keycloak-provisioner.token-exchange.target"

	rec, url := newPermServer(t, permSeed{
		enabled: true,
		policies: map[string]map[string]any{
			policyName: {
				"id":               policyName + "-id",
				"name":             policyName,
				"logic":            "POSITIVE",
				"decisionStrategy": "UNANIMOUS",
				"clients":          []any{"bff-uuid", "legacy-uuid"},
			},
		},
		associated: []map[string]any{{"id": policyName + "-id", "name": policyName}},
	})

	if err := runPermissions(t, url, tokenExchangeTo("bff")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if len(rec.updatedPolicy) != 1 {
		t.Fatalf("expected the policy to be rewritten once, got %d", len(rec.updatedPolicy))
	}

	got := stringsIn(t, rec.updatedPolicy[0]["clients"])
	if len(got) != 1 || got[0] != "bff-uuid" {
		t.Errorf("clients = %v, want only bff-uuid — legacy should have been withdrawn", got)
	}

	// The policy's own settings are not the provisioner's to reset.
	if s := rec.updatedPolicy[0]["decisionStrategy"]; s != "UNANIMOUS" {
		t.Errorf("policy decisionStrategy = %v, want the existing value preserved", s)
	}
}

// TestManagementPermissionsReorderedClientsAreNotRewritten guards against a
// write on every run: Keycloak does not preserve the order of a policy's
// clients.
func TestManagementPermissionsReorderedClientsAreNotRewritten(t *testing.T) {
	const policyName = "keycloak-provisioner.token-exchange.target"

	rec, url := newPermServer(t, permSeed{
		enabled: true,
		policies: map[string]map[string]any{
			policyName: {"id": policyName + "-id", "name": policyName, "clients": []any{"b-uuid", "a-uuid"}},
		},
		associated: []map[string]any{{"id": policyName + "-id", "name": policyName}},
	})

	if err := runPermissions(t, url, tokenExchangeTo("a", "b")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if len(rec.updatedPolicy) != 0 {
		t.Errorf("same clients in a different order, but %d updates were sent", len(rec.updatedPolicy))
	}
}

// TestManagementPermissionsAttachIsAdditive covers the other half of the
// ownership split: a policy attached by hand keeps working.
func TestManagementPermissionsAttachIsAdditive(t *testing.T) {
	rec, url := newPermServer(t, permSeed{
		enabled:    true,
		associated: []map[string]any{{"id": "hand-made-id", "name": "someone-elses-policy"}},
	})

	if err := runPermissions(t, url, tokenExchangeTo("bff")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if len(rec.updatedPerm) != 1 {
		t.Fatalf("expected one permission update, got %d", len(rec.updatedPerm))
	}

	got := stringsIn(t, rec.updatedPerm[0]["policies"])
	if len(got) != 2 || got[0] != "hand-made-id" {
		t.Errorf("policies = %v, want the existing policy kept alongside the new one", got)
	}
}

// TestManagementPermissionsIgnoresSubstringMatches pins the filter on the
// search results. Keycloak matches a policy name as a substring, so without an
// exact comparison the reconciler would adopt and rewrite another client's
// policy.
func TestManagementPermissionsIgnoresSubstringMatches(t *testing.T) {
	const other = "keycloak-provisioner.token-exchange.target-admin"

	rec, url := newPermServer(t, permSeed{
		enabled: true,
		policies: map[string]map[string]any{
			other: {"id": other + "-id", "name": other, "clients": []any{"someone-else-uuid"}},
		},
	})

	if err := runPermissions(t, url, tokenExchangeTo("bff")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if len(rec.updatedPolicy) != 0 {
		t.Errorf("rewrote a policy belonging to a different client: %v", rec.updatedPolicy)
	}
	if len(rec.createdPolicy) != 1 {
		t.Fatalf("expected its own policy to be created, got %d", len(rec.createdPolicy))
	}
	if got := rec.createdPolicy[0]["name"]; got != "keycloak-provisioner.token-exchange.target" {
		t.Errorf("created policy name = %v", got)
	}
}

// TestManagementPermissionsAppliesEveryScopeSorted covers the map key handling:
// the config carries several scopes and the run order must not depend on Go's
// randomised map iteration.
func TestManagementPermissionsAppliesEveryScopeSorted(t *testing.T) {
	rec, url := newPermServer(t, permSeed{enabled: true})

	c := config.Client{
		ClientID: "target",
		ManagementPermissions: &config.ManagementPermissions{
			Scopes: map[string]config.ManagementPermissionScope{
				"token-exchange": {Clients: []string{"bff"}},
				"map-roles":      {Clients: []string{"bff"}},
				"view":           {Clients: []string{"ops"}},
			},
		},
	}

	if err := runPermissions(t, url, c); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	var names []string
	for _, p := range rec.createdPolicy {
		names = append(names, p["name"].(string))
	}

	want := []string{
		"keycloak-provisioner.map-roles.target",
		"keycloak-provisioner.token-exchange.target",
		"keycloak-provisioner.view.target",
	}

	if len(names) != len(want) {
		t.Fatalf("created policies = %v, want %v", names, want)
	}

	for i := range want {
		if names[i] != want[i] {
			t.Errorf("created policies = %v, want %v (sorted by scope)", names, want)

			break
		}
	}
}

func TestManagementPermissionsUnknownScopeIsRejected(t *testing.T) {
	_, url := newPermServer(t, permSeed{enabled: true})

	c := config.Client{
		ClientID: "target",
		ManagementPermissions: &config.ManagementPermissions{
			Scopes: map[string]config.ManagementPermissionScope{
				"token-exchagne": {Clients: []string{"bff"}},
			},
		},
	}

	err := runPermissions(t, url, c)
	if err == nil {
		t.Fatal("expected an error for a scope the server does not offer")
	}

	// The message has to name what is available, since the typo is the likely
	// cause and Keycloak itself never sees the request.
	if !strings.Contains(err.Error(), "token-exchagne") || !strings.Contains(err.Error(), "token-exchange") {
		t.Errorf("error should name both the bad scope and the available ones, got: %v", err)
	}
}

func TestManagementPermissionsMissingGrantedClientIsRejected(t *testing.T) {
	_, url := newPermServer(t, permSeed{enabled: true})

	err := runPermissions(t, url, tokenExchangeTo("ghost"))
	if err == nil {
		t.Fatal("expected an error for a client that does not exist")
	}

	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing client, got: %v", err)
	}
}

func TestManagementPermissionsAbsentBlockDoesNothing(t *testing.T) {
	rec, url := newPermServer(t, permSeed{enabled: false})

	if err := runPermissions(t, url, config.Client{ClientID: "target"}); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if rec.enabledPUTs != 0 || len(rec.createdPolicy) != 0 || len(rec.updatedPerm) != 0 {
		t.Error("a client without managementPermissions must not touch the permission API")
	}
}

// TestManagementPermissionsResolvesEachClientOnce pins the index. A grantee
// named by several scopes, and realm-management itself, were each re-resolved
// per use before it existed.
func TestManagementPermissionsResolvesEachClientOnce(t *testing.T) {
	rec, url := newPermServer(t, permSeed{enabled: true})

	c := config.Client{
		ClientID: "target",
		ManagementPermissions: &config.ManagementPermissions{
			Scopes: map[string]config.ManagementPermissionScope{
				"token-exchange": {Clients: []string{"bff"}},
				"map-roles":      {Clients: []string{"bff"}},
				"view":           {Clients: []string{"bff"}},
			},
		},
	}

	if err := runPermissions(t, url, c); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	// One for realm-management, one for the grantee — not one per scope.
	if rec.clientLookups != 2 {
		t.Errorf("client lookups = %d, want 2 (realm-management and bff, each resolved once)", rec.clientLookups)
	}
}

// TestManagementPermissionsUsesTheSeededIndex covers the other half: a grantee
// the client loop already resolved costs no lookup at all.
func TestManagementPermissionsUsesTheSeededIndex(t *testing.T) {
	rec, url := newPermServer(t, permSeed{enabled: true})

	seeded := clientUUIDIndex{
		"bff":                   "bff-uuid",
		realmManagementClientID: "realm-management-uuid",
	}

	if err := runPermissionsIndexed(t, url, tokenExchangeTo("bff"), seeded); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if rec.clientLookups != 0 {
		t.Errorf("client lookups = %d, want 0 — both clients were already known", rec.clientLookups)
	}
}

// TestManagementPermissionsRepairsDecisionStrategy covers a grant that is
// attached and inert.
//
// Under UNANIMOUS a permission grants only when every attached policy passes, so
// a second policy added out of band silently disables the configured grant.
// Skipping the permission because the policy is already attached would leave
// that unrepaired, which is the failure this reconciler is least able to see:
// everything it manages looks present.
func TestManagementPermissionsRepairsDecisionStrategy(t *testing.T) {
	const policyName = "keycloak-provisioner.token-exchange.target"

	rec, url := newPermServer(t, permSeed{
		enabled: true,
		policies: map[string]map[string]any{
			policyName: {"id": policyName + "-id", "name": policyName, "clients": []any{"bff-uuid"}},
		},
		associated: []map[string]any{
			{"id": policyName + "-id", "name": policyName},
			{"id": "hand-made-id", "name": "someone-elses-policy"},
		},
		// Keycloak's default, and what an out-of-band edit leaves behind.
		permission: map[string]any{
			"id": tokenExchangeP, "type": "scope", "logic": "POSITIVE", "decisionStrategy": "UNANIMOUS",
		},
	})

	if err := runPermissions(t, url, tokenExchangeTo("bff")); err != nil {
		t.Fatalf("ensureManagementPermissions: %v", err)
	}

	if len(rec.updatedPerm) != 1 {
		t.Fatalf("expected the permission to be corrected once, got %d updates", len(rec.updatedPerm))
	}

	if got := rec.updatedPerm[0]["decisionStrategy"]; got != "AFFIRMATIVE" {
		t.Errorf("decisionStrategy = %v, want AFFIRMATIVE", got)
	}

	// Repairing the strategy must not detach anything, its own policy included.
	got := stringsIn(t, rec.updatedPerm[0]["policies"])
	if len(got) != 2 {
		t.Errorf("policies = %v, want both to survive the correction", got)
	}

	// The policy is already correct, so nothing about it should be rewritten.
	if len(rec.updatedPolicy) != 0 || len(rec.createdPolicy) != 0 {
		t.Errorf("policy was rewritten while only the strategy needed fixing")
	}
}

func TestSameStringSetIgnoresDuplicates(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"duplicate on the server side", []string{"x", "x"}, []string{"x"}, true},
		{"duplicate on the config side", []string{"x"}, []string{"x", "x"}, true},
		{"reordered", []string{"x", "y"}, []string{"y", "x"}, true},
		{"different values", []string{"x"}, []string{"y"}, false},
		{"subset is not equal", []string{"x", "y"}, []string{"x"}, false},
		{"both empty", nil, nil, true},
	}

	for _, c := range cases {
		if got := sameStringSet(c.a, c.b); got != c.want {
			t.Errorf("%s: sameStringSet(%v, %v) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

// TestStringsFromAcceptsBothShapes pins the two shapes a policy's client list
// arrives in.
//
// Keycloak's own responses decode to []any, but the dry-run adapter stores what
// the reconciler handed it, which is []string. A helper that understands only
// the first silently returns nothing for the second — and "nothing" is a valid
// answer here, meaning "this policy grants to no one", so the mistake reads as a
// difference and rewrites the policy rather than failing.
func TestStringsFromAcceptsBothShapes(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"decoded from JSON", []any{"a", "b"}, []string{"a", "b"}},
		{"stored by the dry-run adapter", []string{"a", "b"}, []string{"a", "b"}},
		{"mixed junk is skipped", []any{"a", 7, nil, "b"}, []string{"a", "b"}},
		{"absent", nil, nil},
	}

	for _, c := range cases {
		got := stringsFrom(c.in)

		if len(got) != len(c.want) {
			t.Errorf("%s: stringsFrom(%v) = %v, want %v", c.name, c.in, got, c.want)

			continue
		}

		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%s: stringsFrom(%v) = %v, want %v", c.name, c.in, got, c.want)

				break
			}
		}
	}
}
