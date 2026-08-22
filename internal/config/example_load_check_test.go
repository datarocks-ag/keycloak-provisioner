package config

import "testing"

// TestShippedExampleConfigLoads guards the example against schema drift: it is
// the file users copy, so a field renamed in the schema must not leave it
// unloadable.
func TestShippedExampleConfigLoads(t *testing.T) {
	if _, err := Load("../../config.example.yaml"); err != nil {
		t.Fatalf("config.example.yaml does not load: %v", err)
	}
}
