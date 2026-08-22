package compat

import (
	"fmt"
	"strings"
)

// Doc markers delimit the generated compatibility table in README.md.
const (
	DocsBeginMarker = "<!-- BEGIN COMPATIBILITY TABLE -->"
	DocsEndMarker   = "<!-- END COMPATIBILITY TABLE -->"
)

// DocsTable renders Requirements as the markdown table published in the README.
// It is generated rather than hand-written so the docs cannot drift from the
// behaviour; TestCompatibilityTableMatchesDocs enforces that.
func DocsTable() string {
	var b strings.Builder

	b.WriteString("| Config | Minimum Keycloak | Server feature |\n")
	b.WriteString("|---|---|---|\n")

	for _, r := range Requirements {
		feature := "—"
		if r.Feature != "" {
			feature = "`" + r.Feature + "`"
		}

		minVersion := r.MinVersion
		if minVersion == "" {
			minVersion = "any"
		}

		fmt.Fprintf(&b, "| %s | %s | %s |\n", r.Name, minVersion, feature)
	}

	return b.String()
}

// ReplaceDocsTable returns doc with the content between the markers replaced by
// the generated table. It reports false when the markers are missing or out of
// order, so a caller can fail loudly rather than silently skip the update.
func ReplaceDocsTable(doc string) (string, bool) {
	start := strings.Index(doc, DocsBeginMarker)
	end := strings.Index(doc, DocsEndMarker)

	if start < 0 || end < 0 || end < start {
		return doc, false
	}

	head := doc[:start+len(DocsBeginMarker)]
	tail := doc[end:]

	return head + "\n\n" + DocsTable() + "\n" + tail, true
}
