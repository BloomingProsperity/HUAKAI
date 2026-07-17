// Package codexagentimport 解析 Codex Agent Identity 的结构化导入数据。
package codexagentimport

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrInvalid = errors.New("codex agent identity import invalid")

type Entry struct {
	RuntimeID         string
	PrivateKeyPKCS8   string
	UpstreamAccountID string
	UpstreamUserID    string
	TaskID            string
	Email             string
	Plan              string
	FedRAMP           bool
	FedRAMPSet        bool
}

func Parse(input string) ([]Entry, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, ErrInvalid
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		return entries(decoded)
	}
	var out []Entry
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, ErrInvalid
		}
		parsed, err := entries(item)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed...)
	}
	if len(out) == 0 {
		return nil, ErrInvalid
	}
	return out, nil
}

func entries(decoded any) ([]Entry, error) {
	switch value := decoded.(type) {
	case map[string]any:
		entry, err := parseEntry(value)
		if err != nil {
			return nil, err
		}
		return []Entry{entry}, nil
	case []any:
		out := make([]Entry, 0, len(value))
		for _, item := range value {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, ErrInvalid
			}
			entry, err := parseEntry(object)
			if err != nil {
				return nil, err
			}
			out = append(out, entry)
		}
		if len(out) == 0 {
			return nil, ErrInvalid
		}
		return out, nil
	default:
		return nil, ErrInvalid
	}
}

func parseEntry(input map[string]any) (Entry, error) {
	fields := input
	for _, wrapper := range []string{"agent_identity", "agentIdentity"} {
		if nested, ok := input[wrapper].(map[string]any); ok {
			fields = nested
			break
		}
	}
	entry := Entry{
		RuntimeID:         firstString(fields, "runtime_id", "agent_runtime_id", "runtimeId", "agentRuntimeId"),
		PrivateKeyPKCS8:   firstString(fields, "private_key_pkcs8", "agent_private_key", "privateKey", "agentPrivateKey"),
		UpstreamAccountID: firstString(fields, "upstream_account_id", "chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"),
		UpstreamUserID:    firstString(fields, "upstream_user_id", "chatgpt_user_id", "chatgptUserId", "user_id", "userId"),
		TaskID:            firstString(fields, "task_id", "taskId"),
		Email:             firstString(fields, "email"),
		Plan:              firstString(fields, "plan", "plan_type", "planType"),
	}
	if entry.RuntimeID == "" || entry.PrivateKeyPKCS8 == "" || entry.UpstreamAccountID == "" || entry.UpstreamUserID == "" {
		return Entry{}, ErrInvalid
	}
	fedramp, present, err := boolAliases(fields, "fedramp", "is_fedramp", "chatgpt_account_is_fedramp", "chatgptAccountIsFedramp")
	if err != nil {
		return Entry{}, err
	}
	entry.FedRAMP = fedramp
	entry.FedRAMPSet = present
	return entry, nil
}

func firstString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolAliases(fields map[string]any, names ...string) (bool, bool, error) {
	var value bool
	present := false
	for _, name := range names {
		raw, exists := fields[name]
		if !exists {
			continue
		}
		current, ok := raw.(bool)
		if !ok || (present && current != value) {
			return false, false, ErrInvalid
		}
		value = current
		present = true
	}
	return value, present, nil
}
