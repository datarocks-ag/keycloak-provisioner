package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"keycloak-provisioner/internal/client"
)

// emptyList answers with an empty JSON array, for a read a test has to serve
// but does not care about.
func emptyList(w http.ResponseWriter, _ *http.Request) {
	json.NewEncoder(w).Encode([]map[string]any{})
}

// testServer creates an httptest.Server with a token endpoint and custom handlers.
func testServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Token endpoint always succeeds
	mux.HandleFunc("POST /realms/master/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"expires_in":   300,
		})
	})

	for pattern, handler := range handlers {
		mux.HandleFunc(pattern, handler)
	}

	return httptest.NewServer(mux)
}

func newTestClient(t *testing.T, serverURL string) *client.Client {
	t.Helper()
	c := client.New(serverURL, "admin", "admin")
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return c
}
