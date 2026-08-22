package provisioner

import (
	"testing"
)

func TestBuildAcrLoaMapAttributeEmpty(t *testing.T) {
	if got := buildAcrLoaMapAttribute(nil); got != "" {
		t.Errorf("expected empty string for nil map, got %q", got)
	}
	if got := buildAcrLoaMapAttribute(map[string]int{}); got != "" {
		t.Errorf("expected empty string for empty map, got %q", got)
	}
}

func TestMergeAttributesNilExisting(t *testing.T) {
	merged := mergeAttributes(nil, map[string]string{"a": "1"})

	if len(merged) != 1 || merged["a"] != "1" {
		t.Errorf("unexpected merge result: %v", merged)
	}
}

func TestMergeAttributesNonStringValues(t *testing.T) {
	existing := map[string]any{
		"attributes": map[string]any{
			"num":  float64(3),
			"null": nil,
		},
	}

	merged := mergeAttributes(existing, nil)

	if got := merged["num"]; got != "3" {
		t.Errorf("expected stringified number, got %q", got)
	}
	if _, ok := merged["null"]; ok {
		t.Error("nil attribute values should be skipped")
	}
}
