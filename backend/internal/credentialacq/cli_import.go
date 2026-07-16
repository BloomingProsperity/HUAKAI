package credentialacq

import (
	"encoding/csv"
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func ParseCLIImportContent(input string) ([]CredentialCandidate, error) {
	return ParseImportContent(input, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
}

func ParseImportContent(input, defaultVendor, defaultAuthMode string) ([]CredentialCandidate, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, ErrInvalidImportBody
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		return importCandidatesFromDecoded(decoded, defaultVendor, defaultAuthMode)
	}
	lines := strings.Split(trimmed, "\n")
	out := make([]CredentialCandidate, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var one any
		if err := json.Unmarshal([]byte(line), &one); err == nil {
			got, err := importCandidatesFromDecoded(one, defaultVendor, defaultAuthMode)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
			continue
		} else if jsonLikeLine(line) {
			// 行以 { 或 [开头 = 明显想写结构化 JSON,但解析失败 = 畸形导入。绝不能把畸形 JSON
			// 静默当作 raw session token 吞下(否则 `{"session_token":"abc"` 这种缺括号会被原样存成 token
			// 文本,导入"看似成功"实则凭据不可用)。报错让调用方修正。纯 token 行(不以 {/[ 开头)仍兼容。
			return nil, ErrInvalidImportBody
		}
		out = append(out, importTokenCandidate(line, defaultVendor, defaultAuthMode))
	}
	if len(out) == 0 {
		return nil, ErrInvalidImportBody
	}
	return out, nil
}

// jsonLikeLine 判断一行是否"看起来是 JSON"(以 { 或 [ 开头)。session token 一般是 base64/JWT,不会以
// 这两个字符开头,故以此区分"想写 JSON 但写坏了"与"合法 raw token 行"。
func jsonLikeLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[")
}

func ParseCSVImportContent(input, defaultVendor, defaultAuthMode string) ([]CredentialCandidate, error) {
	reader := csv.NewReader(strings.NewReader(input))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, ErrInvalidImportBody
	}
	header := rows[0]
	out := make([]CredentialCandidate, 0, len(rows)-1)
	for _, row := range rows[1:] {
		fields := map[string]any{}
		for i, name := range header {
			if i >= len(row) {
				continue
			}
			name = strings.TrimSpace(name)
			if name != "" {
				fields[name] = strings.TrimSpace(row[i])
			}
		}
		if len(fields) == 0 {
			continue
		}
		out = append(out, importCandidateFromMap(fields, defaultVendor, defaultAuthMode))
	}
	if len(out) == 0 {
		return nil, ErrInvalidImportBody
	}
	return out, nil
}

func importCandidatesFromDecoded(decoded any, defaultVendor, defaultAuthMode string) ([]CredentialCandidate, error) {
	switch v := decoded.(type) {
	case map[string]any:
		return []CredentialCandidate{importCandidateFromMap(v, defaultVendor, defaultAuthMode)}, nil
	case []any:
		out := make([]CredentialCandidate, 0, len(v))
		for _, item := range v {
			switch typed := item.(type) {
			case map[string]any:
				out = append(out, importCandidateFromMap(typed, defaultVendor, defaultAuthMode))
			case string:
				out = append(out, importTokenCandidate(typed, defaultVendor, defaultAuthMode))
			default:
				return nil, ErrInvalidImportBody
			}
		}
		return out, nil
	case string:
		return []CredentialCandidate{importTokenCandidate(v, defaultVendor, defaultAuthMode)}, nil
	default:
		return nil, ErrInvalidImportBody
	}
}

func importCandidateFromMap(fields map[string]any, defaultVendor, defaultAuthMode string) CredentialCandidate {
	vendor := importStringField(fields, "vendor", defaultVendor)
	mode := importStringField(fields, "auth_mode", defaultAuthMode)
	flattened := flattenCLITokenObject(fields)
	payload, _ := json.Marshal(flattened)
	candidate := CredentialCandidate{
		Vendor: credentialstore.Normalize(vendor), AuthMode: credentialstore.Normalize(mode), Payload: payload,
		RedactedContext: map[string]any{"shape": "json_object"},
	}
	attachImportIdentity(&candidate, flattened)
	return candidate
}

func attachImportIdentity(candidate *CredentialCandidate, fields map[string]any) {
	if candidate == nil {
		return
	}
	explicit := accountident.Identity{
		AccountID: firstImportString(fields, "external_account_id", "chatgpt_account_id", "account_id"),
		SubjectID: firstImportString(fields, "external_subject_id", "chatgpt_user_id"),
		Email:     firstImportString(fields, "external_account_email", "email"),
		Source:    accountident.SourceImportPayload,
	}
	identity := explicit
	switch candidate.Vendor {
	case credentialstore.VendorOpenAI:
		idToken := firstImportString(fields, "id_token")
		extracted := accountident.ExtractChatGPT(
			idToken,
			explicit.AccountID,
			explicit.SubjectID,
		)
		if !extracted.Empty() {
			extracted.Email = firstNonEmptyImport(extracted.Email, explicit.Email)
			identity = extracted
		}
	case credentialstore.VendorGemini:
		extracted := accountident.ExtractGemini(firstImportString(fields, "id_token"), explicit.Email)
		if !extracted.Empty() {
			identity = extracted
		}
	case credentialstore.VendorAnthropic:
		extracted := accountident.ExtractAnthropic(explicit.AccountID, explicit.Email, "")
		if !extracted.Empty() {
			identity = extracted
		}
	}
	// 导入文件完全由运营者提供；即使其中的 JWT 能解析，也不能把声明升级成
	// provider token 交换来源，否则伪造 subject 会获得自动覆盖已有账号的能力。
	identity.Source = accountident.SourceImportPayload
	AttachIdentity(candidate, identity)
}

// flattenCLITokenObject 识别 CLI 凭据文件的 {token:{...}} 外层，把运行时
// 需要的 token 字段扁平化；expiry 是 RFC3339 时间，存储层统一读取
// expires_at。普通扁平 JSON 不经过特殊改写。
func flattenCLITokenObject(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+3)
	for key, value := range fields {
		out[key] = value
	}
	token, ok := fields["token"].(map[string]any)
	if !ok {
		return out
	}
	delete(out, "token")
	for _, key := range []string{"access_token", "token_type", "refresh_token"} {
		if value, exists := token[key]; exists {
			out[key] = value
		}
	}
	if expiry, ok := token["expiry"].(string); ok && strings.TrimSpace(expiry) != "" {
		out["expires_at"] = strings.TrimSpace(expiry)
	}
	return out
}

func importTokenCandidate(token, defaultVendor, defaultAuthMode string) CredentialCandidate {
	payload, _ := json.Marshal(map[string]string{"session_token": strings.TrimSpace(token)})
	return CredentialCandidate{
		Vendor: credentialstore.Normalize(defaultVendor), AuthMode: credentialstore.Normalize(defaultAuthMode), Payload: payload,
		RedactedContext: map[string]any{"shape": "single_token"},
	}
}

func importStringField(fields map[string]any, key, fallback string) string {
	switch value := fields[key].(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}

func firstImportString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func firstNonEmptyImport(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
