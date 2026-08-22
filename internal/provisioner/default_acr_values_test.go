package provisioner

import (
	"testing"

	"keycloak-provisioner/internal/config"
)

// TestBuildDefaultAcrValuesAttribute pins the encoding, which is the whole
// reason the field is typed: Keycloak wants a "##"-separated string, not the
// JSON array the field's shape suggests, and rejects a JSON array with a
// message about the ACR map rather than about encoding.
func TestBuildDefaultAcrValuesAttribute(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"nil is unmanaged", nil, ""},
		{"empty is unmanaged", []string{}, ""},
		{"single", []string{"gold"}, "gold"},
		{"several are ##-joined", []string{"gold", "silver"}, "gold##silver"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildDefaultAcrValuesAttribute(tc.values); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildClientAttributesDefaultAcrValues(t *testing.T) {
	attrs := buildClientAttributes(config.Client{
		ClientID:         "app",
		DefaultAcrValues: []string{"gold", "silver"},
	}, nil)

	if attrs[defaultAcrValuesAttr] != "gold##silver" {
		t.Errorf("unexpected attribute: %q", attrs[defaultAcrValuesAttr])
	}
}

// TestBuildClientAttributesDefaultAcrValuesWinsOverRawAttribute mirrors how
// acrLoaMap behaves: the typed field is the one that takes effect.
func TestBuildClientAttributesDefaultAcrValuesWinsOverRawAttribute(t *testing.T) {
	attrs := buildClientAttributes(config.Client{
		ClientID:         "app",
		Attributes:       map[string]string{defaultAcrValuesAttr: "bronze"},
		DefaultAcrValues: []string{"gold"},
	}, nil)

	if attrs[defaultAcrValuesAttr] != "gold" {
		t.Errorf("the typed field must win, got %q", attrs[defaultAcrValuesAttr])
	}
}

// TestBuildClientAttributesLeavesDefaultAcrValuesUnmanaged is the control: a
// client that declares nothing must not send the attribute at all, so a value
// set out of band survives.
func TestBuildClientAttributesLeavesDefaultAcrValuesUnmanaged(t *testing.T) {
	existing := map[string]any{
		"attributes": map[string]any{defaultAcrValuesAttr: "gold"},
	}

	attrs := buildClientAttributes(config.Client{ClientID: "app"}, existing)
	if attrs != nil {
		t.Errorf("a client declaring nothing must send no attributes, got %v", attrs)
	}
}
