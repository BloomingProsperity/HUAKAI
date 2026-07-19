package credentialacq

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	EventStarted     = "credential_acquisition_started"
	EventCompleted   = "credential_acquisition_completed"
	EventFailed      = "credential_acquisition_failed"
	EventCancelled   = "credential_acquisition_cancelled"
	EventPollWaiting = "credential_acquisition_poll_waiting"
)

var acqSecretKeyPattern = regexp.MustCompile(`(?i)(access[_-]?token|refresh[_-]?token|session[_-]?token|api[_-]?key|private[_-]?key|authorization|cookie|secret|pkce|code|verifier)`)

type CredentialAuditWriter interface {
	InsertAuditEvent(context.Context, credentialstore.AuditEvent) error
}

func EmitLifecycleAudit(ctx context.Context, writer CredentialAuditWriter, session Session, eventType string, credentialID int64, actorID, requestID string, payload map[string]any) error {
	if writer == nil {
		return nil
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["tenant_id"] = session.TenantID
	payload["provider_account_id"] = session.ProviderAccountID
	payload["vendor"] = session.Vendor
	payload["auth_mode"] = session.AuthMode
	payload["flow_kind"] = string(session.Kind)
	payload["client_identity_source"] = session.ClientIdentitySource
	payload = AuditSanitizePayload(payload)
	return writer.InsertAuditEvent(ctx, credentialstore.AuditEvent{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		CredentialID: credentialID, EventType: eventType, Vendor: session.Vendor, AuthMode: session.AuthMode,
		ActorID: actorID, RequestID: requestID, Payload: payload,
	})
}

func AuditSanitizePayload(input map[string]any) map[string]any {
	out := map[string]any{}
	credentialsPresent := false
	for key, value := range input {
		if acqSecretKeyPattern.MatchString(key) {
			credentialsPresent = true
			continue
		}
		out[key] = auditSanitizeValue(value, &credentialsPresent)
	}
	if credentialsPresent {
		out["credentials_present"] = true
	}
	return out
}

func ValidateRedactedContext(input map[string]any) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	clean := AuditSanitizePayload(input)
	raw, _ := json.Marshal(clean)
	if strings.Contains(string(raw), "[REDACTED]") || clean["credentials_present"] == true {
		return nil, ErrSecretInContext
	}
	return clean, nil
}

func auditSanitizeValue(value any, credentialsPresent *bool) any {
	switch v := value.(type) {
	case string:
		if auditLooksLikeSecret(v) {
			*credentialsPresent = true
			return "[REDACTED]"
		}
		return v
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, auditSanitizeValue(item, credentialsPresent))
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, auditSanitizeValue(item, credentialsPresent))
		}
		return out
	case map[string]any:
		return AuditSanitizePayload(v)
	default:
		return v
	}
}

func auditLooksLikeSecret(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "bearer ") ||
		strings.HasPrefix(lower, "sk-") ||
		strings.HasPrefix(lower, "sk-ant-") ||
		strings.Contains(lower, "refresh") && strings.Contains(lower, "token") ||
		strings.Contains(lower, "session") && strings.Contains(lower, "value") ||
		strings.Contains(lower, "private key") ||
		strings.Contains(lower, "-----begin")
}
