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
	if policy.AllowMissingMoneyRef {
		return nil
	}
	if event.AuditLedgerID != "" && event.AuditSignatureFingerprint != "" {
		return nil
	}
	if event.AuditLedgerDLQRef != "" {
		return nil
	}
	return ErrAuditRefMissing
}
