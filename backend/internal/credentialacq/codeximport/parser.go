// Package codeximport 解析 Codex OAuth 凭据的专用批量导入格式。
package codeximport

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const maxTokenBytes = 64 << 10

// Parse 接受官方 auth.json、扁平 token 对象、JSON 数组/连续 JSON、JSONL 和裸 access token 行。
func Parse(input string) ([]credentialacq.CredentialCandidate, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, credentialacq.ErrInvalidImportBody
	}
	if documents, recognized, err := decodeJSONDocuments(trimmed); recognized {
		if err != nil {
			return nil, invalid("JSON 序列格式错误")
		}
		return candidatesFromValues(documents)
	}
	if looksLikeJSON(trimmed) {
		return nil, invalid("JSON 格式错误")
	}

	lines := strings.Split(trimmed, "\n")
	out := make([]credentialacq.CredentialCandidate, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if documents, recognized, err := decodeJSONDocuments(line); recognized {
			if err != nil {
				return nil, invalid("JSON 行格式错误")
			}
			candidates, err := candidatesFromValues(documents)
			if err != nil {
				return nil, err
			}
			out = append(out, candidates...)
			continue
		}
		if looksLikeJSON(line) {
			return nil, invalid("JSON 行格式错误")
		}
		candidate, err := candidateFromToken(line, "raw_access_token")
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	if len(out) == 0 {
		return nil, credentialacq.ErrInvalidImportBody
	}
	return out, nil
}

func decodeJSONDocuments(raw string) ([]any, bool, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	documents := make([]any, 0, 1)
	for {
		var value any
		err := decoder.Decode(&value)
		if err == io.EOF {
			return documents, len(documents) > 0, nil
		}
		if err != nil {
			return documents, len(documents) > 0, err
		}
		documents = append(documents, value)
	}
}

func candidatesFromValues(values []any) ([]credentialacq.CredentialCandidate, error) {
	out := make([]credentialacq.CredentialCandidate, 0, len(values))
	for _, value := range values {
		candidates, err := candidatesFromValue(value)
		if err != nil {
			return nil, err
		}
		out = append(out, candidates...)
	}
	if len(out) == 0 {
		return nil, credentialacq.ErrInvalidImportBody
	}
	return out, nil
}

func candidatesFromValue(value any) ([]credentialacq.CredentialCandidate, error) {
	switch typed := value.(type) {
	case string:
		candidate, err := candidateFromToken(typed, "json_access_token")
		if err != nil {
			return nil, err
		}
		return []credentialacq.CredentialCandidate{candidate}, nil
	case map[string]any:
		candidate, err := candidateFromObject(typed)
		if err != nil {
			return nil, err
		}
		return []credentialacq.CredentialCandidate{candidate}, nil
	case []any:
		out := make([]credentialacq.CredentialCandidate, 0, len(typed))
		for _, item := range typed {
			candidates, err := candidatesFromValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, candidates...)
		}
		if len(out) == 0 {
			return nil, credentialacq.ErrInvalidImportBody
		}
		return out, nil
	default:
		return nil, invalid("仅支持 token 字符串或 auth.json 对象")
	}
}

func candidateFromObject(object map[string]any) (credentialacq.CredentialCandidate, error) {
	authMode := normalizeAuthMode(firstString(object, "auth_mode", "authMode"))
	switch authMode {
	case "", "chatgpt", "chatgptauthtokens":
	case "agentidentity":
		return credentialacq.CredentialCandidate{}, invalid("agent_identity 必须走专用导入入口")
	case "personalaccesstoken":
		return credentialacq.CredentialCandidate{}, invalid("personal_access_token 不是 Codex OAuth 凭据")
	default:
		return credentialacq.CredentialCandidate{}, invalid("auth_mode 不是 Codex OAuth token 模式")
	}
	if authMode == "" {
		if value, exists := object["agent_identity"]; exists && value != nil {
			return credentialacq.CredentialCandidate{}, invalid("agent_identity 必须走专用导入入口")
		}
		if firstString(object, "personal_access_token", "personalAccessToken") != "" {
			return credentialacq.CredentialCandidate{}, invalid("personal_access_token 不是 Codex OAuth 凭据")
		}
	}
	if rawTokens, exists := object["tokens"]; exists {
		tokens, ok := rawTokens.(map[string]any)
		if !ok {
			return credentialacq.CredentialCandidate{}, invalid("auth.json tokens 必须是对象")
		}
		return candidateFromTokenObject(mergeAuthJSONMetadata(tokens, object), "auth_json")
	}
	if firstString(object, "OPENAI_API_KEY", "openai_api_key") != "" && firstString(object, "access_token", "accessToken", "session_token") == "" {
		return credentialacq.CredentialCandidate{}, invalid("OpenAI API key 不是 Codex OAuth 凭据")
	}
	return candidateFromTokenObject(object, "token_object")
}

func mergeAuthJSONMetadata(tokens, outer map[string]any) map[string]any {
	merged := make(map[string]any, len(tokens)+8)
	for key, value := range tokens {
		merged[key] = value
	}
	for _, key := range []string{
		"account_id", "accountId", "chatgpt_account_id",
		"chatgpt_user_id", "user_id", "external_subject_id",
		"email", "external_account_email",
		"chatgpt_plan_type", "plan_type", "subscription_plan",
		"user_agent", "userAgent", "codex_version", "codexVersion",
		"originator", "oai_device_id", "oaiDeviceId",
	} {
		if _, exists := merged[key]; exists {
			continue
		}
		if value, exists := outer[key]; exists {
			merged[key] = value
		}
	}
	return merged
}

func normalizeAuthMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("_", "", "-", "", " ", "").Replace(value)
	return value
}

func candidateFromTokenObject(fields map[string]any, shape string) (credentialacq.CredentialCandidate, error) {
	access := firstString(fields, "access_token", "accessToken", "session_token")
	if access == "" {
		return credentialacq.CredentialCandidate{}, invalid("Codex access_token 缺失")
	}
	if err := validateToken(access); err != nil {
		return credentialacq.CredentialCandidate{}, err
	}
	refresh := firstString(fields, "refresh_token", "refreshToken")
	idToken := firstString(fields, "id_token", "idToken")
	for _, material := range []string{refresh, idToken} {
		if material != "" {
			if err := validateToken(material); err != nil {
				return credentialacq.CredentialCandidate{}, err
			}
		}
	}

	accountID := firstString(fields, "account_id", "accountId", "chatgpt_account_id")
	subjectID := firstString(fields, "chatgpt_user_id", "user_id", "external_subject_id")
	email := firstString(fields, "email", "external_account_email")
	payload := map[string]any{
		"access_token":     access,
		"session_token":    access,
		"client_id_source": credentialacq.ClientSourcePublicCLI,
	}
	if refresh != "" {
		payload["refresh_token"] = refresh
	}
	if idToken != "" {
		payload["id_token"] = idToken
	}
	if accountID != "" {
		payload["account_id"] = accountID
		payload["chatgpt_account_id"] = accountID
	}
	if subjectID != "" {
		payload["chatgpt_user_id"] = subjectID
	}
	if email != "" {
		payload["email"] = email
	}
	if plan := firstString(fields, "chatgpt_plan_type", "plan_type", "subscription_plan"); plan != "" {
		payload["chatgpt_plan_type"] = plan
	}
	if tokenType := firstString(fields, "token_type", "tokenType"); tokenType != "" {
		payload["token_type"] = tokenType
	}
	if scope := firstString(fields, "scope"); scope != "" {
		payload["scope"] = scope
	}
	for _, metadata := range []struct {
		target  string
		aliases []string
	}{
		{target: "user_agent", aliases: []string{"user_agent", "userAgent"}},
		{target: "codex_version", aliases: []string{"codex_version", "codexVersion"}},
		{target: "originator", aliases: []string{"originator"}},
		{target: "oai_device_id", aliases: []string{"oai_device_id", "oaiDeviceId"}},
	} {
		value := firstString(fields, metadata.aliases...)
		if value == "" {
			continue
		}
		if err := credentialacq.ValidateHTTPHeaderMetadata(value); err != nil {
			return credentialacq.CredentialCandidate{}, invalid(metadata.target + " 不是安全的 HTTP 头元数据")
		}
		payload[metadata.target] = value
	}
	expiresAt, err := tokenExpiry(fields, access)
	if err != nil {
		return credentialacq.CredentialCandidate{}, err
	}
	if !expiresAt.IsZero() {
		payload["expires_at"] = expiresAt.Format(time.RFC3339)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return credentialacq.CredentialCandidate{}, credentialacq.ErrInvalidImportBody
	}
	candidate := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload: raw,
		RedactedContext: map[string]any{
			"shape": shape, "credential_kind": "codex_oauth",
		},
	}
	identity := accountident.ExtractChatGPT(idToken, accountID, subjectID)
	identity.Email = firstNonEmpty(identity.Email, email)
	identity.Source = accountident.SourceImportPayload
	credentialacq.AttachIdentity(&candidate, identity)
	credentialacq.AttachSubscription(&candidate)
	return candidate, nil
}

func candidateFromToken(token, shape string) (credentialacq.CredentialCandidate, error) {
	return candidateFromTokenObject(map[string]any{"access_token": token}, shape)
}

func tokenExpiry(fields map[string]any, accessToken string) (time.Time, error) {
	if raw := firstString(fields, "expires_at", "expiresAt", "access_expires_at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, invalid("expires_at 必须是 RFC3339 时间")
		}
		return parsed.UTC(), nil
	}
	claims, err := accountident.ParseJWTClaimsUnverified(accessToken)
	if err != nil {
		return time.Time{}, nil
	}
	seconds, ok := numericClaim(claims["exp"])
	if !ok || seconds <= 0 {
		return time.Time{}, nil
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func numericClaim(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) || typed > math.MaxInt64 || typed < math.MinInt64 {
			return 0, false
		}
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func validateToken(token string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenBytes || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return invalid("token 为空、过长或包含空白字符")
	}
	return nil
}

func firstString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name].(string); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func looksLikeJSON(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "\"")
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", credentialacq.ErrInvalidImportBody, message)
}
