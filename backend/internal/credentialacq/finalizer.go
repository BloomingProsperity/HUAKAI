package credentialacq

import (
	"context"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

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
		failed, _ := f.sessions.MarkFailed(ctx, flowID, "finalizer_rejected", redactedErr(err))
		_ = EmitLifecycleAudit(ctx, f.audit, failed, EventFailed, 0, actorID, requestID, map[string]any{"error_class": "finalizer_rejected"})
		return FinalizeResult{Session: failed}, err
	}
	meta, err := f.creator.Create(ctx, credentialstore.CreateCredentialInput{
		TenantID: candidate.TenantID, ProviderAccountID: candidate.ProviderAccountID,
		Vendor: candidate.Vendor, AuthMode: candidate.AuthMode, Payload: candidate.Payload,
		ActorID: firstNonEmpty(candidate.ActorID, actorID),
	})
	if err != nil {
		failed, _ := f.sessions.MarkFailed(ctx, flowID, "credential_create_failed", redactedErr(err))
		_ = EmitLifecycleAudit(ctx, f.audit, failed, EventFailed, 0, actorID, requestID, map[string]any{"error_class": "credential_create_failed"})
		return FinalizeResult{Session: failed}, err
	}
	finalized, err := f.sessions.MarkFinalized(ctx, flowID, meta.ID)
	if err != nil {
		return FinalizeResult{Session: session, Credential: meta}, err
	}
	_ = EmitLifecycleAudit(ctx, f.audit, finalized, EventCompleted, meta.ID, actorID, requestID, map[string]any{
		"credential_id": meta.ID,
		"metadata_keys": redactedContextKeys(finalized.RedactedContext),
	})
	return FinalizeResult{Session: finalized, Credential: meta}, nil
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
