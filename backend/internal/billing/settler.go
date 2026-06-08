package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/cachemetrics"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

var (
	ErrClaimNotReserving        = errors.New("billing: claim is not reserving")
	ErrAcquisitionTokenMismatch = errors.New("billing: acquisition token mismatch")
	ErrSlotReleaseMissed        = errors.New("billing: pool_slot_acquisitions row not in 'acquired' state for token")
	ErrCostOverflow             = errors.New("billing: cost overflow")
)

const maxCostMicroUSDInt64 int64 = 1<<63 - 1

const RefundSkippedAmountZeroRef = "refund_skipped_amount_zero"

// settlement_source 判别值 (migration 0043): provider_upstream = 正常上游路径
// (provider_account_id / acquisition_token 必非空); response_cache_l2 = L2
// 缓存命中路径 (两者必为空, 无上游账号)。
const (
	SettlementSourceProviderUpstream = "provider_upstream"
	SettlementSourceResponseCacheL2  = "response_cache_l2"
)

var maxCostMicroUSDDecimal = decimal.NewFromInt(maxCostMicroUSDInt64)

type DefaultSettler struct {
	pool          *pgxpool.Pool
	q             *dbbilling.Queries
	dlqStore      *dlq.Store
	replicaTarget string
}

type SettlerOption func(*DefaultSettler)

func WithDLQStore(store *dlq.Store) SettlerOption {
	return func(s *DefaultSettler) { s.dlqStore = store }
}

func WithReplicaTarget(target string) SettlerOption {
	return func(s *DefaultSettler) {
		target = strings.TrimSpace(target)
		if target != "" {
			s.replicaTarget = target
		}
	}
}

func NewSettler(pool *pgxpool.Pool, opts ...SettlerOption) *DefaultSettler {
	if pool == nil {
		return &DefaultSettler{pool: nil}
	}
	s := &DefaultSettler{pool: pool, q: dbbilling.New(pool), dlqStore: dlq.NewStore(pool)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DefaultSettler) Settle(ctx context.Context, req SettleRequest) (*SettleResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrPoolNotConfigured
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("billing: begin Tx2: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := s.q.WithTx(tx)
	claim, err := qtx.GetClaimForSettle(ctx, dbbilling.GetClaimForSettleParams{
		ID:               req.ClaimID,
		TenantID:         req.TenantID,
		AcquisitionToken: req.AcquisitionToken,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, s.classifySettleNoRows(ctx, tx, req)
	}
	if err != nil {
		return nil, fmt.Errorf("billing: get claim for settle: %w", err)
	}

	// 一致性: claim 行经 Tx2 锁定 (tenant_id+claim_id+acquisition_token), 其
	// providerAccountID / APIKeyID / UserID / AttemptSeq 是权威值; req 字段
	// 可能来自 caller 上下文 (e.g. retry / shadow), 不可覆盖 claim 列。
	// 不一致直接 reject 防 usage_record 写错归属。
	if claim.ProviderAccountID == nil || *claim.ProviderAccountID == 0 {
		return nil, fmt.Errorf("billing: provider account id missing for claim %d", req.ClaimID)
	}
	providerAccountID := *claim.ProviderAccountID
	if req.ProviderAccountID != 0 && req.ProviderAccountID != providerAccountID {
		return nil, fmt.Errorf("billing: settle req.ProviderAccountID=%d ≠ claim=%d (claim=%d)",
			req.ProviderAccountID, providerAccountID, req.ClaimID)
	}
	if req.APIKeyID != 0 && req.APIKeyID != claim.APIKeyID {
		return nil, fmt.Errorf("billing: settle req.APIKeyID=%d ≠ claim=%d (claim=%d)",
			req.APIKeyID, claim.APIKeyID, req.ClaimID)
	}
	if req.UserID != 0 && claim.UserID != 0 && req.UserID != claim.UserID {
		return nil, fmt.Errorf("billing: settle req.UserID=%d ≠ claim=%d (claim=%d)",
			req.UserID, claim.UserID, req.ClaimID)
	}
	// AttemptSeq 不一致时直接 reject 会卡 re-reserve 后
	// 的 settle (ReReserveAbortedClaim 把 claim.AttemptSeq 加 1, caller 仍传
	// req.AttemptSeq=1 hardcoded → reject + slot 泄漏)。 AttemptSeq 仅顺序计数,
	// 不是跨租户防御列; 改用 claim.AttemptSeq 作为权威值, 不再硬 mismatch reject。

	actualCost := req.ActualCost
	if actualCost.IsZero() && !req.Draft.ActualCost.IsZero() {
		actualCost = req.Draft.ActualCost
	}
	attempt := AttemptFromSettleRequest(req)
	actualCost = CostForAttempt(actualCost, attempt)
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}

	// claim 行经 Tx2 锁定, 其 APIKeyID / UserID / AttemptSeq
	// 是权威值。 req 可能带 stale 值 (e.g. re-reserve 后 caller 仍传 AttemptSeq=1),
	// 之前 coalesce 偏好 req → usage_record 写错 attempt 序号。 直接用 claim 列。
	usageParams := dbbilling.InsertUsageRecordParams{
		TenantID:               claim.TenantID,
		ClaimID:                claim.ID,
		APIKeyID:               claim.APIKeyID,
		UserID:                 claim.UserID,
		ProviderAccountID:      &providerAccountID,
		SettlementSource:       SettlementSourceProviderUpstream,
		AcquisitionToken:       pgUUID(req.AcquisitionToken),
		AttemptSeq:             claim.AttemptSeq,
		TokensInput:            clampInt32Tokens(int64(req.Draft.TokensInput)),
		TokensOutput:           int32(outputTokensForAttempt(req.Draft, attempt)),
		CacheCreationTokens:    clampInt32Tokens(int64(req.Draft.CacheCreationTokens)),
		CacheReadTokens:        clampInt32Tokens(int64(req.Draft.CacheReadTokens)),
		CacheCreation5mTokens:  clampInt32Tokens(int64(req.Draft.CacheCreation5mTokens)),
		CacheCreation1hTokens:  clampInt32Tokens(int64(req.Draft.CacheCreation1hTokens)),
		ImageOutputTokens:      0,
		ActualCost:             actualCost,
		CostSnapshot:           nullableString(req.Draft.CostSnapshot),
		InputCost:              decimal.Zero,
		OutputCost:             decimal.Zero,
		CacheCreationCost:      CostForAttempt(req.Draft.CacheCreationCost, attempt),
		CacheReadCost:          CostForAttempt(req.Draft.CacheReadCost, attempt),
		ImageOutputCost:        decimal.Zero,
		EndClass:               normalizeEndClass(req.Draft.EndClass, req.Stream),
		UsageSource:            normalizeUsageSource(req.Draft.UsageSource),
		ConfidenceScore:        numericFromFloat(req.Draft.ConfidenceScore),
		PendingReconciliation:  req.Draft.PendingReconciliation,
		StreamState:            attempt.State.DBValue(),
		DeliveredTokenCount:    attempt.DeliveredTokenCount,
		StreamTerminatedReason: nullableString(attempt.StreamTerminatedReason),
		DrainOutcome:           normalizeDrainOutcome(req.Draft.DrainOutcome),
		RoutingReason:          jsonOrEmptyObject(req.Draft.RoutingReason),
		ProtocolLoss:           jsonOrEmptyArray(req.ProtocolLoss),
		RequestedAt:            pgTimestamp(requestedAt),
		RequestedModel:         coalesceString(req.RequestedModel, claim.RequestedModel),
		UpstreamModel:          nullableString(req.UpstreamModel),
		Stream:                 req.Stream,
		SnapshotVersion:        nullableString(req.SnapshotVersion),
		ImageCount:             req.Draft.ImageCount,
		ImageSize:              req.Draft.ImageSize,
		ImageSizeBreakdown:     req.Draft.ImageSizeBreakdown,
		IPAddress:              req.Draft.IPAddress,
		UserAgent:              req.Draft.UserAgent,
	}

	endClass := normalizeEndClass(req.Draft.EndClass, req.Stream)
	usageSource := normalizeUsageSource(req.Draft.UsageSource)
	auditRequestID := strings.TrimSpace(req.AuditRequestID)
	billingEventParams := dbbilling.InsertBillingEventParams{
		TenantID:               claim.TenantID,
		ClaimID:                nullableInt64(claim.ID),
		EventType:              "claim_committed",
		ActualCost:             actualCost,
		ActualCostSigned:       actualCost,
		EndClass:               &endClass,
		UsageSource:            &usageSource,
		StreamState:            attempt.State.DBValue(),
		DeliveredTokenCount:    attempt.DeliveredTokenCount,
		StreamTerminatedReason: nullableString(attempt.StreamTerminatedReason),
		Fingerprint:            coalesceString(req.Fingerprint, claim.RequestFingerprint),
		AuditRequestID:         nullableString(auditRequestID),
	}
	billingEvent, err := qtx.InsertBillingEvent(ctx, billingEventParams)
	if err != nil {
		return nil, fmt.Errorf("billing: insert billing event: %w", err)
	}
	if err := s.enqueueBillingEventReplica(ctx, tx, billingEvent, billingEventParams); err != nil {
		return nil, err
	}
	if err := s.insertUsageRecordOrDLQ(ctx, tx, qtx, usageParams, "usage_record_insert_failed"); err != nil {
		return nil, err
	}

	outboxEvents := 0
	if req.EmitSchedulerOutbox {
		if _, err := qtx.InsertSchedulerOutboxRow(ctx, dbbilling.InsertSchedulerOutboxRowParams{
			TenantID:          claim.TenantID,
			EventType:         "account_quota_changed",
			ProviderAccountID: &providerAccountID,
			Payload:           []byte("{}"),
		}); err != nil {
			return nil, fmt.Errorf("billing: insert scheduler outbox row: %w", err)
		}
		outboxEvents = 1
	}

	releaseReason := "settled_committed"
	released, err := qtx.ReleaseSlotAndDecrementInFlight(ctx, dbbilling.ReleaseSlotAndDecrementInFlightParams{
		AcquisitionToken: req.AcquisitionToken,
		ReleaseReason:    &releaseReason,
	})
	if err != nil {
		return nil, fmt.Errorf("billing: release slot + decrement in-flight count: %w", err)
	}
	if released == 0 {
		return nil, ErrSlotReleaseMissed
	}
	rows, err := qtx.UpdateClaimCommitted(ctx, dbbilling.UpdateClaimCommittedParams{
		ID: claim.ID,
		ActualCost: decimal.NullDecimal{
			Decimal: actualCost,
			Valid:   true,
		},
		TenantID: claim.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("billing: update claim committed: %w", err)
	}
	if rows == 0 {
		return nil, ErrClaimNotReserving
	}
	snap, err := Capture(ctx, tx, claim.ID, actualCost)
	if err != nil {
		return nil, fmt.Errorf("billing: capture hold: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: commit Tx2: %w", err)
	}

	cachemetrics.ObserveStreamState(attempt.State.String(), req.Provider, coalesceString(req.UpstreamModel, req.RequestedModel))
	return &SettleResult{
		NewUserBalance:       snap.Balance,
		OutboxEventsEnqueued: outboxEvents,
		TenantID:             claim.TenantID,
		UserID:               claim.UserID,
		BillingEventID:       billingEvent.ID,
	}, nil
}

func (s *DefaultSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	if s == nil || s.pool == nil {
		return ErrPoolNotConfigured
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("billing: begin abort Tx2: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fingerprint string
	var status string
	var acquisitionToken pgtype.UUID
	var apiKeyID, userID int64
	var providerAccountID *int64
	var attemptSeq int32
	var requestedModel string
	if err := tx.QueryRow(ctx,
		`SELECT request_fingerprint, status, acquisition_token, api_key_id, user_id,
		        provider_account_id, attempt_seq, requested_model
		 FROM billing_ledger_claims WHERE id=$1 AND tenant_id=$2 FOR UPDATE`,
		claimID, tenantID,
	).Scan(&fingerprint, &status, &acquisitionToken, &apiKeyID, &userID,
		&providerAccountID, &attemptSeq, &requestedModel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClaimNotReserving
		}
		return fmt.Errorf("billing: get claim for abort: %w", err)
	}
	if status != "reserving" {
		return ErrClaimNotReserving
	}

	qtx := s.q.WithTx(tx)
	rows, err := qtx.UpdateClaimAbortedWithReason(ctx, dbbilling.UpdateClaimAbortedWithReasonParams{
		ID:            claimID,
		TenantID:      tenantID,
		AbortedReason: nullableString(reason),
	})
	if err != nil {
		return fmt.Errorf("billing: update claim aborted: %w", err)
	}
	if rows == 0 {
		return ErrClaimNotReserving
	}
	if _, err := Release(ctx, tx, claimID); err != nil {
		return fmt.Errorf("billing: release hold: %w", err)
	}
	abortEndClass := "unknown_termination"
	abortUsageSource := string(gateway.UsageSourceInferred)
	abortAttempt := Attempt{State: StreamStateFailed, StreamTerminatedReason: normalizeTerminatedReason(reason)}
	auditRequestID = strings.TrimSpace(auditRequestID)
	abortEventParams := dbbilling.InsertBillingEventParams{
		TenantID:               tenantID,
		ClaimID:                nullableInt64(claimID),
		EventType:              "claim_aborted",
		ActualCost:             decimal.Zero,
		ActualCostSigned:       decimal.Zero,
		EndClass:               &abortEndClass,
		UsageSource:            &abortUsageSource,
		StreamState:            abortAttempt.State.DBValue(),
		DeliveredTokenCount:    0,
		StreamTerminatedReason: nullableString(abortAttempt.StreamTerminatedReason),
		Fingerprint:            fingerprint,
		AuditRequestID:         nullableString(auditRequestID),
	}
	abortEvent, err := qtx.InsertBillingEvent(ctx, abortEventParams)
	if err != nil {
		return fmt.Errorf("billing: insert abort billing event: %w", err)
	}
	if err := s.enqueueBillingEventReplica(ctx, tx, abortEvent, abortEventParams); err != nil {
		return err
	}

	// Audit-grade Usage Record on abort path: every Tx2 commit,
	// including aborted final disposition, produces a usage_record so aborts
	// remain queryable consistently with committed requests.
	// Only writable when Pool wrote back provider_account_id (NOT NULL on
	// usage_records); pre-acquire aborts skip the record.
	if providerAccountID != nil && acquisitionToken.Valid {
		var tokAbort uuid.UUID
		copy(tokAbort[:], acquisitionToken.Bytes[:])
		usageParams := dbbilling.InsertUsageRecordParams{
			TenantID:               tenantID,
			ClaimID:                claimID,
			APIKeyID:               apiKeyID,
			UserID:                 userID,
			ProviderAccountID:      providerAccountID,
			SettlementSource:       SettlementSourceProviderUpstream,
			AcquisitionToken:       pgUUID(tokAbort),
			AttemptSeq:             attemptSeq,
			TokensInput:            clampInt32Tokens(observedInputTokens),
			ActualCost:             decimal.Zero,
			InputCost:              decimal.Zero,
			OutputCost:             decimal.Zero,
			CacheCreationCost:      decimal.Zero,
			CacheReadCost:          decimal.Zero,
			ImageOutputCost:        decimal.Zero,
			EndClass:               abortEndClass,
			UsageSource:            abortUsageSource,
			PendingReconciliation:  false,
			StreamState:            abortAttempt.State.DBValue(),
			DeliveredTokenCount:    0,
			StreamTerminatedReason: nullableString(abortAttempt.StreamTerminatedReason),
			RoutingReason:          []byte("{}"),
			ProtocolLoss:           jsonOrEmptyArray(protocolLoss),
			RequestedAt:            pgTimestamp(time.Now().UTC()),
			RequestedModel:         requestedModel,
		}
		if err := s.insertUsageRecordOrDLQ(ctx, tx, qtx, usageParams, "abort_usage_record_insert_failed"); err != nil {
			return err
		}
	}

	// Idempotently release pool slot + decrement in_flight if Pool wrote back
	// the acquisition_token (Pattern B). When the claim aborts BEFORE Pool
	// acquired (token NULL), there's nothing to release.
	if acquisitionToken.Valid {
		releaseReason := "settled_aborted"
		var tokUUID uuid.UUID
		copy(tokUUID[:], acquisitionToken.Bytes[:])
		released, err := qtx.ReleaseSlotAndDecrementInFlight(ctx, dbbilling.ReleaseSlotAndDecrementInFlightParams{
			AcquisitionToken: tokUUID,
			ReleaseReason:    &releaseReason,
		})
		if err != nil {
			return fmt.Errorf("billing: release slot on abort: %w", err)
		}
		if released == 0 {
			return ErrSlotReleaseMissed
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit abort Tx2: %w", err)
	}
	return nil
}

// CommitCacheHit 见 Settler 接口注释。 用于 L2 cache 命中且 claim 已 reserve
// 但尚未 acquire pool account 的场景: 请求成功返回缓存响应体, 计费 0, claim
// 必须以 committed 终结而非 aborted。
//
// 与 Settle 区别: 无 acquisition_token、无 pool slot、无 provider account
// → 不调 ReleaseSlotAndDecrementInFlight。 但仍写一条 usage_records 行
// (settlement_source=response_cache_l2, provider_account_id / acquisition_token
// 为 NULL — migration 0043 起 schema 受约束可空), 使 receipt / admin 用量 /
// obs / 退款 等下游与正常请求一致地消费缓存命中事实。req 复用 SettleRequest,
// 仅 TenantID/ClaimID/AuditRequestID/Draft/RequestedModel/UpstreamModel/
// Fingerprint/SnapshotVersion/RequestedAt 字段被用到。
func (s *DefaultSettler) CommitCacheHit(ctx context.Context, req SettleRequest) error {
	if s == nil || s.pool == nil {
		return ErrPoolNotConfigured
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("billing: begin cache-hit commit Tx2: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fingerprint, status, claimRequestedModel string
	var apiKeyID, userID int64
	var attemptSeq int32
	if err := tx.QueryRow(ctx,
		`SELECT request_fingerprint, status, api_key_id, user_id, attempt_seq, requested_model
		 FROM billing_ledger_claims WHERE id=$1 AND tenant_id=$2 FOR UPDATE`,
		req.ClaimID, req.TenantID,
	).Scan(&fingerprint, &status, &apiKeyID, &userID, &attemptSeq, &claimRequestedModel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClaimNotReserving
		}
		return fmt.Errorf("billing: get claim for cache-hit commit: %w", err)
	}
	if status != "reserving" {
		return ErrClaimNotReserving
	}

	qtx := s.q.WithTx(tx)
	rows, err := qtx.UpdateClaimCommitted(ctx, dbbilling.UpdateClaimCommittedParams{
		ID:         req.ClaimID,
		ActualCost: decimal.NullDecimal{Decimal: decimal.Zero, Valid: true},
		TenantID:   req.TenantID,
	})
	if err != nil {
		return fmt.Errorf("billing: update claim committed (cache hit): %w", err)
	}
	if rows == 0 {
		return ErrClaimNotReserving
	}
	if _, err := Capture(ctx, tx, req.ClaimID, decimal.Zero); err != nil {
		return fmt.Errorf("billing: capture hold for cache hit: %w", err)
	}

	// 非流式成功结清: stream_state partial (与 AccountID!=0 的 cache-hit Settle
	// 路径经 nonStreamingUsageDraft + AttemptFromGatewayDraft(stream=false)
	// 得到的状态一致), cost 0。
	endClass := normalizeEndClass(req.Draft.EndClass, false)
	usageSource := normalizeUsageSource(req.Draft.UsageSource)
	attempt := AttemptFromGatewayDraft(false, req.Draft)
	auditRequestID := strings.TrimSpace(req.AuditRequestID)
	eventFingerprint := coalesceString(req.Fingerprint, fingerprint)
	eventParams := dbbilling.InsertBillingEventParams{
		TenantID:            req.TenantID,
		ClaimID:             nullableInt64(req.ClaimID),
		EventType:           "claim_committed",
		ActualCost:          decimal.Zero,
		ActualCostSigned:    decimal.Zero,
		EndClass:            &endClass,
		UsageSource:         &usageSource,
		StreamState:         attempt.State.DBValue(),
		DeliveredTokenCount: attempt.DeliveredTokenCount,
		Fingerprint:         eventFingerprint,
		AuditRequestID:      nullableString(auditRequestID),
	}
	event, err := qtx.InsertBillingEvent(ctx, eventParams)
	if err != nil {
		return fmt.Errorf("billing: insert cache-hit billing event: %w", err)
	}
	if err := s.enqueueBillingEventReplica(ctx, tx, event, eventParams); err != nil {
		return err
	}

	// provider-less usage_records 行: 无上游账号 / acquisition_token,
	// settlement_source=response_cache_l2 (migration 0043 CHECK 允许)。
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now().UTC()
	}
	usageParams := dbbilling.InsertUsageRecordParams{
		TenantID:               req.TenantID,
		ClaimID:                req.ClaimID,
		APIKeyID:               apiKeyID,
		UserID:                 userID,
		ProviderAccountID:      nil,
		AcquisitionToken:       pgtype.UUID{},
		SettlementSource:       SettlementSourceResponseCacheL2,
		AttemptSeq:             attemptSeq,
		TokensInput:            clampInt32Tokens(int64(req.Draft.TokensInput)),
		TokensOutput:           int32(outputTokensForAttempt(req.Draft, attempt)),
		CacheCreationTokens:    clampInt32Tokens(int64(req.Draft.CacheCreationTokens)),
		CacheReadTokens:        clampInt32Tokens(int64(req.Draft.CacheReadTokens)),
		CacheCreation5mTokens:  clampInt32Tokens(int64(req.Draft.CacheCreation5mTokens)),
		CacheCreation1hTokens:  clampInt32Tokens(int64(req.Draft.CacheCreation1hTokens)),
		ActualCost:             decimal.Zero,
		CostSnapshot:           nullableString(req.Draft.CostSnapshot),
		InputCost:              decimal.Zero,
		OutputCost:             decimal.Zero,
		CacheCreationCost:      decimal.Zero,
		CacheReadCost:          decimal.Zero,
		ImageOutputCost:        decimal.Zero,
		EndClass:               endClass,
		UsageSource:            usageSource,
		ConfidenceScore:        numericFromFloat(req.Draft.ConfidenceScore),
		PendingReconciliation:  false,
		StreamState:            attempt.State.DBValue(),
		DeliveredTokenCount:    attempt.DeliveredTokenCount,
		StreamTerminatedReason: nullableString(attempt.StreamTerminatedReason),
		RoutingReason:          jsonOrEmptyObject(req.Draft.RoutingReason),
		ProtocolLoss:           jsonOrEmptyArray(req.ProtocolLoss),
		RequestedAt:            pgTimestamp(requestedAt),
		RequestedModel:         coalesceString(req.RequestedModel, claimRequestedModel),
		UpstreamModel:          nullableString(req.UpstreamModel),
		Stream:                 false,
		SnapshotVersion:        nullableString(req.SnapshotVersion),
		ImageCount:             req.Draft.ImageCount,
		ImageSize:              req.Draft.ImageSize,
		ImageSizeBreakdown:     req.Draft.ImageSizeBreakdown,
		IPAddress:              req.Draft.IPAddress,
		UserAgent:              req.Draft.UserAgent,
	}
	if err := s.insertUsageRecordOrDLQ(ctx, tx, qtx, usageParams, "cache_hit_usage_record_insert_failed"); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit cache-hit Tx2: %w", err)
	}
	return nil
}

func (s *DefaultSettler) Refund(ctx context.Context, req RefundRequest) (*RefundResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrPoolNotConfigured
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("billing: begin refund Tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := s.RefundInTx(ctx, tx, req)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("billing: commit refund Tx: %w", err)
	}
	return res, nil
}

func (s *DefaultSettler) RefundInTx(ctx context.Context, tx pgx.Tx, req RefundRequest) (*RefundResult, error) {
	if s == nil || s.q == nil {
		return nil, ErrPoolNotConfigured
	}
	if tx == nil {
		return nil, fmt.Errorf("billing: refund tx required")
	}
	if req.TenantID <= 0 || req.ClaimID <= 0 || req.AmountMicroUSD < 0 {
		return nil, fmt.Errorf("billing: invalid refund request")
	}
	auditRequestID := strings.TrimSpace(req.AuditRequestID)
	if auditRequestID == "" {
		auditRequestID = fmt.Sprintf("audit-refund-%d", req.ClaimID)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "refund"
	}

	// 先锁 claim 行(FOR UPDATE),再查幂等:原顺序(先幂等 SELECT 后锁)下两个并发退款都先过
	// 幂等检查(都看不到对方未提交的插入)再都插入 → 双退款。锁前置后,第二个退款必等第一个
	// 提交、看到其 reconciliation 事件 → 正确幂等。
	var (
		fingerprint string
		status      string
		actualCost  decimal.Decimal
		userID      int64
	)
	if err := tx.QueryRow(ctx, `
SELECT request_fingerprint, status, actual_cost, user_id
FROM billing_ledger_claims
WHERE tenant_id = $1 AND id = $2
FOR UPDATE`,
		req.TenantID, req.ClaimID,
	).Scan(&fingerprint, &status, &actualCost, &userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrClaimNotReserving
		}
		return nil, fmt.Errorf("billing: get claim for refund: %w", err)
	}

	// 幂等检查(已持 claim 锁,串行化):同 audit_request_id 的退款已记则返回**存储的**退款额
	// (actual_cost_signed 取反 → micros),而非调用方传入的 req.AmountMicroUSD —— 否则不同金额
	// 重放会返回错误的假幂等结果。actual_cost_signed < 0 限定只匹配退款事件(与下方 SUM 一致)。
	var (
		existingID         int64
		existingSignedCost decimal.Decimal
	)
	if err := tx.QueryRow(ctx, `
SELECT id, actual_cost_signed
FROM billing_events
WHERE tenant_id = $1
  AND claim_id = $2
  AND event_type = 'reconciliation_appended'
  AND audit_request_id = $3
  AND actual_cost_signed < 0
ORDER BY id ASC
LIMIT 1`,
		req.TenantID, req.ClaimID, auditRequestID,
	).Scan(&existingID, &existingSignedCost); err == nil {
		storedMicros, cerr := costUSDToMicros(existingSignedCost.Neg())
		if cerr != nil {
			return nil, cerr
		}
		return &RefundResult{
			RefundMicroUSD: storedMicros,
			BillingEventID: existingID,
			AdjustmentRef:  billingAdjustmentRef(existingID),
			Idempotent:     true,
		}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("billing: lookup existing refund: %w", err)
	}

	if status != "committed" {
		return nil, ErrClaimNotReserving
	}

	refundMicros := req.AmountMicroUSD
	// amount=0 走 skipped 短路, 不查 SUM 节省一次 RT。
	if refundMicros == 0 {
		return zeroRefundResult(), nil
	}
	originalMicros, err := costUSDToMicros(actualCost)
	if err != nil {
		return nil, err
	}
	// 累计 refund 上限: 已发的 reconciliation_appended negative events 之和加
	// 本次请求, 总额不得超 originalMicros。这样避免多 refund 不
	// 同 audit_request_id 各自单独 cap 到 original 导致总额超退。
	var alreadyRefundedMicros int64
	// 累计 refund 上限: billing_events 用 actual_cost_signed (numeric USD, 退款
	// 时为负); 把负值取反 × 1_000_000 转 micros 求和。
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(ROUND(-actual_cost_signed * 1000000)), 0)::bigint
FROM billing_events
WHERE tenant_id = $1 AND claim_id = $2 AND event_type = 'reconciliation_appended'
  AND actual_cost_signed < 0`,
		req.TenantID, req.ClaimID,
	).Scan(&alreadyRefundedMicros); err != nil {
		return nil, fmt.Errorf("billing: sum prior reconciliation refunds: %w", err)
	}
	remainingMicros := originalMicros - alreadyRefundedMicros
	if remainingMicros < 0 {
		remainingMicros = 0
	}
	if refundMicros > remainingMicros {
		refundMicros = remainingMicros
	}
	if refundMicros == 0 {
		return zeroRefundResult(), nil
	}

	refundUSD := decimal.NewFromInt(refundMicros).Div(decimal.NewFromInt(1_000_000))
	signedRefund := refundUSD.Neg()
	refundEndClass := reason
	refundUsageSource := "audit_mismatch"
	refundEventParams := dbbilling.InsertBillingEventParams{
		TenantID:         req.TenantID,
		ClaimID:          nullableInt64(req.ClaimID),
		EventType:        "reconciliation_appended",
		ActualCost:       decimal.Zero,
		ActualCostSigned: signedRefund,
		EndClass:         &refundEndClass,
		UsageSource:      &refundUsageSource,
		StreamState:      StreamStatePartial.DBValue(),
		Fingerprint:      fingerprint,
		AuditRequestID:   nullableString(auditRequestID),
	}
	row, err := s.q.WithTx(tx).InsertBillingEvent(ctx, refundEventParams)
	if err != nil {
		return nil, fmt.Errorf("billing: insert refund event: %w", err)
	}
	// opt-in 余额强制:仅对已 provision user_balances 行的
	// 用户回补退款。RowsAffected()==0 = 该用户未纳入余额强制(无行)→ 退款
	// reconciliation 事件已记、无余额行可补,是合法 no-op(非静默假成功:此处显式
	// 承认 RowsAffected,回应)。已 provision 用户(1 行)正常回补。
	creditTag, err := tx.Exec(ctx,
		`UPDATE user_balances
		 SET balance = balance + $1, version = version + 1
		 WHERE tenant_id = $2 AND user_id = $3`,
		refundUSD,
		req.TenantID,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("billing: credit refund to user balance: %w", err)
	}
	refundBalanceCredited := creditTag.RowsAffected() > 0
	if err := s.enqueueBillingEventReplica(ctx, tx, row, refundEventParams); err != nil {
		return nil, err
	}
	return &RefundResult{
		RefundMicroUSD:  refundMicros,
		BillingEventID:  row.ID,
		AdjustmentRef:   billingAdjustmentRef(row.ID),
		BalanceCredited: refundBalanceCredited,
	}, nil
}

func zeroRefundResult() *RefundResult {
	return &RefundResult{
		RefundMicroUSD: 0,
		AdjustmentRef:  RefundSkippedAmountZeroRef,
	}
}

func costUSDToMicros(cost decimal.Decimal) (int64, error) {
	micros := cost.Mul(decimal.NewFromInt(1_000_000)).Round(0)
	if micros.IsNegative() {
		return 0, nil
	}
	if micros.GreaterThan(maxCostMicroUSDDecimal) {
		return 0, ErrCostOverflow
	}
	return micros.IntPart(), nil
}

func billingAdjustmentRef(eventID int64) string {
	if eventID <= 0 {
		return "billing_event:0"
	}
	return fmt.Sprintf("billing_event:%d", eventID)
}

func (s *DefaultSettler) classifySettleNoRows(ctx context.Context, tx pgx.Tx, req SettleRequest) error {
	var token pgtype.UUID
	var status string
	if err := tx.QueryRow(ctx,
		`SELECT acquisition_token, status FROM billing_ledger_claims WHERE id=$1 AND tenant_id=$2 FOR UPDATE`,
		req.ClaimID, req.TenantID,
	).Scan(&token, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrClaimNotReserving
		}
		return fmt.Errorf("billing: classify claim settle failure: %w", err)
	}
	if status != "reserving" {
		return ErrClaimNotReserving
	}
	if !token.Valid || token.Bytes != req.AcquisitionToken {
		return ErrAcquisitionTokenMismatch
	}
	return ErrClaimNotReserving
}

func (s *DefaultSettler) insertUsageRecordOrDLQ(ctx context.Context, tx pgx.Tx, qtx *dbbilling.Queries, params dbbilling.InsertUsageRecordParams, failurePrefix string) error {
	if _, err := tx.Exec(ctx, "SAVEPOINT huakai_usage_record_insert"); err != nil {
		return fmt.Errorf("billing: create usage savepoint: %w", err)
	}
	if _, err := qtx.InsertUsageRecord(ctx, params); err == nil {
		if _, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT huakai_usage_record_insert"); releaseErr != nil {
			return fmt.Errorf("billing: release usage savepoint: %w", releaseErr)
		}
		return nil
	} else {
		if ctx.Err() != nil {
			return fmt.Errorf("billing: insert usage record: %w", err)
		}
		usageErr := err
		if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT huakai_usage_record_insert"); rollbackErr != nil {
			return fmt.Errorf("billing: rollback usage savepoint after %v: %w", usageErr, rollbackErr)
		}
		if _, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT huakai_usage_record_insert"); releaseErr != nil {
			return fmt.Errorf("billing: release rolled-back usage savepoint: %w", releaseErr)
		}
		if s.dlqStore == nil {
			return fmt.Errorf("billing: insert usage record: %w", usageErr)
		}
		payload, marshalErr := marshalUsageRecordPayload(params)
		if marshalErr != nil {
			return fmt.Errorf("billing: marshal usage DLQ payload: %w", marshalErr)
		}
		_, enqueueErr := s.dlqStore.EnqueueTx(ctx, tx, dlq.Event{
			TenantID:       params.TenantID,
			ClaimID:        params.ClaimID,
			EventKind:      dlq.EventKindUsageRecord,
			Lane:           dlq.LaneHigh,
			Payload:        payload,
			FailureReason:  failurePrefix + ": " + usageErr.Error(),
			IdempotencyKey: fmt.Sprintf("usage_record:%d:%d", params.TenantID, params.ClaimID),
			SourceTable:    "usage_records",
			SourceID:       params.ClaimID,
		})
		if enqueueErr != nil {
			return fmt.Errorf("billing: enqueue usage DLQ after insert failure: %w", enqueueErr)
		}
		return nil
	}
}

func (s *DefaultSettler) enqueueBillingEventReplica(ctx context.Context, tx pgx.Tx, row dbbilling.InsertBillingEventRow, params dbbilling.InsertBillingEventParams) error {
	if s.replicaTarget == "" {
		return nil
	}
	if s.dlqStore == nil {
		return fmt.Errorf("billing: replica intent configured without DLQ store")
	}
	claimID := int64Value(params.ClaimID)
	payload, err := json.Marshal(dlq.BillingEventReplicaPayload{
		BillingEventID:         row.ID,
		TenantID:               params.TenantID,
		ClaimID:                claimID,
		EventType:              params.EventType,
		ActualCost:             params.ActualCost.StringFixed(8),
		ActualCostSigned:       params.ActualCostSigned.StringFixed(8),
		EndClass:               params.EndClass,
		UsageSource:            params.UsageSource,
		StreamState:            params.StreamState,
		DeliveredTokenCount:    params.DeliveredTokenCount,
		StreamTerminatedReason: params.StreamTerminatedReason,
		Fingerprint:            params.Fingerprint,
		AuditRequestID:         params.AuditRequestID,
		OccurredAt:             timestampString(row.OccurredAt),
	})
	if err != nil {
		return fmt.Errorf("billing: marshal billing replica payload: %w", err)
	}
	_, err = s.dlqStore.EnqueueTx(ctx, tx, dlq.Event{
		TenantID:       params.TenantID,
		ClaimID:        claimID,
		EventKind:      dlq.EventKindBillingEventReplica,
		Lane:           dlq.LaneHigh,
		Payload:        payload,
		FailureReason:  "replica_pending",
		ReplicaTarget:  s.replicaTarget,
		ReplicaStatus:  dlq.ReplicaStatusPending,
		IdempotencyKey: fmt.Sprintf("billing_event_replica:%d:%d", params.TenantID, row.ID),
		SourceTable:    "billing_events",
		SourceID:       row.ID,
	})
	if err != nil {
		return fmt.Errorf("billing: enqueue billing replica intent: %w", err)
	}
	return nil
}

func marshalUsageRecordPayload(params dbbilling.InsertUsageRecordParams) (json.RawMessage, error) {
	payload := dlq.UsageRecordPayload{
		TenantID:               params.TenantID,
		ClaimID:                params.ClaimID,
		APIKeyID:               params.APIKeyID,
		UserID:                 params.UserID,
		ProviderAccountID:      params.ProviderAccountID,
		SettlementSource:       params.SettlementSource,
		AcquisitionToken:       pgUUIDString(params.AcquisitionToken),
		AttemptSeq:             params.AttemptSeq,
		TokensInput:            params.TokensInput,
		TokensOutput:           params.TokensOutput,
		CacheCreationTokens:    params.CacheCreationTokens,
		CacheReadTokens:        params.CacheReadTokens,
		CacheCreation5mTokens:  params.CacheCreation5mTokens,
		CacheCreation1hTokens:  params.CacheCreation1hTokens,
		ImageOutputTokens:      params.ImageOutputTokens,
		ActualCost:             params.ActualCost.StringFixed(8),
		CostSnapshot:           params.CostSnapshot,
		InputCost:              params.InputCost.StringFixed(8),
		OutputCost:             params.OutputCost.StringFixed(8),
		CacheCreationCost:      params.CacheCreationCost.StringFixed(8),
		CacheReadCost:          params.CacheReadCost.StringFixed(8),
		ImageOutputCost:        params.ImageOutputCost.StringFixed(8),
		EndClass:               params.EndClass,
		UsageSource:            params.UsageSource,
		ConfidenceScore:        numericString(params.ConfidenceScore),
		PendingReconciliation:  params.PendingReconciliation,
		StreamState:            params.StreamState,
		DeliveredTokenCount:    params.DeliveredTokenCount,
		StreamTerminatedReason: params.StreamTerminatedReason,
		DrainOutcome:           params.DrainOutcome,
		RoutingReason:          json.RawMessage(jsonOrEmptyObject(params.RoutingReason)),
		ProtocolLoss:           json.RawMessage(jsonOrEmptyArray(params.ProtocolLoss)),
		RequestedAt:            timestampString(params.RequestedAt),
		UpstreamRequestAt:      timestampStringPtr(params.UpstreamRequestAt),
		FirstByteAt:            timestampStringPtr(params.FirstByteAt),
		FirstEventAt:           timestampStringPtr(params.FirstEventAt),
		LastEventAt:            timestampStringPtr(params.LastEventAt),
		RequestedModel:         params.RequestedModel,
		UpstreamModel:          params.UpstreamModel,
		Stream:                 params.Stream,
		SnapshotVersion:        params.SnapshotVersion,
		ImageCount:             params.ImageCount,
		ImageSize:              params.ImageSize,
		ImageSizeBreakdown:     json.RawMessage(params.ImageSizeBreakdown),
		IPAddress:              params.IPAddress,
		UserAgent:              params.UserAgent,
	}
	raw, err := json.Marshal(payload)
	return json.RawMessage(raw), err
}

func pgUUID(v uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: v, Valid: true}
}

func pgUUIDString(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	u := uuid.UUID(v.Bytes)
	return u.String()
}

func pgTimestamp(v time.Time) pgtype.Timestamptz {
	if v.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: v.UTC(), Valid: true}
}

func numericFromFloat(v *float64) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	n := pgtype.Numeric{}
	_ = n.Scan(decimal.NewFromFloat(*v).String())
	return n
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func coalesceString(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func int64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func outputTokensForAttempt(draft gateway.UsageRecordDraft, _ Attempt) int64 {
	// tokens_output 只反映真实输出 token。不再用 DeliveredTokenCount(SSE 帧/chunk 投递计数,非 token)
	// 回退充当:缺真实 usage 时(TokensOutput==0)帧数会把 tokens_output 灌成帧计数,既污染 reconcile
	// 行识别(零真实输出信号被帧数掩盖,R4-P2),又是潜在超收。帧/chunk 投递量另由 delivered_token_count
	// 列承载,二者不再混用(C1)。无真实输出 token → 记 0(正向估算成本由后续 C3 在 usage_source=estimated
	// 时单独注入)。参考项目对照(带 repo@sha:file:line)见 docs/process/plans/2026-05-29-money-path-worker-claude.md §9。
	output := int64(draft.TokensOutput)
	if output < 0 {
		return 0
	}
	if output > int64(^uint32(0)>>1) {
		return int64(^uint32(0) >> 1)
	}
	return output
}

// clampInt32Tokens 把 token 桶压到 [0, MaxInt32]。
//
// 背景:usage_records 的 token 列在 0002 migration 是 PostgreSQL integer
// (int32 范围),Go int 在 64 位平台是 int64,直接 cast 在异常上游报 > 2.1B
// token 时会变负或被截断,后续 INSERT 报 "integer out of range" 让本次
// settle 丢账;负值则写入无意义数据污染审计/对账。clamp 是务实折中:
// 极端 outlier 被压到 MaxInt32 保账面一致,代价是失去原始精度 — 接受。
//
// outputTokensForAttempt 已经做同样 clamp;这个 helper 把 input / cache 桶
// 也补上,关掉 settler 主路径 / Abort / L2 cache-hit 三条 settlement
// path 共 7 处直接 int32 cast 漏洞。
func clampInt32Tokens(v int64) int32 {
	if v < 0 {
		return 0
	}
	const maxInt32 = int64(^uint32(0) >> 1)
	if v > maxInt32 {
		return int32(maxInt32)
	}
	return int32(v)
}

func jsonOrEmptyObject(v []byte) []byte {
	if len(v) == 0 {
		return []byte("{}")
	}
	return v
}

func jsonOrEmptyArray(v []byte) []byte {
	if len(v) == 0 {
		return []byte("[]")
	}
	return v
}

func timestampString(v pgtype.Timestamptz) string {
	if !v.Valid {
		return ""
	}
	return v.Time.UTC().Format(time.RFC3339Nano)
}

func timestampStringPtr(v pgtype.Timestamptz) *string {
	if !v.Valid {
		return nil
	}
	s := timestampString(v)
	return &s
}

func numericString(v pgtype.Numeric) *string {
	if !v.Valid {
		return nil
	}
	raw, err := v.Value()
	if err != nil || raw == nil {
		return nil
	}
	s := fmt.Sprint(raw)
	return &s
}

func normalizeUsageSource(v gateway.UsageSource) string {
	if v == "" {
		return "reported"
	}
	return string(v)
}

func normalizeEndClass(v gateway.StreamEndClass, stream bool) string {
	switch v {
	case "":
		if stream {
			return "unknown_termination"
		}
		return "non_streaming"
	case gateway.UpstreamEOFNoTerminal:
		return "stream_end_no_terminal_marker"
	case gateway.ResponseEventTooLarge:
		return "event_size_exceeded"
	case gateway.OrchestratorCancel:
		return "orchestrator_cancelled"
	case gateway.AmbiguousUsage:
		return "usage_ambiguous"
	default:
		return string(v)
	}
}

func normalizeDrainOutcome(v gateway.DrainOutcome) *string {
	var out string
	switch v {
	case "":
		return nil
	case gateway.DrainBudgetSecondsExhausted:
		out = "max_seconds"
	case gateway.DrainBudgetBytesExhausted:
		out = "max_bytes"
	case gateway.DrainBudgetCostExhausted:
		out = "max_estimated_cost"
	case gateway.DrainNotDrained:
		return nil
	default:
		out = string(v)
	}
	return &out
}

var _ Settler = (*DefaultSettler)(nil)
