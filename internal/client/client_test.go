package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	result, err := c.GetProtocolMappers(context.Background(), "test", MapperContainerClients, "uuid-1")
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
	_, err := c.GetProtocolMappers(context.Background(), "test", MapperContainerClients, "uuid-1")
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
	err := c.CreateProtocolMapper(context.Background(), "test", MapperContainerClients, "uuid-1", map[string]any{"name": "mapper"})
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
	err := c.CreateProtocolMapper(context.Background(), "test", MapperContainerClients, "uuid-1", map[string]any{"name": "dup"})
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
	err := c.UpdateProtocolMapper(context.Background(), "test", MapperContainerClients, "uuid-1", "pm-1", map[string]any{"name": "mapper"})
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
	err := c.UpdateProtocolMapper(context.Background(), "test", MapperContainerClients, "uuid-1", "pm-1", map[string]any{})
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
	err := c.ResetUserPassword(context.Background(), "test", "user-uuid-1", "newpassword", false)
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
	err := c.ResetUserPassword(context.Background(), "test", "user-uuid-1", "pw", false)
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
	result, err := c.GetRealmRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1")
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
	err := c.AddRealmRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1", []map[string]any{
		{"id": "role-1", "name": "admin"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetUserRealmRoleMappings_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetRealmRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAddUserRealmRoleMappings_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.AddRealmRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1", []map[string]any{
		{"id": "role-1", "name": "admin"},
	})
	if err == nil {
		t.Fatal("expected error")
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
	result, err := c.GetClientRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1", "client-uuid-1")
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
	err := c.AddClientRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1", "client-uuid-1", []map[string]any{
		{"id": "role-1", "name": "editor"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetUserClientRoleMappings_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetClientRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1", "client-uuid-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAddUserClientRoleMappings_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/users/{id}/role-mappings/clients/{clientUUID}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.AddClientRoleMappings(context.Background(), "test", RoleSubjectUsers, "user-uuid-1", "client-uuid-1", []map[string]any{
		{"id": "role-1", "name": "editor"},
	})
	if err == nil {
		t.Fatal("expected error")
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

func TestConnect_RejectsInvalidURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{"empty", ""},
		{"missing scheme", "keycloak.example.com:8080"},
		{"unsupported scheme", "ftp://keycloak.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(tc.baseURL, "admin", "admin")
			if err := c.Connect(context.Background()); err == nil {
				t.Fatal("expected error for invalid URL")
			}
		})
	}
}

func TestAuthenticate_RejectsEmptyAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "",
			"expires_in":   300,
		})
	}))
	defer server.Close()

	c := New(server.URL, "admin", "admin")
	if err := c.authenticate(context.Background()); err == nil {
		t.Fatal("expected error for empty access token")
	}
}

// --- Group Membership tests ---

func TestGetUserGroups(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			if got := r.PathValue("id"); got != "u-1" {
				t.Errorf("expected user id u-1, got %q", got)
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "g-1", "name": "engineering", "path": "/engineering"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)

	groups, err := c.GetUserGroups(context.Background(), "test", "u-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 1 || groups[0]["path"] != "/engineering" {
		t.Errorf("unexpected groups: %v", groups)
	}
}

func TestGetUserGroups_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/users/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if _, err := c.GetUserGroups(context.Background(), "test", "u-1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAddUserToGroup(t *testing.T) {
	var mu sync.Mutex
	var seen string

	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/users/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = r.PathValue("id") + "/" + r.PathValue("gid")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.AddUserToGroup(context.Background(), "test", "u-1", "g-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen != "u-1/g-1" {
		t.Errorf("unexpected path values: %q", seen)
	}
}

func TestAddUserToGroup_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/users/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("nope"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.AddUserToGroup(context.Background(), "test", "u-1", "g-1"); err == nil {
		t.Fatal("expected error")
	}
}

// --- Group tests ---

func TestGetGroups(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("search") != "engineering" {
				t.Errorf("expected search query param engineering, got %s", r.URL.Query().Get("search"))
			}
			if r.URL.Query().Get("exact") != "true" {
				t.Errorf("expected exact=true, got %s", r.URL.Query().Get("exact"))
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "g-1", "name": "engineering"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetGroups(context.Background(), "test", "", "engineering")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 group, got %d", len(result))
	}
}

func TestGetGroups_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetGroups(context.Background(), "test", "", "eng")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetGroup(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"id":         "g-1",
				"name":       "engineering",
				"attributes": map[string]any{"department": []string{"eng"}},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetGroup(context.Background(), "test", "g-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result["name"] != "engineering" {
		t.Errorf("unexpected group: %v", result)
	}
}

func TestGetGroup_NotFound(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetGroup(context.Background(), "test", "g-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for not found, got %v", result)
	}
}

func TestGetGroup_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetGroup(context.Background(), "test", "g-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetSubGroups(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") != "parent-uuid" {
				t.Errorf("expected parent-uuid, got %s", r.PathValue("id"))
			}
			if r.URL.Query().Get("search") != "backend" {
				t.Errorf("expected search=backend, got %s", r.URL.Query().Get("search"))
			}
			if r.URL.Query().Get("exact") != "true" {
				t.Errorf("expected exact=true, got %s", r.URL.Query().Get("exact"))
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "child-1", "name": "backend"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetGroups(context.Background(), "test", "parent-uuid", "backend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 subgroup, got %d", len(result))
	}
}

func TestGetSubGroups_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.GetGroups(context.Background(), "test", "parent-uuid", "backend")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateGroup_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://localhost/admin/realms/test/groups/group-uuid")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	uuid, err := c.CreateGroup(context.Background(), "test", "", map[string]any{"name": "eng"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uuid != "group-uuid" {
		t.Errorf("expected group-uuid, got %s", uuid)
	}
}

func TestCreateGroup_NoLocationHeader(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateGroup(context.Background(), "test", "", map[string]any{"name": "eng"})
	if err == nil {
		t.Fatal("expected error for missing Location header")
	}
}

func TestCreateGroup_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("conflict"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateGroup(context.Background(), "test", "", map[string]any{"name": "eng"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateSubGroup_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") != "parent-uuid" {
				t.Errorf("expected parent-uuid, got %s", r.PathValue("id"))
			}
			w.Header().Set("Location", "http://localhost/admin/realms/test/groups/child-uuid")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	uuid, err := c.CreateGroup(context.Background(), "test", "parent-uuid", map[string]any{"name": "backend"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uuid != "child-uuid" {
		t.Errorf("expected child-uuid, got %s", uuid)
	}
}

func TestCreateSubGroup_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups/{id}/children": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("conflict"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	_, err := c.CreateGroup(context.Background(), "test", "parent-uuid", map[string]any{"name": "backend"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateGroup_Success(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.UpdateGroup(context.Background(), "test", "g-1", map[string]any{"name": "eng"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateGroup_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/groups/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.UpdateGroup(context.Background(), "test", "g-1", map[string]any{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetGroupRealmRoleMappings(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "r-1", "name": "developer"}})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetRealmRoleMappings(context.Background(), "test", RoleSubjectGroups, "g-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(result))
	}
}

func TestAddGroupRealmRoleMappings_Success(t *testing.T) {
	var received []map[string]any
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	roles := []map[string]any{{"id": "r-1", "name": "developer"}}
	if err := c.AddRealmRoleMappings(context.Background(), "test", RoleSubjectGroups, "g-1", roles); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 || received[0]["name"] != "developer" {
		t.Errorf("unexpected received roles: %v", received)
	}
}

func TestAddGroupRealmRoleMappings_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/realm": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.AddRealmRoleMappings(context.Background(), "test", RoleSubjectGroups, "g-1", []map[string]any{{"id": "r-1"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetGroupClientRoleMappings(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("clientUuid") != "c-uuid" {
				t.Errorf("expected c-uuid, got %s", r.PathValue("clientUuid"))
			}
			json.NewEncoder(w).Encode([]map[string]any{{"id": "cr-1", "name": "admin"}})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetClientRoleMappings(context.Background(), "test", RoleSubjectGroups, "g-1", "c-uuid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(result))
	}
}

func TestAddGroupClientRoleMappings_Success(t *testing.T) {
	var received []map[string]any
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	roles := []map[string]any{{"id": "cr-1", "name": "admin"}}
	if err := c.AddClientRoleMappings(context.Background(), "test", RoleSubjectGroups, "g-1", "c-uuid", roles); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 || received[0]["name"] != "admin" {
		t.Errorf("unexpected received roles: %v", received)
	}
}

func TestAddGroupClientRoleMappings_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/groups/{id}/role-mappings/clients/{clientUuid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	err := c.AddClientRoleMappings(context.Background(), "test", RoleSubjectGroups, "g-1", "c-uuid", []map[string]any{{"id": "cr-1"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Client Scope tests ---

func TestGetClientScopes(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "cs-1", "name": "orders:read"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	result, err := c.GetClientScopes(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0]["name"] != "orders:read" {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestGetClientScopes_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if _, err := c.GetClientScopes(context.Background(), "test"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateClientScope(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/client-scopes": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://kc/admin/realms/test/client-scopes/cs-9")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	id, err := c.CreateClientScope(context.Background(), "test", map[string]any{"name": "orders:read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "cs-9" {
		t.Errorf("expected cs-9, got %q", id)
	}
}

func TestUpdateClientScope(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/client-scopes/{id}": func(w http.ResponseWriter, r *http.Request) {
			if got := r.PathValue("id"); got != "cs-1" {
				t.Errorf("expected scope id cs-1, got %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.UpdateClientScope(context.Background(), "test", "cs-1", map[string]any{"name": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientScopeProtocolMappers(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "m-1", "name": "audience"}})
		},
		"POST /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/client-scopes/{id}/protocol-mappers/models/{mapperId}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	mappers, err := c.GetProtocolMappers(ctx, "test", MapperContainerClientScopes, "cs-1")
	if err != nil || len(mappers) != 1 {
		t.Fatalf("get: %v %v", mappers, err)
	}
	if err := c.CreateProtocolMapper(ctx, "test", MapperContainerClientScopes, "cs-1", map[string]any{"name": "audience"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.UpdateProtocolMapper(ctx, "test", MapperContainerClientScopes, "cs-1", "m-1", map[string]any{"name": "audience"}); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// --- Client Scope Assignment tests ---

func TestClientScopeAssignmentEndpoints(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}

	record := func(key string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			seen[key] = r.PathValue("scopeId")
			w.WriteHeader(http.StatusNoContent)
		}
	}
	emptyList := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{})
	}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/default-default-client-scopes":                   emptyList,
		"PUT /admin/realms/{realm}/default-default-client-scopes/{scopeId}":         record("realmDefault"),
		"GET /admin/realms/{realm}/default-optional-client-scopes":                  emptyList,
		"PUT /admin/realms/{realm}/default-optional-client-scopes/{scopeId}":        record("realmOptional"),
		"GET /admin/realms/{realm}/clients/{uuid}/default-client-scopes":            emptyList,
		"PUT /admin/realms/{realm}/clients/{uuid}/default-client-scopes/{scopeId}":  record("clientDefault"),
		"GET /admin/realms/{realm}/clients/{uuid}/optional-client-scopes":           emptyList,
		"PUT /admin/realms/{realm}/clients/{uuid}/optional-client-scopes/{scopeId}": record("clientOptional"),
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	if _, err := c.GetRealmClientScopes(ctx, "test", ClientScopeDefault); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRealmClientScope(ctx, "test", "cs-1", ClientScopeDefault); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetRealmClientScopes(ctx, "test", ClientScopeOptional); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRealmClientScope(ctx, "test", "cs-2", ClientScopeOptional); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetClientScopeAssignments(ctx, "test", "uuid-1", ClientScopeDefault); err != nil {
		t.Fatal(err)
	}
	if err := c.AddClientScopeAssignment(ctx, "test", "uuid-1", "cs-3", ClientScopeDefault); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetClientScopeAssignments(ctx, "test", "uuid-1", ClientScopeOptional); err != nil {
		t.Fatal(err)
	}
	if err := c.AddClientScopeAssignment(ctx, "test", "uuid-1", "cs-4", ClientScopeOptional); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := map[string]string{
		"realmDefault":   "cs-1",
		"realmOptional":  "cs-2",
		"clientDefault":  "cs-3",
		"clientOptional": "cs-4",
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("%s: expected %q, got %q", k, v, seen[k])
		}
	}
}

// --- Organization tests ---

func TestOrganizationEndpoints(t *testing.T) {
	var mu sync.Mutex
	var memberBody string
	var searchQuery string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			searchQuery = r.URL.Query().Get("search")
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{{"id": "org-1", "name": "acme"}})
		},
		"POST /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-9")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/organizations/{id}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"POST /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading member request body: %v", err)
			}
			mu.Lock()
			memberBody = string(body)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	orgs, err := c.GetOrganizations(ctx, "test", "acme")
	if err != nil || len(orgs) != 1 {
		t.Fatalf("get organizations: %v %v", orgs, err)
	}
	id, err := c.CreateOrganization(ctx, "test", map[string]any{"name": "acme"})
	if err != nil || id != "org-9" {
		t.Fatalf("create organization: %q %v", id, err)
	}
	if err := c.UpdateOrganization(ctx, "test", "org-1", map[string]any{"name": "acme"}); err != nil {
		t.Fatalf("update organization: %v", err)
	}
	members, err := c.GetOrganizationMembers(ctx, "test", "org-1")
	if err != nil || len(members) != 1 {
		t.Fatalf("get members: %v %v", members, err)
	}
	if err := c.AddOrganizationMember(ctx, "test", "org-1", "u-2"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if searchQuery != "acme" {
		t.Errorf("expected search=acme, got %q", searchQuery)
	}
	// This endpoint takes the user id as a bare JSON string, not an object.
	if memberBody != `"u-2"` {
		t.Errorf("expected quoted user id, got %s", memberBody)
	}
}

func TestGetOrganizations_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if _, err := c.GetOrganizations(context.Background(), "test", "acme"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAddOrganizationMember_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"POST /admin/realms/{realm}/organizations/{id}/members": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte("already a member"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.AddOrganizationMember(context.Background(), "test", "org-1", "u-1"); err == nil {
		t.Fatal("expected error")
	}
}

// --- Organization Group tests ---

func TestOrganizationGroupEndpoints(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-1", "name": "engineering"}})
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "g-2", "name": "backend"}})
		},
		"POST /admin/realms/{realm}/organizations/{id}/groups": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1/groups/g-9")
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/organizations/{id}/groups/{gid}/children": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://kc/admin/realms/test/organizations/org-1/groups/g-10")
			w.WriteHeader(http.StatusCreated)
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen["update"] = r.PathValue("gid")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
		"GET /admin/realms/{realm}/organizations/{id}/groups/{gid}/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "u-1", "username": "alice"}})
		},
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen["addMember"] = r.PathValue("gid") + "/" + r.PathValue("uid")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	groups, err := c.GetOrganizationGroups(ctx, "test", "org-1", "")
	if err != nil || len(groups) != 1 {
		t.Fatalf("get groups: %v %v", groups, err)
	}
	children, err := c.GetOrganizationGroups(ctx, "test", "org-1", "g-1")
	if err != nil || len(children) != 1 || children[0]["name"] != "backend" {
		t.Fatalf("get children: %v %v", children, err)
	}
	id, err := c.CreateOrganizationGroup(ctx, "test", "org-1", "", map[string]any{"name": "sales"})
	if err != nil || id != "g-9" {
		t.Fatalf("create group: %q %v", id, err)
	}
	subID, err := c.CreateOrganizationGroup(ctx, "test", "org-1", "g-1", map[string]any{"name": "backend"})
	if err != nil || subID != "g-10" {
		t.Fatalf("create subgroup: %q %v", subID, err)
	}
	if err := c.UpdateOrganizationGroup(ctx, "test", "org-1", "g-1", map[string]any{"name": "engineering"}); err != nil {
		t.Fatalf("update group: %v", err)
	}
	members, err := c.GetOrganizationGroupMembers(ctx, "test", "org-1", "g-1")
	if err != nil || len(members) != 1 {
		t.Fatalf("get group members: %v %v", members, err)
	}
	if err := c.AddOrganizationGroupMember(ctx, "test", "org-1", "g-1", "u-2"); err != nil {
		t.Fatalf("add group member: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen["update"] != "g-1" {
		t.Errorf("unexpected update target: %q", seen["update"])
	}
	if seen["addMember"] != "g-1/u-2" {
		t.Errorf("unexpected member add: %q", seen["addMember"])
	}
}

func TestAddOrganizationGroupMember_NonOrgMemberError(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"PUT /admin/realms/{realm}/organizations/{id}/groups/{gid}/members/{uid}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errorMessage":"User is not member of the organization"}`))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if err := c.AddOrganizationGroupMember(context.Background(), "test", "org-1", "g-1", "u-1"); err == nil {
		t.Fatal("expected error for a user that is not an organization member")
	}
}

// --- Authentication Flow tests ---

func TestAuthenticationFlowEndpoints(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]string{}

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "f-1", "alias": "browser", "builtIn": true}})
		},
		"POST /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/copy": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			defer mu.Unlock()
			calls["copy"] = r.PathValue("alias") + "->" + body["newName"].(string)
			w.WriteHeader(http.StatusCreated)
		},
		"GET /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{"id": "ex-1", "providerId": "auth-cookie", "level": 0, "index": 0}})
		},
		"PUT /admin/realms/{realm}/authentication/flows/{alias}/executions": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/execution": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/flows/{alias}/executions/flow": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
		"POST /admin/realms/{realm}/authentication/executions/{id}/config": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			calls["config"] = r.PathValue("id")
			w.WriteHeader(http.StatusCreated)
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	ctx := context.Background()

	flows, err := c.GetAuthenticationFlows(ctx, "test")
	if err != nil || len(flows) != 1 {
		t.Fatalf("get flows: %v %v", flows, err)
	}
	if err := c.CreateAuthenticationFlow(ctx, "test", map[string]any{"alias": "f"}); err != nil {
		t.Fatalf("create flow: %v", err)
	}
	if err := c.CopyAuthenticationFlow(ctx, "test", "browser", "browser-step-up"); err != nil {
		t.Fatalf("copy flow: %v", err)
	}
	execs, err := c.GetAuthenticationFlowExecutions(ctx, "test", "f")
	if err != nil || len(execs) != 1 {
		t.Fatalf("get executions: %v %v", execs, err)
	}
	if err := c.UpdateAuthenticationFlowExecution(ctx, "test", "f", map[string]any{"id": "ex-1"}); err != nil {
		t.Fatalf("update execution: %v", err)
	}
	if err := c.CreateAuthenticationExecution(ctx, "test", "f", map[string]any{"provider": "auth-cookie"}); err != nil {
		t.Fatalf("create execution: %v", err)
	}
	if err := c.CreateAuthenticationSubflow(ctx, "test", "f", map[string]any{"alias": "sub"}); err != nil {
		t.Fatalf("create subflow: %v", err)
	}
	if err := c.CreateAuthenticationExecutionConfig(ctx, "test", "ex-1", map[string]any{"alias": "cfg"}); err != nil {
		t.Fatalf("create execution config: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls["copy"] != "browser->browser-step-up" {
		t.Errorf("unexpected copy call: %q", calls["copy"])
	}
	if calls["config"] != "ex-1" {
		t.Errorf("unexpected config call: %q", calls["config"])
	}
}

func TestGetAuthenticationFlows_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/flows": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)
	if _, err := c.GetAuthenticationFlows(context.Background(), "test"); err == nil {
		t.Fatal("expected error")
	}
}

// --- Server capability tests ---

func TestGetServerInfo(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/serverinfo": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"systemInfo": map[string]any{"version": "26.6.4"},
				"features":   []any{map[string]any{"name": "ORGANIZATION", "enabled": true}},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)

	info, err := c.GetServerInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	system, ok := info["systemInfo"].(map[string]any)
	if !ok || system["version"] != "26.6.4" {
		t.Errorf("unexpected server info: %v", info)
	}
}

func TestGetServerInfo_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/serverinfo": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"HTTP 403 Forbidden"}`))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)

	_, err := c.GetServerInfo(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	// The compatibility check tells a refusal apart from a failure by status,
	// so the typed error has to survive.
	var status *StatusError
	if !errors.As(err, &status) {
		t.Fatalf("expected a *StatusError, got %T", err)
	}
	if status.StatusCode() != http.StatusForbidden {
		t.Errorf("expected 403, got %d", status.StatusCode())
	}
}

func TestGetAuthenticationProviders(t *testing.T) {
	var mu sync.Mutex
	var seenKind string

	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/{kind}": func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seenKind = r.PathValue("kind")
			mu.Unlock()
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "auth-cookie", "displayName": "Cookie"},
			})
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)

	providers, err := c.GetAuthenticationProviders(context.Background(), "master", "authenticator-providers")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(providers) != 1 || providers[0]["id"] != "auth-cookie" {
		t.Errorf("unexpected providers: %v", providers)
	}

	mu.Lock()
	defer mu.Unlock()
	if seenKind != "authenticator-providers" {
		t.Errorf("kind should reach the path, got %q", seenKind)
	}
}

func TestGetAuthenticationProviders_Error(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/authentication/{kind}": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"HTTP 403 Forbidden"}`))
		},
	})
	defer server.Close()

	c := connectClient(t, server.URL)

	err := func() error {
		_, err := c.GetAuthenticationProviders(context.Background(), "master", "form-providers")
		return err
	}()
	if err == nil {
		t.Fatal("expected error")
	}

	var status *StatusError
	if !errors.As(err, &status) || status.StatusCode() != http.StatusForbidden {
		t.Errorf("expected a 403 *StatusError, got %v", err)
	}
}
