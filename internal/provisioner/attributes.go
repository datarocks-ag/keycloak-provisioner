package provisioner

import (
	"encoding/json"
	"fmt"
)

// acrLoaMapAttr is the Keycloak attribute holding the ACR-to-Level-of-
// Authentication mapping used for step-up authentication. It is valid on both
// realms and clients, and its value is a JSON object.
const acrLoaMapAttr = "acr.loa.map"

// mergeAttributes merges configured attributes over the "attributes" map of an
// existing Keycloak representation, returning a fresh map. existing may be nil,
// in which case only the configured attributes are returned.
//
// Keycloak replaces the whole attribute map on update, so callers must send the
// union rather than only the keys they manage. Configured values win over
// current ones; keys absent from the config are preserved, never removed.
func mergeAttributes(existing map[string]any, configured map[string]string) map[string]string {
	merged := make(map[string]string, len(configured))

	if current, ok := existing["attributes"].(map[string]any); ok {
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
// Keycloak stores in the acr.loa.map attribute. It returns "" when the map is
// empty, so callers can tell "not configured" from "configured as empty".
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
