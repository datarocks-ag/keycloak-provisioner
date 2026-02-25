package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": "test-token",
		"expires_in":   300,
	})
}

func testServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", tokenHandler)
	for pattern, h := range handlers {
		mux.HandleFunc(pattern, h)
	}
	return httptest.NewServer(mux)
}

func connectClient(t *testing.T, url string) *Client {
	t.Helper()
	c := New(url, "admin", "admin")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return c
}

func TestNew(t *testing.T) {
	c := New("http://localhost:8080/", "admin", "pass")
	if c.baseURL != "http://localhost:8080" {
		t.Errorf("expected trailing slash stripped, got %s", c.baseURL)
	}
	if c.username != "admin" || c.password != "pass" {
		t.Error("credentials not stored")
	}
}

func TestConnect_Success(t *testing.T) {
	server := testServer(t, nil)
	defer server.Close()

	c := New(server.URL, "admin", "admin")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.accessToken != "test-token" {
		t.Errorf("expected test-token, got %s", c.accessToken)
	}
}

func TestConnect_AuthFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad credentials"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := New(server.URL, "admin", "wrong")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestConnect_CancelledContext(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := New(server.URL, "admin", "admin")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestDoRequest_401Retry(t *testing.T) {
	var attempts atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", tokenHandler)
	mux.HandleFunc("GET /admin/realms/test", func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"realm": "test"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := connectClient(t, server.URL)

	result, err := c.GetRealm(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["realm"] != "test" {
		t.Errorf("expected realm test, got %v", result["realm"])
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestDoRequest_401RetryAuthFails(t *testing.T) {
	var tokenCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		n := tokenCalls.Add(1)
		if n == 1 {
			tokenHandler(w, r)
			return
		}
		// second auth attempt fails
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("auth failed"))
	})
	mux.HandleFunc("GET /admin/realms/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := connectClient(t, server.URL)
	// force token to be expired so ensureToken triggers re-auth
	c.mu.Lock()
	c.expiresAt = time.Now().Add(-1 * time.Minute)
	c.mu.Unlock()

	_, err := c.GetRealm(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error when re-auth fails after 401")
	}
}

// --- Realm tests ---

func TestGetRealm_Found(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test", "enabled": true})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetRealm(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["realm"] != "test" {
		t.Errorf("expected test, got %v", result["realm"])
	}
}

func TestGetRealm_NotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetRealm(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestGetRealm_ServerError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetRealm(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateRealm_Success(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateRealm(context.Background(), map[string]any{"realm": "new-realm"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if body["realm"] != "new-realm" {
		t.Errorf("expected new-realm, got %v", body["realm"])
	}
}

func TestCreateRealm_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("already exists"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateRealm(context.Background(), map[string]any{"realm": "dup"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateRealm_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateRealm(context.Background(), "test", map[string]any{"displayName": "Updated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateRealm_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateRealm(context.Background(), "test", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Client tests ---

func TestGetClients(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("clientId") != "my-app" {
				t.Errorf("expected clientId query param my-app, got %s", r.URL.Query().Get("clientId"))
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "uuid-1", "clientId": "my-app"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetClients(context.Background(), "test", "my-app")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 client, got %d", len(result))
	}
}

func TestGetClients_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetClients(context.Background(), "test", "app")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateClient_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://localhost/admin/realms/test/clients/uuid-abc")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	uuid, err := c.CreateClient(context.Background(), "test", map[string]any{"clientId": "app"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uuid != "uuid-abc" {
		t.Errorf("expected uuid-abc, got %s", uuid)
	}
}

func TestCreateClient_NoLocationHeader(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateClient(context.Background(), "test", map[string]any{"clientId": "app"})
	if err == nil {
		t.Fatal("expected error for missing Location header")
	}
}

func TestCreateClient_BadLocationHeader(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateClient(context.Background(), "test", map[string]any{"clientId": "app"})
	if err == nil {
		t.Fatal("expected error for bad Location header format")
	}
}

func TestCreateClient_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("conflict"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateClient(context.Background(), "test", map[string]any{"clientId": "app"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateClient_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateClient(context.Background(), "test", "uuid-1", map[string]any{"secret": "new"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateClient_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/clients/{uuid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateClient(context.Background(), "test", "uuid-1", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Realm Role tests ---

func TestGetRealmRole_Found(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"name": "admin"})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetRealmRole(context.Background(), "test", "admin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["name"] != "admin" {
		t.Errorf("expected admin, got %v", result["name"])
	}
}

func TestGetRealmRole_NotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetRealmRole(context.Background(), "test", "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestGetRealmRole_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetRealmRole(context.Background(), "test", "admin")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateRealmRole_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/roles": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateRealmRole(context.Background(), "test", map[string]any{"name": "admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRealmRole_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/roles": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("exists"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateRealmRole(context.Background(), "test", map[string]any{"name": "dup"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateRealmRole_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateRealmRole(context.Background(), "test", "admin", map[string]any{"description": "updated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateRealmRole_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateRealmRole(context.Background(), "test", "admin", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Client Role tests ---

func TestGetClientRole_Found(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"name": "editor"})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetClientRole(context.Background(), "test", "uuid-1", "editor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["name"] != "editor" {
		t.Errorf("expected editor, got %v", result["name"])
	}
}

func TestGetClientRole_NotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetClientRole(context.Background(), "test", "uuid-1", "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestGetClientRole_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetClientRole(context.Background(), "test", "uuid-1", "admin")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateClientRole_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients/{uuid}/roles": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateClientRole(context.Background(), "test", "uuid-1", map[string]any{"name": "admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateClientRole_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients/{uuid}/roles": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("exists"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateClientRole(context.Background(), "test", "uuid-1", map[string]any{"name": "dup"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateClientRole_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateClientRole(context.Background(), "test", "uuid-1", "admin", map[string]any{"description": "updated"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateClientRole_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/clients/{uuid}/roles/{name}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateClientRole(context.Background(), "test", "uuid-1", "admin", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Protocol Mapper tests ---

func TestGetProtocolMappers(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "pm-1", "name": "mapper-1"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetProtocolMappers(context.Background(), "test", "uuid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 mapper, got %d", len(result))
	}
}

func TestGetProtocolMappers_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetProtocolMappers(context.Background(), "test", "uuid-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateProtocolMapper_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateProtocolMapper(context.Background(), "test", "uuid-1", map[string]any{"name": "mapper"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateProtocolMapper_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("exists"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.CreateProtocolMapper(context.Background(), "test", "uuid-1", map[string]any{"name": "dup"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateProtocolMapper_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateProtocolMapper(context.Background(), "test", "uuid-1", "pm-1", map[string]any{"name": "mapper"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateProtocolMapper_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/clients/{uuid}/protocol-mappers/models/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateProtocolMapper(context.Background(), "test", "uuid-1", "pm-1", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ensureToken tests ---

func TestEnsureToken_NotExpired(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"realm": "test"})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	// token is fresh, should not re-authenticate
	result, err := c.GetRealm(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestEnsureToken_Expired(t *testing.T) {
	var tokenCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		tokenHandler(w, r)
	})
	mux.HandleFunc("GET /admin/realms/{realm}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"realm": "test"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := connectClient(t, server.URL)
	// 1 token call from Connect

	// Force token expiry
	c.mu.Lock()
	c.expiresAt = time.Now().Add(-1 * time.Minute)
	c.mu.Unlock()

	_, err := c.GetRealm(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tokenCalls.Load() < 2 {
		t.Errorf("expected at least 2 token calls (initial + refresh), got %d", tokenCalls.Load())
	}
}

// --- doRequest with nil body ---

func TestDoRequest_NilBody(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Content-Type") == "application/json" {
				t.Error("Content-Type should not be set for nil body")
			}
			json.NewEncoder(w).Encode(map[string]any{"realm": "test"})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetRealm(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- User tests ---

func TestGetUsers(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("username") != "testuser" {
				t.Errorf("expected username=testuser, got %s", r.URL.Query().Get("username"))
			}
			if r.URL.Query().Get("exact") != "true" {
				t.Errorf("expected exact=true, got %s", r.URL.Query().Get("exact"))
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "user-uuid-1", "username": "testuser"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetUsers(context.Background(), "test", "testuser")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 user, got %d", len(result))
	}
	if result[0]["username"] != "testuser" {
		t.Errorf("expected testuser, got %v", result[0]["username"])
	}
}

func TestGetUsers_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetUsers(context.Background(), "test", "testuser")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateUser_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://localhost/admin/realms/test/users/user-uuid-abc")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	uuid, err := c.CreateUser(context.Background(), "test", map[string]any{"username": "newuser"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uuid != "user-uuid-abc" {
		t.Errorf("expected user-uuid-abc, got %s", uuid)
	}
}

func TestCreateUser_NoLocationHeader(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateUser(context.Background(), "test", map[string]any{"username": "newuser"})
	if err == nil {
		t.Fatal("expected error for missing Location header")
	}
}

func TestCreateUser_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("exists"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateUser(context.Background(), "test", map[string]any{"username": "dup"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateUser_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateUser(context.Background(), "test", "user-uuid-1", map[string]any{"email": "new@example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateUser_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/users/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateUser(context.Background(), "test", "user-uuid-1", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResetUserPassword_Success(t *testing.T) {
	var mu sync.Mutex
	var body map[string]any

	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.ResetUserPassword(context.Background(), "test", "user-uuid-1", "newpassword")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if body["value"] != "newpassword" {
		t.Errorf("expected password newpassword, got %v", body["value"])
	}
	if body["temporary"] != false {
		t.Errorf("expected temporary=false, got %v", body["temporary"])
	}
}

func TestResetUserPassword_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/users/{id}/reset-password": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.ResetUserPassword(context.Background(), "test", "user-uuid-1", "pw")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetUserRealmRoleMappings(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "role-1", "name": "admin"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetUserRealmRoleMappings(context.Background(), "test", "user-uuid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 role, got %d", len(result))
	}
}

func TestAddUserRealmRoleMappings(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.AddUserRealmRoleMappings(context.Background(), "test", "user-uuid-1", []map[string]any{
		{"id": "role-1", "name": "admin"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetUserClientRoleMappings(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "role-1", "name": "editor"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetUserClientRoleMappings(context.Background(), "test", "user-uuid-1", "client-uuid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 role, got %d", len(result))
	}
}

func TestAddUserClientRoleMappings(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.AddUserClientRoleMappings(context.Background(), "test", "user-uuid-1", "client-uuid-1", []map[string]any{
		{"id": "role-1", "name": "editor"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetServiceAccountUser(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":       "sa-user-uuid",
				"username": "service-account-my-client",
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetServiceAccountUser(context.Background(), "test", "client-uuid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["id"] != "sa-user-uuid" {
		t.Errorf("expected sa-user-uuid, got %v", result["id"])
	}
}

func TestGetServiceAccountUser_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients/{uuid}/service-account-user": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetServiceAccountUser(context.Background(), "test", "client-uuid-1")
	if err == nil {
		t.Fatal("expected error")
	}
}
