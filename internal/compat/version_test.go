package compat

import "testing"

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want Version
	}{
		{"26.6.4", Version{26, 6, 4}},
		{"26.6", Version{26, 6, 0}},
		{"26", Version{26, 0, 0}},
		{" 26.7.2 ", Version{26, 7, 2}},
		// Nightly builds identify themselves this way; treating it as 999.0.0
		// makes it newer than any release, which is the intent.
		{"999.0.0-SNAPSHOT", Version{999, 0, 0}},
		{"26.7.0-rc1", Version{26, 7, 0}},
		{"26.6.4+build7", Version{26, 6, 4}},
		// Extra components are ignored rather than rejected.
		{"26.6.4.1", Version{26, 6, 4}},
	}

	for _, tt := range tests {
		got, err := ParseVersion(tt.in)
		if err != nil {
			t.Errorf("ParseVersion(%q): unexpected error %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseVersion(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseVersionErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "quarkus", "26.x.1", "-1.0.0"} {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) = %v, want an error", in, v)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"26.6.0", "26.6.0", 0},
		{"26.6.1", "26.6.0", 1},
		{"26.6.0", "26.6.1", -1},
		{"26.7.0", "26.6.9", 1},
		{"27.0.0", "26.9.9", 1},
		{"26.2.0", "26.6.0", -1},
		{"999.0.0", "26.6.0", 1},
	}

	for _, tt := range tests {
		a, err := ParseVersion(tt.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseVersion(tt.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.Compare(b); got != tt.want {
			t.Errorf("%s.Compare(%s) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	v := Version{26, 6, 4}

	for _, tt := range []struct {
		min  Version
		want bool
	}{
		{Version{26, 6, 4}, true},
		{Version{26, 6, 0}, true},
		{Version{26, 0, 0}, true},
		{Version{26, 7, 0}, false},
		{Version{27, 0, 0}, false},
	} {
		if got := v.AtLeast(tt.min); got != tt.want {
			t.Errorf("%v.AtLeast(%v) = %v, want %v", v, tt.min, got, tt.want)
		}
	}
}

func TestVersionString(t *testing.T) {
	if got := (Version{26, 6, 4}).String(); got != "26.6.4" {
		t.Errorf("got %q", got)
	}
}
