package compat

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// readmePath is relative to this package's directory.
const readmePath = "../../README.md"

var update = flag.Bool("update", false, "rewrite the README compatibility table from the requirement registry")

// TestCompatibilityTableMatchesDocs keeps the published table and the registry
// in step. Documentation drifting behind the code is how the wrong minimum
// version gets published, so this fails rather than trusting a human to
// remember. Run `go test ./internal/compat -update` to regenerate.
func TestCompatibilityTableMatchesDocs(t *testing.T) {
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading README: %v", err)
	}

	doc := string(raw)

	updated, ok := ReplaceDocsTable(doc)
	if !ok {
		t.Fatalf("README is missing the %s / %s markers", DocsBeginMarker, DocsEndMarker)
	}

	if updated == doc {
		return
	}

	if *update {
		if err := os.WriteFile(readmePath, []byte(updated), 0o644); err != nil {
			t.Fatalf("writing README: %v", err)
		}
		t.Log("README compatibility table regenerated")

		return
	}

	t.Errorf("README compatibility table is out of date; run `go test ./internal/compat -update`.\n\nExpected table:\n%s", DocsTable())
}

func TestDocsTableRendersEveryRequirement(t *testing.T) {
	table := DocsTable()

	for _, r := range Requirements {
		if !strings.Contains(table, r.Name) {
			t.Errorf("table is missing %q", r.Name)
		}
		if !strings.Contains(table, r.MinVersion) {
			t.Errorf("table is missing the minimum version for %q", r.Name)
		}
	}
}

func TestReplaceDocsTableReportsMissingMarkers(t *testing.T) {
	if _, ok := ReplaceDocsTable("no markers here"); ok {
		t.Error("expected missing markers to be reported")
	}
	if _, ok := ReplaceDocsTable(DocsEndMarker + "\n" + DocsBeginMarker); ok {
		t.Error("expected out-of-order markers to be reported")
	}
}
