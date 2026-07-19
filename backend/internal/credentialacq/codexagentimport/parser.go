// Package codexagentimport 解析 Codex Agent Identity 的专用批量导入格式。
package codexagentimport

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/codexagent"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// Parse 接受单个对象、对象数组、连续 JSON 对象或 JSONL，不接受裸 token 行。
func Parse(input string) ([]credentialacq.CredentialCandidate, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, credentialacq.ErrInvalidImportBody
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	values := make([]any, 0, 1)
	for {
		var value any
		err := decoder.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, invalid("仅支持 JSON 对象、数组或 JSONL")
		}
		values = append(values, value)
	}
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
	case map[string]any:
		candidate, err := candidateFromObject(typed)
		if err != nil {
			return nil, err
		}
		return []credentialacq.CredentialCandidate{candidate}, nil
	case []any:
		if len(typed) == 0 {
			return nil, credentialacq.ErrInvalidImportBody
		}
		out := make([]credentialacq.CredentialCandidate, 0, len(typed))
		for _, item := range typed {
			candidates, err := candidatesFromValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, candidates...)
		}
		return out, nil
	default:
		return nil, invalid("每一项必须是 Agent Identity JSON 对象")
	}
}

func candidateFromObject(outer map[string]any) (credentialacq.CredentialCandidate, error) {
	mode := normalizeMode(firstString(outer, "auth_mode", "authMode"))
	if mode != "" && mode != "agentidentity" && mode != "codexagentidentity" {
		return credentialacq.CredentialCandidate{}, invalid("auth_mode 不是 Agent Identity")
	}
	fields := outer
	if nested, exists := outer["agent_identity"]; exists {
		object, ok := nested.(map[string]any)
		if !ok {
			return credentialacq.CredentialCandidate{}, invalid("agent_identity 必须是对象")
		}
		fields = mergeMetadata(object, outer)
	}
	payload := map[string]any{
		"agent_runtime_id":  firstString(fields, "agent_runtime_id", "agentRuntimeId"),
		"agent_private_key": firstString(fields, "agent_private_key", "agentPrivateKey"),
		"account_id":        firstString(fields, "account_id", "accountId", "chatgpt_account_id"),
		"chatgpt_user_id":   firstString(fields, "chatgpt_user_id", "chatgptUserId", "user_id", "userId"),
	}
	copyOptionalString(payload, fields, "task_id", "task_id", "taskId")
	copyOptionalString(payload, fields, "email", "email", "external_account_email")
	copyOptionalString(payload, fields, "chatgpt_plan_type", "chatgpt_plan_type", "plan_type", "planType", "subscription_plan")
	copyOptionalString(payload, fields, "user_agent", "user_agent", "userAgent")
	copyOptionalString(payload, fields, "originator", "originator")
	copyOptionalString(payload, fields, "oai_device_id", "oai_device_id", "oaiDeviceId")
	if value, ok := firstBool(fields, "chatgpt_account_is_fedramp", "chatgptAccountIsFedramp"); ok {
		payload["chatgpt_account_is_fedramp"] = value
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return credentialacq.CredentialCandidate{}, credentialacq.ErrInvalidImportBody
	}
	if err := codexagent.ValidatePayload(raw, false); err != nil {
		return credentialacq.CredentialCandidate{}, invalid(err.Error())
	}
	candidate := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgent,
		Payload: raw,
		RedactedContext: map[string]any{
			"shape": "agent_identity", "credential_kind": "codex_agent_identity",
		},
	}
	credentialacq.AttachIdentity(&candidate, accountident.Identity{
		AccountID: firstString(payload, "account_id"),
		SubjectID: firstString(payload, "chatgpt_user_id"),
		Email:     firstString(payload, "email"),
		Source:    accountident.SourceImportPayload,
	})
	credentialacq.AttachSubscription(&candidate)
	return candidate, nil
}

func mergeMetadata(inner, outer map[string]any) map[string]any {
	merged := make(map[string]any, len(inner)+8)
	for key, value := range inner {
		merged[key] = value
	}
	for _, key := range []string{
		"account_id", "accountId", "chatgpt_account_id",
		"chatgpt_user_id", "chatgptUserId", "user_id", "userId",
		"email", "external_account_email", "chatgpt_plan_type", "plan_type", "planType", "subscription_plan",
	} {
		if _, exists := merged[key]; !exists {
			if value, ok := outer[key]; ok {
				merged[key] = value
			}
		}
	}
	return merged
}

func copyOptionalString(target map[string]any, source map[string]any, targetKey string, sourceKeys ...string) {
	if value := firstString(source, sourceKeys...); value != "" {
		target[targetKey] = value
	}
}

func firstString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func firstBool(fields map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := fields[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}

func normalizeMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(value)
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", credentialacq.ErrInvalidImportBody, message)
}
