package bodyparamgate

import (
	"encoding/json"
	"errors"
)

const nestedIncludeObfuscationKey = "stream_options.include_obfuscation"

// StripBodyParams 从一个 JSON 对象中移除按渠道开关启用(opt-in)的请求
// 字段。空的 strip 列表是严格的空操作,会原样返回 body。
//
// 注意:剥除 "store" 对隐私/合规管控很有用,但对于依赖上游存储的调用方,
// 它可能改变 Codex 风格的会话持久化语义。
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

// ApplyParamOverride 根据按渠道开关启用(opt-in)的 override map 设置顶层
// 请求字段。空的 override map 是严格的空操作,会原样返回 body。
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
