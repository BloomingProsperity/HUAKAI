// Package thinkingnorm normalizes an Anthropic extended-thinking request so it
// satisfies Anthropic's hard validity constraints before forwarding, mirroring
// CLIProxyAPI: when thinking is enabled Anthropic REQUIRES temperature == 1 and
// tool_choice of type auto/none -- any other value is a 400. Without this a
// client's thinking request simply fails. This is a correctness normalization,
// not a semantic change: the request could not have succeeded otherwise.
package thinkingnorm

import "encoding/json"

// NormalizeThinkingValidity returns body with Anthropic thinking constraints
// enforced. Bodies with no active thinking field, already-valid bodies, or
// bodies that fail to parse are returned UNCHANGED (byte-identical, fail-safe) —
// only a thinking request carrying an invalid temperature/tool_choice is
// re-encoded.
func NormalizeThinkingValidity(body []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	thinkingRaw, ok := m["thinking"]
	if !ok {
		return body
	}
	var thinking map[string]json.RawMessage
	if err := json.Unmarshal(thinkingRaw, &thinking); err != nil {
		return body
	}
	var ttype string
	_ = json.Unmarshal(thinking["type"], &ttype)
	if ttype != "enabled" && ttype != "adaptive" && ttype != "auto" {
		return body // thinking not active -> no constraint
	}

	changed := false

	// (1) temperature must be exactly 1 under extended thinking.
	if tempRaw, ok := m["temperature"]; ok {
		var temp float64
		if json.Unmarshal(tempRaw, &temp) == nil && temp != 1 {
			if v, err := json.Marshal(1); err == nil {
				m["temperature"] = v
				changed = true
			}
		}
	}

	// (2) tool_choice must be type auto or none under extended thinking.
	if tcRaw, ok := m["tool_choice"]; ok {
		var tc map[string]json.RawMessage
		if json.Unmarshal(tcRaw, &tc) == nil {
			var tctype string
			_ = json.Unmarshal(tc["type"], &tctype)
			if tctype != "" && tctype != "auto" && tctype != "none" {
				if v, err := json.Marshal(map[string]string{"type": "auto"}); err == nil {
					m["tool_choice"] = v
					changed = true
				}
			}
		}
	}

	if !changed {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
