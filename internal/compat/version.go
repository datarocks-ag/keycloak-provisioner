package compat

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a Keycloak version reduced to the parts that ordering depends on.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion reads a Keycloak version string such as "26.6.4".
//
// Trailing qualifiers are ignored, so "26.7.0-rc1" and "999.0.0-SNAPSHOT" both
// parse — the latter is how nightly builds identify themselves, and comparing
// it as 999.0.0 correctly treats it as newer than any release. A missing patch
// or minor component is read as zero, so "26" and "26.6" are both valid.
func ParseVersion(s string) (Version, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return Version{}, fmt.Errorf("empty version string")
	}

	// Drop any qualifier: "26.7.0-rc1" -> "26.7.0".
	if i := strings.IndexAny(trimmed, "-+"); i >= 0 {
		trimmed = trimmed[:i]
	}

	parts := strings.Split(trimmed, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}

	var v Version

	targets := []*int{&v.Major, &v.Minor, &v.Patch}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("parsing version %q: component %q is not a number", s, part)
		}
		if n < 0 {
			return Version{}, fmt.Errorf("parsing version %q: component %q is negative", s, part)
		}
		*targets[i] = n
	}

	return v, nil
}

// Compare returns -1 when v is older than other, 1 when it is newer, and 0 when
// they are equal.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}

	return 0
}

// AtLeast reports whether v is other or newer.
func (v Version) AtLeast(other Version) bool {
	return v.Compare(other) >= 0
}

// String renders the version as major.minor.patch.
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
