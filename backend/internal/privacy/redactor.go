package privacy

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrUnsafePayload   = errors.New("privacy: unsafe payload")
	ErrFieldNotAllowed = errors.New("privacy: field not allowlisted")
	ErrFreeformString  = errors.New("privacy: freeform string payload rejected")
)

type Redactor interface {
	SanitizePayload(context.Context, any) ([]byte, error)
	SanitizeError(context.Context, error) (string, error)
	AllowlistField(string) bool
}

func DefaultRedactor() *AllowlistRedactor {
	return NewAllowlistRedactor()
}

func SanitizePayload(ctx context.Context, payload any) ([]byte, error) {
	return DefaultRedactor().SanitizePayload(ctx, payload)
}

func SafePayloadOrBlocked(ctx context.Context, payload any) []byte {
	raw, err := SanitizePayload(ctx, payload)
	if err == nil {
		return raw
	}
	return BlockedPayload(ErrorClassPrivacyGuardHit)
}

func BlockedPayload(errorClass string) []byte {
	if errorClass == "" {
		errorClass = ErrorClassPrivacyGuardHit
	}
	raw, _ := json.Marshal(map[string]any{
		"schema_version":   SchemaVersion,
		"redaction_result": RedactionResultBlocked,
		"error_class":      errorClass,
	})
	return raw
}

func ContainsForbiddenRawData(raw []byte) bool {
	return containsForbiddenRawData(raw)
}
