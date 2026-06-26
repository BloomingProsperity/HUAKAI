package paramgate

import (
	"encoding/json"
	"errors"
)

// GateConfig 控制对按渠道开关启用(opt-in)的请求参数的剥除。零值是
// 严格的空操作:StripGatedFields 返回原始 body 字节的拷贝,既不解析也不
// 重新序列化。
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

// StripGatedFields 只移除其对应 config 标志为 true 的字段。当所有标志均为
// false 时,它逐字节保留 HUAKAI 现有的透传行为,只是返回一个防御性拷贝。
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
