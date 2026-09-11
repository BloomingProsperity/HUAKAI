package credentialstore

import (
	"encoding/json"
	"fmt"
	"strings"
)

const refreshMaterialField = "refresh_token"

// payloadOmitsRefreshMaterial 在规范化后的凭据对象上判断本次写入是否没有可续期材料。
// 缺席、空串和空白一律视为未提供，避免运营粘贴空字段时把已有续期材料冲掉。
func payloadOmitsRefreshMaterial(raw []byte) bool {
	fields, err := parsePayloadFields(raw)
	if err != nil {
		return false
	}
	return strings.TrimSpace(fieldString(fields, refreshMaterialField)) == ""
}

// copyExistingRefreshMaterial 仅在入站对象缺少续期材料、且当前明文仍有非空续期材料时拷回。
// 入站已带非空续期材料时原样采用新值。不会编造原本不存在的续期材料。
func copyExistingRefreshMaterial(incoming, existing []byte) ([]byte, bool, error) {
	incomingFields, err := parsePayloadFields(incoming)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(fieldString(incomingFields, refreshMaterialField)) != "" {
		return incoming, false, nil
	}
	existingFields, err := parsePayloadFields(existing)
	if err != nil {
		return nil, false, err
	}
	kept := strings.TrimSpace(fieldString(existingFields, refreshMaterialField))
	if kept == "" {
		return incoming, false, nil
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	incomingFields[refreshMaterialField] = json.RawMessage(encoded)
	out, err := json.Marshal(incomingFields)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	return out, true, nil
}
