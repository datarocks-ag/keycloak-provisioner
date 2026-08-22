package provisioner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// acrLoaMapAttr is the Keycloak attribute holding the ACR-to-Level-of-
// Authentication mapping used for step-up authentication. It is valid on both
// realms and clients, and its value is a JSON object.
const acrLoaMapAttr = "acr.loa.map"

// defaultAcrValuesAttr is the client attribute holding the ACR values Keycloak
// applies when a request asks for none. Its value is a "##"-separated string,
// not the JSON array the field's shape suggests.
const defaultAcrValuesAttr = "default.acr.values"

// acrValueSeparator is how Keycloak packs several ACR values into that one
// attribute.
const acrValueSeparator = "##"

// mergeAttributes merges configured attributes over the "attributes" map of an
// existing Keycloak representation, returning a fresh map. existing may be nil,
// in which case only the configured attributes are returned.
//
// Keycloak replaces the whole attribute map on update, so callers must send the
// union rather than only the keys they manage. Configured values win over
// current ones; keys absent from the config are preserved, never removed.
func mergeAttributes(existing map[string]any, configured map[string]string) map[string]string {
	return mergeStringMapField(existing, "attributes", configured)
}

// mergeStringMapField merges configured values over a named string-map field of
// an existing Keycloak representation, returning a fresh map.
//
// Several Keycloak resources replace a whole map on update rather than patching
// it — a realm's and a client's "attributes", an identity provider's "config" —
// so the field name is a parameter rather than each caller repeating the
// coercion below. An identity provider in particular returns its client secret
// masked as "**********", which Keycloak reads back as "keep the stored value";
// carrying the current map forward is what makes that work, and what stops an
// unmanaged key from being deleted.
func mergeStringMapField(existing map[string]any, field string, configured map[string]string) map[string]string {
	merged := make(map[string]string, len(configured))

	if current, ok := existing[field].(map[string]any); ok {
		for k, v := range current {
			if v == nil {
				continue
			}
			if s, ok := v.(string); ok {
				merged[k] = s
				continue
			}
			// Keycloak returns attribute values as JSON strings, but tolerate
			// anything else rather than dropping the key.
			merged[k] = fmt.Sprint(v)
		}
	}

	for k, v := range configured {
		merged[k] = v
	}

	return merged
}

// buildAcrLoaMapAttribute marshals an ACR-to-LoA map into the JSON string
// Keycloak stores in the acr.loa.map attribute.
//
// It returns "" for both a nil and an empty map: an `acrLoaMap:` block with no
// entries is treated the same as omitting it, and the attribute is left
// unmanaged rather than written as an empty object. Callers use the empty
// string as "nothing to write" and cannot distinguish the two cases.
//
// encoding/json sorts map keys, so the output is stable across runs.
func buildAcrLoaMapAttribute(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}

	encoded, err := json.Marshal(m)
	if err != nil {
		// Unreachable for map[string]int, but never emit a half-built value.
		return ""
	}

	return string(encoded)
}

// mergeMultiValueField merges configured values over a named multivalued field
// of an existing Keycloak representation, returning a fresh map.
//
// The single-valued mergeStringMapField cannot be reused: an organization's
// attributes are lists, and Keycloak returns them as []any of strings. Values
// are replaced per key rather than concatenated — a configured key states what
// that attribute is, and appending would make repeated runs grow it.
func mergeMultiValueField(existing map[string]any, field string, configured map[string][]string) map[string][]string {
	merged := make(map[string][]string, len(configured))

	if current, ok := existing[field].(map[string]any); ok {
		for k, v := range current {
			values, ok := v.([]any)
			if !ok {
				continue
			}

			out := make([]string, 0, len(values))

			for _, item := range values {
				if s, ok := item.(string); ok {
					out = append(out, s)
					continue
				}
				// Keycloak returns these as JSON strings, but tolerate anything
				// else rather than dropping the key.
				out = append(out, fmt.Sprint(item))
			}

			merged[k] = out
		}
	}

	for k, v := range configured {
		merged[k] = v
	}

	return merged
}

// buildDefaultAcrValuesAttribute joins ACR values the way Keycloak stores them.
//
// It returns "" for both a nil and an empty list, so an empty block is treated
// as omitting it and the attribute is left unmanaged rather than written blank.
func buildDefaultAcrValuesAttribute(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return strings.Join(values, acrValueSeparator)
}
