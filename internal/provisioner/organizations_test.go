package provisioner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"keycloak-provisioner/internal/config"
)

func TestBuildOrganizationBodyIncludesAliasOnlyOnCreate(t *testing.T) {
	o := config.Organization{Name: "acme", Alias: "acme-corp"}

	created := buildOrganizationBody(o, true)
	if created["alias"] != "acme-corp" {
		t.Errorf("expected alias on create, got %v", created["alias"])
	}

	updated := buildOrganizationBody(o, false)
	if _, ok := updated["alias"]; ok {
		t.Error("alias must be omitted on update; Keycloak treats it as immutable")
	}
}

func TestBuildOrganizationBodyDomains(t *testing.T) {
	verified := true
	o := config.Organization{
		Name: "acme",
		Domains: []config.OrganizationDomain{
			{Name: "acme.com", Verified: &verified},
			{Name: "acme.org"},
		},
	}

	body := buildOrganizationBody(o, true)

	domains, ok := body["domains"].([]map[string]any)
	if !ok || len(domains) != 2 {
		t.Fatalf("unexpected domains: %v", body["domains"])
	}
	if domains[0]["name"] != "acme.com" || domains[0]["verified"] != true {
		t.Errorf("unexpected first domain: %v", domains[0])
	}
	if _, ok := domains[1]["verified"]; ok {
		t.Error("verified should be omitted when unset")
	}
}

func TestEnsureOrganizationCreatesWhenMissing(t *testing.T) {
	var mu sync.Mutex
	var created map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&created)
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Domains: []config.OrganizationDomain{{Name: "acme.com"}}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if created["name"] != "acme" {
		t.Errorf("unexpected created body: %v", created)
	}
}

func TestEnsureOrganizationCreateStrategySkipsExisting(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("update must not be called with strategy=create")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme"}

	if err := p.ensureOrganization(context.Background(), "test", o, "create"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}
}

func TestEnsureOrganizationMembersAddsMissing(t *testing.T) {
	var mu sync.Mutex
	var addedBodies []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("username") == "bob" {
				json.NewEncoder(w).Encode([]map[string]any{{"id": "u-2", "username": "bob"}})
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading member request body: %v", err)
			}
			addedBodies = append(addedBodies, string(body))
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Members: []string{"alice", "bob"}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(addedBodies) != 1 {
		t.Fatalf("expected only bob to be added, got %d calls: %v", len(addedBodies), addedBodies)
	}
	// The member endpoint takes the user ID as a bare JSON string.
	if addedBodies[0] != `"u-2"` {
		t.Errorf("expected quoted user id as body, got %s", addedBodies[0])
	}
}

func TestEnsureOrganizationMembersWarnsOnMissingUser(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not add a member that does not exist")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Members: []string{"ghost"}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("missing user should be skipped, got error: %v", err)
	}
}

// orgGroupCalls records the organization-group creations a test server sees.
type orgGroupCalls struct {
	mu            sync.Mutex
	createdGroups []string
	createdSubs   []string
}

func TestEnsureOrganizationGroupCreatesTreeInOrder(t *testing.T) {
	calls := &orgGroupCalls{}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"POST /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			calls.mu.Lock()
			calls.createdGroups = append(calls.createdGroups, name)
			calls.mu.Unlock()
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1/groups/g-"+name)
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			calls.mu.Lock()
			calls.createdSubs = append(calls.createdSubs, r.PathValue("gid")+"/"+name)
			calls.mu.Unlock()
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1/groups/g-"+name)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name: "engineering",
			SubGroups: []config.OrganizationGroup{
				{Name: "backend"},
				{Name: "frontend"},
			},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	calls.mu.Lock()
	defer calls.mu.Unlock()

	if len(calls.createdGroups) != 1 || calls.createdGroups[0] != "engineering" {
		t.Errorf("unexpected top-level groups: %v", calls.createdGroups)
	}
	want := []string{"g-engineering/backend", "g-engineering/frontend"}
	if len(calls.createdSubs) != 2 || calls.createdSubs[0] != want[0] || calls.createdSubs[1] != want[1] {
		t.Errorf("unexpected subgroups: %v, want %v", calls.createdSubs, want)
	}
}

func TestEnsureOrganizationGroupUpdatesExistingWithAttributes(t *testing.T) {
	var mu sync.Mutex
	var updated map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updated)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name:       "engineering",
			Attributes: map[string][]string{"tier": {"gold"}},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updated["id"] != "g-1" {
		t.Errorf("update should carry the group id, got %v", updated["id"])
	}
	attrs, ok := updated["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes, got %T", updated["attributes"])
	}
	if tier, ok := attrs["tier"].([]any); !ok || len(tier) != 1 || tier[0] != "gold" {
		t.Errorf("unexpected attributes: %v", attrs)
	}
}

func TestEnsureOrganizationGroupSkipsNonOrgMember(t *testing.T) {
	var mu sync.Mutex
	var added []string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": r.URL.Query().Get("username")}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			added = append(added, r.PathValue("uid"))
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name: "engineering",
			// alice is an org member; bob is not, so Keycloak would answer 400.
			Members: []string{"alice", "bob"},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(added) != 1 {
		t.Fatalf("expected only the org member to be added, got %v", added)
	}
}

func TestEnsureOrganizationGroupSkipsExistingMembership(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not re-add an existing group member")
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name:   "acme",
		Groups: []config.OrganizationGroup{{Name: "engineering", Members: []string{"alice"}}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}
}

func TestBuildOrganizationGroupBody(t *testing.T) {
	plain := buildOrganizationGroupBody(config.OrganizationGroup{Name: "engineering"})
	if plain["name"] != "engineering" {
		t.Errorf("unexpected name: %v", plain["name"])
	}
	if _, ok := plain["attributes"]; ok {
		t.Error("attributes should be omitted when empty")
	}

	withAttrs := buildOrganizationGroupBody(config.OrganizationGroup{
		Name:       "engineering",
		Attributes: map[string][]string{"tier": {"gold"}},
	})
	if _, ok := withAttrs["attributes"].(map[string][]string); !ok {
		t.Errorf("unexpected attributes type: %T", withAttrs["attributes"])
	}
}

// TestEnsureOrganizationGroupsFetchMembersOnce pins the fix for the repeated
// member reads: the organization's member listing is read once per
// organization and carried through the group recursion, and the group path
// resolves user ids from it rather than calling GetUsers per member.
func TestEnsureOrganizationGroupsFetchMembersOnce(t *testing.T) {
	var mu sync.Mutex
	orgMemberReads, userLookups := 0, 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			orgMemberReads++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			userLookups++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-2", "name": "backend"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{
		Name: "acme",
		Groups: []config.OrganizationGroup{{
			Name:    "engineering",
			Members: []string{"alice"},
			SubGroups: []config.OrganizationGroup{
				{Name: "backend", Members: []string{"alice"}},
			},
		}},
	}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if orgMemberReads != 1 {
		t.Errorf("organization members should be read once for the whole tree, got %d reads", orgMemberReads)
	}
	if userLookups != 0 {
		t.Errorf("group membership must resolve ids from the member listing, got %d user lookups", userLookups)
	}
}

func TestEnsureOrganizationGroupsSkipMemberReadWhenNoneAssigned(t *testing.T) {
	var mu sync.Mutex
	orgMemberReads := 0

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			orgMemberReads++
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	o := config.Organization{Name: "acme", Groups: []config.OrganizationGroup{{Name: "engineering"}}}

	if err := p.ensureOrganization(context.Background(), "test", o, "update"); err != nil {
		t.Fatalf("ensureOrganization: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if orgMemberReads != 0 {
		t.Errorf("no group assigns members, so the member listing should not be read; got %d", orgMemberReads)
	}
}
