package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const DefaultDevicePollLease = 90 * time.Second

type DeviceCodeOption func(*deviceCodeOptions)

type deviceCodeOptions struct {
	client        *http.Client
	now           func() time.Time
	sleep         func(context.Context, time.Duration) error
	singleAttempt bool
}

func WithDeviceCodeSingleAttempt() DeviceCodeOption {
	return func(o *deviceCodeOptions) {
		o.singleAttempt = true
	}
}

const devicePollSessionColumns = `
id::text, tenant_id, provider_account_id, vendor, auth_mode, flow_kind, status,
actor_id, actor_role, state_hash, nonce_hash, encrypted_pkce_verifier,
client_identity_source, auth_type, device_code_payload,
redirect_uri, requested_scopes, redacted_context,
long_lived_requested, idempotency_key_hash, result_account_credential_id,
error_class, error_message_redacted, expires_at, consumed_at, cancelled_at,
created_at, updated_at`

func (s *PostgresSessionStore) SetAuthPayload(ctx context.Context, id string, authType AuthType, payload map[string]any) error {
	if s == nil || s.db == nil {
		return errors.New("credentialacq: session store not configured")
	}
	if authType == "" {
		authType = AuthTypePKCE
	}
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	session, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if isTerminalStatus(session.Status) || !session.ConsumedAt.IsZero() {
		return ErrFlowReplay
	}
	if !session.ExpiresAt.After(s.now().UTC()) {
		_, _ = s.UpdateStatus(ctx, id, StatusExpired, "expired", "acquisition flow expired")
		return ErrFlowExpired
	}
	ciphertext, metadata, _, err := s.EncryptTransientPayload(ctx, raw, pkceAADFromSession(session))
	if err != nil {
		return err
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET auth_type = $2::oauth_acquisition_auth_type,
    device_code_payload = '{}'::jsonb,
    encrypted_pkce_verifier = $3,
    nonce_hash = $4,
    updated_at = NOW()
WHERE id = $1::uuid
  AND consumed_at IS NULL
  AND expires_at > NOW()
  AND status NOT IN ('finalized', 'cancelled', 'expired', 'failed')`
	tag, err := s.db.Exec(ctx, q, strings.TrimSpace(id), string(authType), ciphertext, metadata)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	existing, getErr := s.Get(ctx, id)
	if getErr != nil {
		return getErr
	}
	if isTerminalStatus(existing.Status) || !existing.ConsumedAt.IsZero() {
		return ErrFlowReplay
	}
	if !existing.ExpiresAt.After(s.now().UTC()) {
		return ErrFlowExpired
	}
	return ErrFlowNotFound
}

func (s *PostgresSessionStore) hydrateAuthPayload(ctx context.Context, row Session) (Session, error) {
	if (row.AuthType != AuthTypeDeviceCode && row.AuthType != AuthTypeSSO) ||
		isTerminalStatus(row.Status) || !row.ConsumedAt.IsZero() {
		row.DeviceCodePayload = nil
		return row, nil
	}
	if len(row.EncryptedPKCEVerifier) == 0 || len(row.NonceHash) == 0 {
		return Session{}, fmt.Errorf("%w: encrypted auth payload missing", credentialstore.ErrDecryptFailed)
	}
	plaintext, err := s.DecryptTransientPayload(ctx, row.EncryptedPKCEVerifier, row.NonceHash, pkceAADFromSession(row))
	if err != nil {
		return Session{}, err
	}
	if err := json.Unmarshal(plaintext, &row.DeviceCodePayload); err != nil || row.DeviceCodePayload == nil {
		return Session{}, fmt.Errorf("%w: encrypted auth payload invalid", credentialstore.ErrDecryptFailed)
	}
	return row, nil
}

func (s *PostgresSessionStore) claimDevicePoll(ctx context.Context, id string, ownerHash []byte, lease time.Duration) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, errors.New("credentialacq: session store not configured")
	}
	if lease <= 0 {
		lease = DefaultDevicePollLease
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET status = 'callback_received',
    state_hash = $2,
    error_class = NULL,
    error_message_redacted = NULL,
    updated_at = NOW()
WHERE id = $1::uuid
  AND auth_type IN ('device_code', 'sso')
  AND consumed_at IS NULL
  AND expires_at > NOW()
  AND (
      status IN ('started', 'waiting_for_user')
      OR (status = 'callback_received' AND updated_at < $3)
  )
RETURNING ` + devicePollSessionColumns
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id), ownerHash, s.now().UTC().Add(-lease)))
	if err == nil {
		return s.hydrateAuthPayload(ctx, row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	existing, getErr := s.Get(ctx, id)
	if getErr != nil {
		return Session{}, getErr
	}
	if !existing.ExpiresAt.After(s.now().UTC()) || existing.Status == StatusExpired {
		return existing, ErrFlowExpired
	}
	if existing.AuthType != AuthTypeDeviceCode && existing.AuthType != AuthTypeSSO {
		return existing, ErrInvalidTokenShape
	}
	if existing.Status == StatusCallbackReceived {
		return existing, ErrDevicePollInProgress
	}
	return existing, ErrFlowReplay
}

func (s *PostgresSessionStore) finishDevicePoll(ctx context.Context, id string, ownerHash []byte, status FlowStatus, errorClass, message string) (Session, error) {
	if status != StatusWaitingForUser && status != StatusExpired && status != StatusFailed {
		return Session{}, ErrInvalidImportBody
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET status = $3,
    state_hash = NULL,
    error_class = NULLIF($4, ''),
    error_message_redacted = NULLIF($5, ''),
    encrypted_pkce_verifier = CASE WHEN $3::text IN ('expired', 'failed') THEN NULL ELSE encrypted_pkce_verifier END,
    nonce_hash = CASE WHEN $3::text IN ('expired', 'failed') THEN NULL ELSE nonce_hash END,
    device_code_payload = '{}'::jsonb,
    updated_at = NOW()
WHERE id = $1::uuid
  AND state_hash = $2
  AND status = 'callback_received'
  AND consumed_at IS NULL
RETURNING ` + devicePollSessionColumns
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id), ownerHash, status, strings.TrimSpace(errorClass), strings.TrimSpace(message)))
	if err == nil {
		return s.hydrateAuthPayload(ctx, row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	existing, getErr := s.Get(ctx, id)
	if getErr != nil {
		return Session{}, getErr
	}
	if existing.Status == StatusCallbackReceived {
		return existing, ErrDevicePollInProgress
	}
	return existing, ErrFlowReplay
}

func (s *PostgresSessionStore) completeDevicePoll(ctx context.Context, id string, ownerHash []byte, candidate CredentialCandidate) (Session, error) {
	if !json.Valid(candidate.Payload) {
		return Session{}, ErrInvalidTokenShape
	}
	var payload map[string]any
	if err := json.Unmarshal(candidate.Payload, &payload); err != nil || payload == nil {
		return Session{}, ErrInvalidTokenShape
	}
	claimed, err := s.Get(ctx, id)
	if err != nil {
		return Session{}, err
	}
	ciphertext, metadata, _, err := s.EncryptTransientPayload(ctx, candidate.Payload, pkceAADFromSession(claimed))
	if err != nil {
		return Session{}, err
	}
	const q = `
UPDATE credential_acquisition_flow_sessions
SET status = 'validated',
    state_hash = NULL,
    error_class = NULL,
    error_message_redacted = NULL,
    encrypted_pkce_verifier = $3,
    nonce_hash = $4,
    device_code_payload = '{}'::jsonb,
    updated_at = NOW()
WHERE id = $1::uuid
  AND state_hash = $2
  AND status = 'callback_received'
  AND consumed_at IS NULL
RETURNING ` + devicePollSessionColumns
	row, err := scanSession(s.db.QueryRow(ctx, q, strings.TrimSpace(id), ownerHash, ciphertext, metadata))
	if err == nil {
		return s.hydrateAuthPayload(ctx, row)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, err
	}
	existing, getErr := s.Get(ctx, id)
	if getErr != nil {
		return Session{}, getErr
	}
	if existing.Status == StatusValidated {
		return existing, nil
	}
	if existing.Status == StatusCallbackReceived {
		return existing, ErrDevicePollInProgress
	}
	return existing, ErrFlowReplay
}

func PollDeviceCodeFlow(ctx context.Context, store *PostgresSessionStore, session Session, poller DeviceCodePoller, audit CredentialAuditWriter, actorID, requestID string) (CredentialCandidate, Session, error) {
	if store == nil {
		return CredentialCandidate{}, Session{}, errors.New("credentialacq: session store not configured")
	}
	if session.AuthType != AuthTypeDeviceCode && session.AuthType != AuthTypeSSO {
		return CredentialCandidate{}, session, ErrInvalidTokenShape
	}
	if session.Status == StatusValidated {
		candidate, err := candidateFromStoredDeviceToken(session)
		return candidate, session, err
	}
	if isTerminalStatus(session.Status) || !session.ConsumedAt.IsZero() {
		if session.Status == StatusExpired {
			return CredentialCandidate{}, session, ErrFlowExpired
		}
		return CredentialCandidate{}, session, ErrFlowReplay
	}
	if poller == nil {
		poller = func(pollCtx context.Context, current Session) (CredentialCandidate, error) {
			return PollDeviceCodeToken(pollCtx, current, OAuthClientConfig{}, WithDeviceCodeSingleAttempt())
		}
	}
	owner, err := randomURLToken(24)
	if err != nil {
		return CredentialCandidate{}, session, err
	}
	ownerHash := HashIdempotencyKey(firstNonEmpty(requestID, owner))
	claimed, err := store.claimDevicePoll(ctx, session.ID, ownerHash, DefaultDevicePollLease)
	if err != nil {
		return CredentialCandidate{}, claimed, err
	}
	candidate, pollErr := poller(ctx, claimed)
	if pollErr == nil {
		wctx, cancel := finalizeWriteCtx(ctx)
		validated, completeErr := store.completeDevicePoll(wctx, claimed.ID, ownerHash, candidate)
		cancel()
		if completeErr != nil {
			return CredentialCandidate{}, validated, completeErr
		}
		stored, storedErr := candidateFromStoredDeviceToken(validated)
		return stored, validated, storedErr
	}
	status, errorClass, publicErr := classifyDevicePollError(pollErr)
	wctx, cancel := finalizeWriteCtx(ctx)
	updated, finishErr := store.finishDevicePoll(wctx, claimed.ID, ownerHash, status, errorClass, devicePollMessage(errorClass))
	eventType := EventPollWaiting
	if status == StatusExpired || status == StatusFailed {
		eventType = EventFailed
	}
	_ = EmitLifecycleAudit(wctx, audit, updated, eventType, 0, actorID, requestID, map[string]any{"error_class": errorClass})
	cancel()
	if finishErr != nil {
		return CredentialCandidate{}, updated, finishErr
	}
	logDevicePollOutcome(ctx, updated, requestID, errorClass, status)
	return CredentialCandidate{}, updated, publicErr
}

func candidateFromStoredDeviceToken(session Session) (CredentialCandidate, error) {
	if session.DeviceCodePayload == nil {
		return CredentialCandidate{}, ErrInvalidTokenShape
	}
	raw, err := json.Marshal(session.DeviceCodePayload)
	if err != nil {
		return CredentialCandidate{}, ErrInvalidTokenShape
	}
	return candidateFromDeviceTokenPayload(session, raw), nil
}

func classifyDevicePollRequestError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrResponseTooLarge) {
		return err
	}
	if errors.Is(err, auth.ErrOAuthEndpointBlocked) {
		return ErrFeatureDisabled
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return ErrInvalidTokenShape
	}
	return ErrDevicePollTransient
}

func classifyDevicePollError(err error) (FlowStatus, string, error) {
	switch {
	case errors.Is(err, ErrDevicePollPending):
		return StatusWaitingForUser, "authorization_pending", err
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return StatusWaitingForUser, "request_cancelled", err
	case errors.Is(err, ErrFlowExpired):
		return StatusExpired, "authorization_expired", ErrFlowExpired
	case errors.Is(err, ErrDeviceAccessDenied):
		return StatusFailed, "authorization_denied", ErrDeviceAccessDenied
	case errors.Is(err, ErrDeviceExchangeAmbiguous):
		return StatusFailed, "token_exchange_ambiguous", ErrDeviceExchangeAmbiguous
	case errors.Is(err, ErrResponseTooLarge):
		return StatusFailed, "response_too_large", ErrResponseTooLarge
	case errors.Is(err, ErrInvalidTokenShape):
		return StatusFailed, "invalid_response", ErrInvalidTokenShape
	case errors.Is(err, ErrFeatureDisabled):
		return StatusFailed, "endpoint_rejected", ErrFeatureDisabled
	default:
		return StatusWaitingForUser, "upstream_transient", ErrDevicePollTransient
	}
}

func devicePollMessage(errorClass string) string {
	switch errorClass {
	case "authorization_pending":
		return "等待用户完成设备授权"
	case "request_cancelled":
		return "轮询请求已取消，可安全重试"
	case "authorization_expired":
		return "设备授权已过期"
	case "authorization_denied":
		return "用户拒绝了设备授权"
	case "response_too_large", "invalid_response", "endpoint_rejected", "token_exchange_ambiguous":
		return "设备授权响应未通过安全校验"
	default:
		return "上游设备授权暂时不可用，可安全重试"
	}
}

func logDevicePollOutcome(ctx context.Context, session Session, requestID, errorClass string, status FlowStatus) {
	attrs := []any{
		"tenant_id", session.TenantID,
		"provider_account_id", session.ProviderAccountID,
		"flow_id", session.ID,
		"request_id", requestID,
		"error_class", errorClass,
		"status", status,
	}
	if errorClass == "authorization_pending" {
		slog.DebugContext(ctx, "设备授权仍在等待用户确认", attrs...)
		return
	}
	slog.WarnContext(ctx, "设备授权轮询未完成", attrs...)
}
