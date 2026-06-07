package paramgate

import (
	"encoding/json"
	"errors"
)

// GateConfig controls opt-in stripping of channel-gated request parameters.
// The zero value is a strict no-op: StripGatedFields returns the original body
// bytes copied, without parsing or re-serializing.
type GateConfig struct {
	StripServiceTier                     bool
	StripInferenceGeo                    bool
	StripSpeed                           bool
	StripSafetyIdentifier                bool
	StripStreamOptionsIncludeObfuscation bool
	StripStore                           bool
}

func (c GateConfig) Enabled() bool {
	return c.StripServiceTier ||
		c.StripInferenceGeo ||
		c.StripSpeed ||
		c.StripSafetyIdentifier ||
		c.StripStreamOptionsIncludeObfuscation ||
		c.StripStore
}

// StripGatedFields removes only fields whose corresponding config flag is
// true. With all flags false it preserves HUAKAI's existing passthrough
// behavior byte-for-byte except for returning a defensive copy.
func StripGatedFields(body []byte, cfg GateConfig) ([]byte, error) {
	if !cfg.Enabled() {
		return append([]byte(nil), body...), nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("paramgate: request body must be a JSON object")
	}
	if cfg.StripServiceTier {
		delete(root, "service_tier")
	}
	if cfg.StripInferenceGeo {
		delete(root, "inference_geo")
	}
	if cfg.StripSpeed {
		delete(root, "speed")
	}
	if cfg.StripSafetyIdentifier {
		delete(root, "safety_identifier")
	}
	if cfg.StripStore {
		delete(root, "store")
	}
	if cfg.StripStreamOptionsIncludeObfuscation {
		stripStreamOptionsField(root, "include_obfuscation")
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func stripStreamOptionsField(root map[string]json.RawMessage, field string) {
	raw, ok := root["stream_options"]
	if !ok {
		return
	}
	var streamOptions map[string]json.RawMessage
	if err := json.Unmarshal(raw, &streamOptions); err != nil || streamOptions == nil {
		return
	}
	delete(streamOptions, field)
	next, err := json.Marshal(streamOptions)
	if err != nil {
		return
	}
	root["stream_options"] = next
}
