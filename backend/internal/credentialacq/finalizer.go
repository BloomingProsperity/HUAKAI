package credentialacq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// finalizeWriteCtx 交付后持久写 (MarkFinalized / MarkFailed / 审计) 一律脱离请求 ctx:
// creator.Create 已提交后客户端断连不得把 flow 留在 consumed 非终态 —— 那等于活凭据
// 孤儿 + 重试恒 ErrFlowReplay 卡死 (只能等 expires_at 惰性过期)。
func finalizeWriteCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

type CredentialCreator interface {
	Create(context.Context, credentialstore.CreateCredentialInput) (credentialstore.CredentialMetadata, error)
}

type Finalizer struct {
	sessions *PostgresSessionStore
	registry *credentialstore.HandlerRegistry
	creator  CredentialCreator
	audit    CredentialAuditWriter
}

func NewFinalizer(sessions *PostgresSessionStore, registry *credentialstore.HandlerRegistry, creator CredentialCreator, audit CredentialAuditWriter) *Finalizer {
	if registry == nil {
		registry = credentialstore.DefaultHandlerRegistry()
	}
	return &Finalizer{sessions: sessions, registry: registry, creator: creator, audit: audit}
}

func (f *Finalizer) ValidateCandidate(candidate CredentialCandidate) error {
	if f == nil || f.registry == nil {
		return fmt.Errorf("%w: registry missing", ErrUnknownMode)
	}
	candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
	candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
	handler, err := f.registry.MustLookup(candidate.Vendor, candidate.AuthMode)
	if err != nil {
		return ErrUnknownMode
	}
	if err := handler.ValidatePayload(candidate.Payload); err != nil {
		return err
	}
	return nil
}

func (f *Finalizer) Finalize(ctx context.Context, flowID string, candidate CredentialCandidate, actorID, requestID string) (FinalizeResult, error) {
	if f == nil || f.sessions == nil || f.creator == nil {
		return FinalizeResult{}, fmt.Errorf("credentialacq: finalizer not configured")
	}
	session, err := f.sessions.BeginFinalize(ctx, flowID)
	if err != nil {
		if session.Status == StatusFinalized && session.ResultAccountCredentialID > 0 {
			return FinalizeResult{Session: session, Credential: credentialstore.CredentialMetadata{ID: session.ResultAccountCredentialID}, AlreadyFinalized: true}, nil
		}
		return FinalizeResult{Session: session}, err
	}
	candidate = fillCandidateFromSession(session, candidate)
	if err := f.ValidateCandidate(candidate); err != nil {
		// 补偿写脱钩: BeginFinalize 已置 consumed_at, MarkFailed 若随请求取消而失败,
		// flow 留在 consumed 非终态卡死。
		wctx, cancel := finalizeWriteCtx(ctx)
		failed, _ := f.sessions.MarkFailed(wctx, flowID, "finalizer_rejected", redactedErr(err))
		_ = EmitLifecycleAudit(wctx, f.audit, failed, EventFailed, 0, actorID, requestID, map[string]any{"error_class": "finalizer_rejected"})
		cancel()
		return FinalizeResult{Session: failed}, err
	}
	meta, err := f.creator.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: candidate.TenantID, ProviderAccountID: candidate.ProviderAccountID,
		Vendor: candidate.Vendor, AuthMode: candidate.AuthMode, Payload: candidate.Payload,
		ActorID:              firstNonEmpty(candidate.ActorID, actorID),
		ExternalAccountID:    candidate.ExternalAccountID,
		ExternalAccountEmail: candidate.ExternalAccountEmail,
	})
	if err != nil {
		wctx, cancel := finalizeWriteCtx(ctx)
		failed, _ := f.sessions.MarkFailed(wctx, flowID, "credential_create_failed", redactedErr(err))
		_ = EmitLifecycleAudit(wctx, f.audit, failed, EventFailed, 0, actorID, requestID, map[string]any{"error_class": "credential_create_failed"})
		cancel()
		return FinalizeResult{Session: failed}, err
	}
	// Create 已提交, 此后是"记录既成事实": 脱钩 + 重试, 尽最大努力不留孤儿。
	finalized, err := f.markFinalizedWithRetry(ctx, flowID, meta.ID)
	if err != nil {
		return FinalizeResult{Session: session, Credential: meta}, err
	}
	wctx, cancel := finalizeWriteCtx(ctx)
	_ = EmitLifecycleAudit(wctx, f.audit, finalized, EventCompleted, meta.ID, actorID, requestID, map[string]any{
		"credential_id": meta.ID,
		"metadata_keys": redactedContextKeys(finalized.RedactedContext),
	})
	cancel()
	return FinalizeResult{Session: finalized, Credential: meta}, nil
}

// markFinalizedWithRetry 凭据已建成, flow 状态写失败即活凭据孤儿 + flow 卡死,
// 带退避重试收窄窗口; 全败后剩余孤儿由 expires_at 惰性过期兜底。
func (f *Finalizer) markFinalizedWithRetry(ctx context.Context, flowID string, credentialID int64) (Session, error) {
	var (
		finalized Session
		err       error
	)
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 100 * time.Millisecond)
		}
		wctx, cancel := finalizeWriteCtx(ctx)
		finalized, err = f.sessions.MarkFinalized(wctx, flowID, credentialID)
		cancel()
		if err == nil {
			return finalized, nil
		}
	}
	return finalized, err
}

func fillCandidateFromSession(session Session, candidate CredentialCandidate) CredentialCandidate {
	if candidate.TenantID == 0 {
		candidate.TenantID = session.TenantID
	}
	if candidate.ProviderAccountID == 0 {
		candidate.ProviderAccountID = session.ProviderAccountID
	}
	if candidate.Vendor == "" {
		candidate.Vendor = session.Vendor
	}
	if candidate.AuthMode == "" {
		candidate.AuthMode = session.AuthMode
	}
	if candidate.ActorID == "" {
		candidate.ActorID = session.ActorID
	}
	return candidate
}

func redactedErr(err error) string {
	if err == nil {
		return ""
	}
	msg := AuditSanitizePayload(map[string]any{"message": err.Error()})["message"]
	if s, ok := msg.(string); ok {
		return s
	}
	return "credential acquisition failed"
}

func redactedContextKeys(ctx map[string]any) []string {
	keys := make([]string, 0, len(ctx))
	for key := range ctx {
		keys = append(keys, strings.TrimSpace(key))
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
