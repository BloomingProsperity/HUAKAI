package bodyparamgate

import (
	"encoding/json"
	"errors"
)

const nestedIncludeObfuscationKey = "stream_options.include_obfuscation"

// StripBodyParams removes opt-in channel-gated request fields from a JSON
// object. An empty strip list is a strict no-op and returns body unchanged.
//
// Caution: stripping "store" is useful for privacy/compliance controls, but it
// can change Codex-style conversation persistence semantics for callers that
// rely on upstream storage.
func StripBodyParams(body []byte, stripKeys []string) ([]byte, error) {
	if len(stripKeys) == 0 {
		return body, nil
	}
	root, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	for _, key := range stripKeys {
		switch key {
		case "":
			continue
		case nestedIncludeObfuscationKey:
			stripNestedRaw(root, "stream_options", "include_obfuscation")
		default:
			delete(root, key)
		}
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyParamOverride sets top-level request fields from an opt-in channel
// override map. An empty override map is a strict no-op and returns body
// unchanged.
func ApplyParamOverride(body []byte, override map[string]json.RawMessage) ([]byte, error) {
	if len(override) == 0 {
		return body, nil
	}
	root, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	for key, value := range override {
		if key == "" {
			continue
		}
		root[key] = value
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func decodeObject(body []byte) (map[string]json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("bodyparamgate: request body must be a JSON object")
	}
	return root, nil
}

func stripNestedRaw(root map[string]json.RawMessage, parentKey, childKey string) {
	raw, ok := root[parentKey]
	if !ok {
		return
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
		return
	}
	delete(nested, childKey)
	next, err := json.Marshal(nested)
	if err != nil {
		return
	}
	root[parentKey] = next
}
