// Package channelrewriteconfig 校验并规范化渠道请求改写配置。
package channelrewriteconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Values 同时保存规范化值和 update 所需的字段出现性。
type Values struct {
	SetBodyParamStrips bool
	BodyParamStrips    []string
	SetParamOverride   bool
	ParamOverride      json.RawMessage
	SetSensitiveWords  bool
	SensitiveWords     []string
}

// ValidationError 标识不符合消费侧数据结构的字段。
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// Decode 校验三个字段，并保留省略与显式空值的差异。
func Decode(bodyParamStrips, paramOverride, sensitiveWords json.RawMessage) (Values, error) {
	strips, setStrips, err := decodeStringList(bodyParamStrips, "body_param_strips")
	if err != nil {
		return Values{}, err
	}
	override, setOverride, err := decodeObject(paramOverride)
	if err != nil {
		return Values{}, err
	}
	words, setWords, err := decodeStringList(sensitiveWords, "sensitive_words")
	if err != nil {
		return Values{}, err
	}
	return Values{
		SetBodyParamStrips: setStrips,
		BodyParamStrips:    strips,
		SetParamOverride:   setOverride,
		ParamOverride:      override,
		SetSensitiveWords:  setWords,
		SensitiveWords:     words,
	}, nil
}

func decodeStringList(raw json.RawMessage, field string) ([]string, bool, error) {
	if len(raw) == 0 {
		return []string{}, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, invalid(field, "%s must be an array of non-empty strings", field)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, true, invalid(field, "%s must be an array of non-empty strings", field)
	}
	if values == nil {
		return nil, true, invalid(field, "%s must be an array of non-empty strings", field)
	}
	out := make([]string, len(values))
	for i, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, true, invalid(field, "%s[%d] must be a non-empty string", field, i)
		}
		out[i] = value
	}
	return out, true, nil
}

func decodeObject(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, invalid("param_override", "param_override must be a JSON object")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, true, invalid("param_override", "param_override must be a JSON object")
	}
	normalized := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return nil, true, invalid("param_override", "param_override keys must be non-empty")
		}
		if _, exists := normalized[trimmed]; exists {
			return nil, true, invalid("param_override", "param_override keys must be unique after trimming")
		}
		normalized[trimmed] = append(json.RawMessage(nil), value...)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, true, invalid("param_override", "param_override must be a JSON object")
	}
	return encoded, true, nil
}

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}
