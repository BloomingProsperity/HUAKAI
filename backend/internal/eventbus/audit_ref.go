package eventbus

import "errors"

type ReleaseMode string

const (
	ReleaseModeProduction ReleaseMode = "production"
	ReleaseModeDev        ReleaseMode = "dev"
	ReleaseModeTest       ReleaseMode = "test"
)

type AuditRefPolicy struct {
	ReleaseMode          ReleaseMode
	AllowMissingMoneyRef bool
}

var ErrAuditRefMissing = errors.New("eventbus: money-path 缺少有效账本引用")

func ValidateMoneyPathAuditRef(event *RequestCompletionEvent, policy *AuditRefPolicy) error {
	if policy == nil {
		return nil
	}
	if policy.ReleaseMode != ReleaseModeProduction {
		return nil
	}
	// Production is fail-closed: the escape flag is rejected at config load and
	// is not a validator bypass for missing money-path audit references.
	if event.AuditLedgerID != "" && event.AuditSignatureFingerprint != "" {
		return nil
	}
	if event.AuditLedgerDLQRef != "" {
		return nil
	}
	return ErrAuditRefMissing
}
