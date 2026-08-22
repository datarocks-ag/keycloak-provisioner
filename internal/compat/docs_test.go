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
		row := requirementRow(t, table, r.Name)

		want := r.MinVersion
		if want == "" {
			// strings.Contains(table, "") is vacuously true, so a version-less
			// requirement has to be checked for the rendered placeholder
			// instead — otherwise this assertion tests nothing.
			want = "any"
		}

		if !strings.Contains(row, want) {
			t.Errorf("row for %q should show version %q, got: %s", r.Name, want, row)
		}

		if r.Feature != "" && !strings.Contains(row, r.Feature) {
			t.Errorf("row for %q should name feature %q, got: %s", r.Name, r.Feature, row)
		}
	}
}

// requirementRow returns the generated table row for a requirement, so an
// assertion cannot accidentally be satisfied by a different row.
func requirementRow(t *testing.T, table, name string) string {
	t.Helper()

	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "| "+name+" |") {
			return line
		}
	}

	t.Fatalf("table has no row for %q:\n%s", name, table)

	return ""
}

func TestReplaceDocsTableReportsMissingMarkers(t *testing.T) {
	if _, ok := ReplaceDocsTable("no markers here"); ok {
		t.Error("expected missing markers to be reported")
	}
	if _, ok := ReplaceDocsTable(DocsEndMarker + "\n" + DocsBeginMarker); ok {
		t.Error("expected out-of-order markers to be reported")
	}
}
