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
	// production 模式 fail-closed：逃逸开关在配置加载时即被拒绝，
	// 不能作为缺失 money-path 审计引用时绕过校验器的手段。
	if event.AuditLedgerID != "" && event.AuditSignatureFingerprint != "" {
		return nil
	}
	if event.AuditLedgerDLQRef != "" {
		return nil
	}
	return ErrAuditRefMissing
}
