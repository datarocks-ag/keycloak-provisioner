package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"keycloak-provisioner/internal/config"
)

func TestEnsureRealmCreate(t *testing.T) {
	var mu sync.Mutex
	var createdBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&createdBody)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	enabled := true
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm:   "test-realm",
				Enabled: &enabled,
			},
		},
	}

	p := New(c, cfg)
	if err := p.ensureRealm(context.Background(), cfg.Realms[0], "update"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if createdBody["realm"] != "test-realm" {
		t.Errorf("expected realm test-realm, got %v", createdBody["realm"])
	}
	if createdBody["enabled"] != true {
		t.Errorf("expected enabled true, got %v", createdBody["enabled"])
	}
}

func TestEnsureRealmCreateStrategySkipsExisting(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test-realm"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{Realm: "test-realm"},
		},
	}

	p := New(c, cfg)
	if err := p.ensureRealm(context.Background(), cfg.Realms[0], "create"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called with strategy=create")
	}
}

func TestEnsureRealmUpdate(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test-realm"})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	cfg := &config.Config{
		Realms: []config.Realm{
			{
				Realm:       "test-realm",
				DisplayName: "Updated Realm",
			},
		},
	}

	p := New(c, cfg)
	if err := p.ensureRealm(context.Background(), cfg.Realms[0], "update"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["displayName"] != "Updated Realm" {
		t.Errorf("expected displayName Updated Realm, got %v", updatedBody["displayName"])
	}
}

func TestBuildRealmBodyAllFields(t *testing.T) {
	enabled := true
	regAllowed := false
	resetPw := true
	loginWithEmail := true
	bruteForce := true

	realm := config.Realm{
		Realm:                 "test",
		DisplayName:           "Test Realm",
		Enabled:               &enabled,
		LoginTheme:            "keycloak",
		RegistrationAllowed:   &regAllowed,
		ResetPasswordAllowed:  &resetPw,
		LoginWithEmailAllowed: &loginWithEmail,
		BruteForceProtected:   &bruteForce,
	}

	body := buildRealmBody(realm, nil)

	checks := map[string]any{
		"realm":                 "test",
		"displayName":           "Test Realm",
		"enabled":               true,
		"loginTheme":            "keycloak",
		"registrationAllowed":   false,
		"resetPasswordAllowed":  true,
		"loginWithEmailAllowed": true,
		"bruteForceProtected":   true,
	}

	for key, want := range checks {
		got, ok := body[key]
		if !ok {
			t.Errorf("missing key %q", key)
			continue
		}
		if got != want {
			t.Errorf("key %q: got %v, want %v", key, got, want)
		}
	}
}

func TestBuildRealmBodyWithSslRequired(t *testing.T) {
	realm := config.Realm{
		Realm:       "test",
		SslRequired: "external",
	}

	body := buildRealmBody(realm, nil)

	if body["sslRequired"] != "external" {
		t.Errorf("expected sslRequired=external, got %v", body["sslRequired"])
	}
}

func TestBuildRealmBodyWithoutSslRequired(t *testing.T) {
	realm := config.Realm{
		Realm: "test",
	}

	body := buildRealmBody(realm, nil)

	if _, ok := body["sslRequired"]; ok {
		t.Error("sslRequired should not be set when empty")
	}
}

func TestEnsureMasterRealm(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "none",
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{
		SslRequired: "external",
	}

	p := New(c, &config.Config{MasterRealm: mr})
	if err := p.ensureMasterRealm(context.Background(), mr); err != nil {
		t.Fatalf("ensureMasterRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if updatedBody["sslRequired"] != "external" {
		t.Errorf("expected sslRequired=external, got %v", updatedBody["sslRequired"])
	}
}

func TestEnsureMasterRealmNoChange(t *testing.T) {
	var updateCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "external",
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			updateCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{
		SslRequired: "external",
	}

	p := New(c, &config.Config{MasterRealm: mr})
	if err := p.ensureMasterRealm(context.Background(), mr); err != nil {
		t.Fatalf("ensureMasterRealm: %v", err)
	}

	if updateCalled.Load() {
		t.Error("expected PUT not to be called when sslRequired already matches")
	}
}

func TestEnsureMasterRealmGetError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{SslRequired: "external"}

	p := New(c, &config.Config{MasterRealm: mr})
	err := p.ensureMasterRealm(context.Background(), mr)
	if err == nil {
		t.Fatal("expected error when GetRealm fails")
	}
}

func TestEnsureMasterRealmUpdateError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":       "master",
				"sslRequired": "none",
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{SslRequired: "external"}

	p := New(c, &config.Config{MasterRealm: mr})
	err := p.ensureMasterRealm(context.Background(), mr)
	if err == nil {
		t.Fatal("expected error when UpdateRealm fails")
	}
}

func TestEnsureMasterRealmEmptySslRequired(t *testing.T) {
	var getCalled atomic.Bool

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			getCalled.Store(true)
			json.NewEncoder(w).Encode(map[string]any{"realm": "master"})
		},
	})
	defer server.Close()

	c := newTestClient(t, server.URL)
	mr := &config.MasterRealmConfig{}

	p := New(c, &config.Config{MasterRealm: mr})
	if err := p.ensureMasterRealm(context.Background(), mr); err != nil {
		t.Fatalf("ensureMasterRealm: %v", err)
	}

	if getCalled.Load() {
		t.Error("expected GetRealm not to be called when sslRequired is empty")
	}
}

func TestBuildRealmBodyWithoutAttributesOmitsKey(t *testing.T) {
	realm := config.Realm{Realm: "test"}

	body := buildRealmBody(realm, nil)

	if _, ok := body["attributes"]; ok {
		t.Error("attributes should not be set when the config declares none")
	}
}

func TestBuildRealmBodyMergesExistingAttributes(t *testing.T) {
	realm := config.Realm{
		Realm:      "test",
		Attributes: map[string]string{"frontendUrl": "https://id.example.com"},
	}
	existing := map[string]any{
		"realm": "test",
		"attributes": map[string]any{
			"userProfileEnabled": "true",
			"frontendUrl":        "https://old.example.com",
		},
	}

	body := buildRealmBody(realm, existing)

	attrs, ok := body["attributes"].(map[string]string)
	if !ok {
		t.Fatalf("expected attributes map, got %T", body["attributes"])
	}
	if got := attrs["userProfileEnabled"]; got != "true" {
		t.Errorf("unmanaged attribute was dropped, got %q", got)
	}
	if got := attrs["frontendUrl"]; got != "https://id.example.com" {
		t.Errorf("configured attribute should win, got %q", got)
	}
}

func TestBuildRealmBodyAcrLoaMap(t *testing.T) {
	realm := config.Realm{
		Realm:     "test",
		AcrLoaMap: map[string]int{"gold": 2, "silver": 1},
	}

	body := buildRealmBody(realm, nil)

	attrs, ok := body["attributes"].(map[string]string)
	if !ok {
		t.Fatalf("expected attributes map, got %T", body["attributes"])
	}
	if got := attrs[acrLoaMapAttr]; got != `{"gold":2,"silver":1}` {
		t.Errorf("unexpected acr.loa.map: %q", got)
	}
}

func TestBuildRealmBodyAcrLoaMapWinsOverRawAttribute(t *testing.T) {
	realm := config.Realm{
		Realm:      "test",
		Attributes: map[string]string{acrLoaMapAttr: `{"bronze":0}`},
		AcrLoaMap:  map[string]int{"gold": 2},
	}

	body := buildRealmBody(realm, nil)

	attrs := body["attributes"].(map[string]string)
	if got := attrs[acrLoaMapAttr]; got != `{"gold":2}` {
		t.Errorf("typed acrLoaMap should win, got %q", got)
	}
}

func TestBuildRealmBodyOrganizationsEnabled(t *testing.T) {
	enabled := true
	realm := config.Realm{Realm: "test", OrganizationsEnabled: &enabled}

	body := buildRealmBody(realm, nil)

	if body["organizationsEnabled"] != true {
		t.Errorf("expected organizationsEnabled=true, got %v", body["organizationsEnabled"])
	}
}

func TestBuildRealmBodyOrganizationsEnabledUnsetOmitsKey(t *testing.T) {
	body := buildRealmBody(config.Realm{Realm: "test"}, nil)

	if _, ok := body["organizationsEnabled"]; ok {
		t.Error("organizationsEnabled should not be set when unset")
	}
}

func TestBuildRealmBodyLoginAndBruteForceFalse(t *testing.T) {
	off := false
	realm := config.Realm{Realm: "test", LoginWithEmailAllowed: &off, BruteForceProtected: &off}

	body := buildRealmBody(realm, nil)

	if body["loginWithEmailAllowed"] != false {
		t.Errorf("expected loginWithEmailAllowed=false, got %v", body["loginWithEmailAllowed"])
	}
	if body["bruteForceProtected"] != false {
		t.Errorf("expected bruteForceProtected=false, got %v", body["bruteForceProtected"])
	}
}

func TestBuildRealmBodyLoginAndBruteForceUnsetOmitsKeys(t *testing.T) {
	body := buildRealmBody(config.Realm{Realm: "test"}, nil)

	if _, ok := body["loginWithEmailAllowed"]; ok {
		t.Error("loginWithEmailAllowed should not be set when unset")
	}
	if _, ok := body["bruteForceProtected"]; ok {
		t.Error("bruteForceProtected should not be set when unset")
	}
}

func TestEnsureRealmUpdateSendsMergedAttributes(t *testing.T) {
	var mu sync.Mutex
	var updatedBody map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"realm":      "test",
				"attributes": map[string]any{"keptByKeycloak": "yes"},
			})
		},
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&updatedBody)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	p := New(newTestClient(t, server.URL), &config.Config{})
	realm := config.Realm{
		Realm:      "test",
		Attributes: map[string]string{"frontendUrl": "https://id.example.com"},
	}

	if err := p.ensureRealm(context.Background(), realm, "update"); err != nil {
		t.Fatalf("ensureRealm: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	attrs, ok := updatedBody["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("expected attributes in update body, got %T", updatedBody["attributes"])
	}
	if attrs["keptByKeycloak"] != "yes" {
		t.Error("existing realm attribute was clobbered by the update")
	}
	if attrs["frontendUrl"] != "https://id.example.com" {
		t.Errorf("configured attribute missing, got %v", attrs["frontendUrl"])
	}
}
