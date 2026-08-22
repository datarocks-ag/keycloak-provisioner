package provisioner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestClientLookupErrorNamesRealmClientAndSubject guards the context in the
// error, which is easy to drop when two reconcilers are merged into one: the
// group path resolved clients through a helper that named only the client, so
// adopting it wholesale would have cost the user path the realm it used to
// report. The message now names more than either original did.
func TestClientLookupErrorNamesRealmClientAndSubject(t *testing.T) {
	server := testServer(t, map[string]http.HandlerFunc{
		"GET /admin/realms/{realm}/clients": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]map[string]any{})
		},
	})
	defer server.Close()

	subjects := map[string]roleSubject{
		"user":  userRoleSubject("u-1", "alice"),
		"group": groupRoleSubject("g-1", "engineering"),
	}

	for label, subject := range subjects {
		t.Run(label, func(t *testing.T) {
			p := New(newTestClient(t, server.URL), nil)

			err := p.ensureClientRoleMappings(context.Background(), "test-realm",
				subject, map[string][]string{"missing-app": {"admin"}})
			if err == nil {
				t.Fatal("expected an error for a client that does not exist")
			}

			for _, want := range []string{"missing-app", "test-realm", subject.name, subject.kind} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q, got: %v", want, err)
				}
			}
		})
	}
}
