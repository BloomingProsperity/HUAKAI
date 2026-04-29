package auth

import (
	"context"
	"time"
)

type AuditWriter interface {
	WriteRefreshAudit(ctx context.Context, entry *RefreshAuditEntry) error
}

type RefreshAuditEntry struct {
	TenantID                   int64
	ProviderAccountID          int64
	Outcome                    Outcome
	StormScope                 string
	OldRefreshTokenFingerprint string
	NewRefreshTokenFingerprint string
	ComponentsApplied          []string
	RequestID                  string
	ClientProtocol             string
	Model                      string
	ErrorClass                 string
	ErrorMessageRedacted       string
	OccurredAt                 time.Time
}

type NoopAuditWriter struct{}

func (NoopAuditWriter) WriteRefreshAudit(context.Context, *RefreshAuditEntry) error {
	return nil
}
