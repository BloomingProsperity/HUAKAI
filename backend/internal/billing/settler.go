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
	dbbillingrecovery "github.com/BloomingProsperity/HUAKAI/internal/db/billingrecovery"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

var (
	ErrClaimNotReserving             = errors.New("billing: claim is not reserving")
	ErrPostDeliverySettlementPending = errors.New("billing: post-delivery settlement recovery is unresolved")
	ErrAcquisitionTokenMismatch      = errors.New("billing: acquisition token mismatch")
	ErrSlotReleaseMissed             = errors.New("billing: pool_slot_acquisitions row not in 'acquired' state for token")
	ErrCostOverflow                  = errors.New("billing: cost overflow")
)

// settlement_source 判别值 (migration 0043): provider_upstream = 正常上游路径
// (provider_account_id / acquisition_token 必非空); response_cache_l2 = L2
// 缓存命中路径 (两者必为空, 无上游账号)。
const (
	SettlementSourceProviderUpstream = "provider_upstream"
	SettlementSourceResponseCacheL2  = "response_cache_l2"
)

type DefaultSettler struct {
	pool           *pgxpool.Pool
	q              *dbbilling.Queries
	abortRecoveryQ *dbbillingrecovery.Queries
	dlqStore       *dlq.Store
	replicaTarget  string
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
	s := &DefaultSettler{
		pool:           pool,
		q:              dbbilling.New(pool),
		abortRecoveryQ: dbbillingrecovery.New(pool),
		dlqStore:       dlq.NewStore(pool),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *DefaultSettler) Settle(ctx context.Context, req SettleRequest) (*SettleResult, error) {
	if s == nil || s.pool == nil {
		return nil, ErrPoolNotConfigured
	}

	var res *SettleResult
	err := retryTx2(ctx, "settle", settleTx2RetryPolicy, func(ctx context.Context) error {
		next, err := s.settleOnce(ctx, req)
		if err != nil {
			return err
		}
		res = next
		return nil
	}, nil, nil)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// settleOnce 执行一次完整 Tx2 结算事务。外层 retryTx2 只在 40001/40P01 后
// 重跑整个事务;事务内任何写入若未提交都会随 Rollback 撤销,且 status='reserving'
// 守卫保证终态 claim 不会被重复插入 usage_record、billing_event 或重复 capture。
func (s *DefaultSettler) settleOnce(ctx context.Context, req SettleRequest) (*SettleResult, error) {
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
		// TTFT/TPS 数据源:首字与流末绝对时刻(forwarder 量,零值→pgTimestamp 写 NULL 被 perf SQL 排除)。
		// 此前从不写→列恒 NULL→avg_ttft_ms/avg_tps/p95 恒 0(设施齐全但断链)。
		FirstByteAt:        pgTimestamp(req.Draft.FirstByteAt),
		LastEventAt:        pgTimestamp(req.Draft.LastEventAt),
		RequestedModel:     coalesceString(req.RequestedModel, claim.RequestedModel),
		UpstreamModel:      nullableString(req.UpstreamModel),
		Stream:             req.Stream,
		SnapshotVersion:    nullableString(req.SnapshotVersion),
		ImageCount:         req.Draft.ImageCount,
		ImageSize:          req.Draft.ImageSize,
		ImageSizeBreakdown: req.Draft.ImageSizeBreakdown,
		IPAddress:          req.Draft.IPAddress,
		UserAgent:          req.Draft.UserAgent,
		ClientTool:         nullableString(req.Draft.ClientTool),
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
		ReleaseStatus:    "released_success",
		ReleaseReason:    &releaseReason,
	})
	if err != nil {
		return nil, fmt.Errorf("billing: release slot + decrement in-flight count: %w", err)
	}
	if released == 0 {
		if err := verifyAlreadyReleasedSlot(ctx, dbbillingrecovery.New(tx), req.AcquisitionToken, slotReleaseSettle); err != nil {
			return nil, err
		}
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

	var generation abortClaimGeneration
	err := retryTx2(ctx, "abort", abortTx2RetryPolicy, func(ctx context.Context) error {
		return s.abortOnce(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss, &generation)
	}, nil, nil)
	if err == nil {
		return nil
	}
	return s.expediteAbortLeaseAfterConflict(ctx, tenantID, claimID, generation, err)
}

type abortClaimGeneration struct {
	attemptSeq int32
	observed   bool
}

// abortOnce 执行一次完整 Tx2 中止事务。外层 retryTx2 只在整事务被 Serializable
// 冲突回滚后重跑;若 claim 已不在 reserving,这里立即返回 ErrClaimNotReserving,
// 因而不会重复 Release、重复 claim_aborted 事件或重复零成本 usage_record。
func (s *DefaultSettler) abortOnce(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage, generation *abortClaimGeneration) error {
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
	// 事务后段冲突会回滚业务写，但恢复 UPDATE 仍须绑定本轮真实读到的代际。
	if generation != nil {
		generation.attemptSeq = attemptSeq
		generation.observed = true
	}
	if status != "reserving" {
		return ErrClaimNotReserving
	}
	// 候选清扫查询与本事务之间存在时间窗；在持有 claim 行锁时再次检查恢复队列，
	// 防止已交付请求刚落下未决恢复行却仍被零成本中止。所有状态未闭合的恢复行都保护 claim。
	var settlementRecoveryPending bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1
			FROM usage_record_dlq
			WHERE tenant_id=$1
			  AND claim_id=$2
			  AND event_kind='post_delivery_settlement'
			  AND status <> 'delivered'
		)`, tenantID, claimID,
	).Scan(&settlementRecoveryPending); err != nil {
		return fmt.Errorf("billing: check post-delivery settlement recovery before abort: %w", err)
	}
	if settlementRecoveryPending {
		return ErrPostDeliverySettlementPending
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

	// abort 路径上的审计级用量记录:每次 Tx2 提交(含 aborted 终态)都产出一条
	// usage_record,使 abort 与 committed 请求一样可一致地查询。
	// 仅当 Pool 已回写 provider_account_id 时才可写(usage_records 上该列 NOT NULL);
	// pre-acquire 阶段的 abort 跳过该记录。
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
			// abort 路径不持有 draft → 无客户端归因(NULL),与既有 abort
			// 用量记录"只记结构性字段"一致。
			ClientTool: nil,
		}
		if err := s.insertUsageRecordOrDLQ(ctx, tx, qtx, usageParams, "abort_usage_record_insert_failed"); err != nil {
			return err
		}
	}

	// 若 Pool 已回写 acquisition_token(Pattern B),则幂等地释放 pool slot 并递减
	// in_flight。当 claim 在 Pool acquire 之前就 abort(token 为 NULL)时,无可释放之物。
	if acquisitionToken.Valid {
		releaseReason := "settled_aborted"
		var tokUUID uuid.UUID
		copy(tokUUID[:], acquisitionToken.Bytes[:])
		released, err := qtx.ReleaseSlotAndDecrementInFlight(ctx, dbbilling.ReleaseSlotAndDecrementInFlightParams{
			AcquisitionToken: tokUUID,
			ReleaseStatus:    "released_failure",
			ReleaseReason:    &releaseReason,
		})
		if err != nil {
			return fmt.Errorf("billing: release slot on abort: %w", err)
		}
		if released == 0 {
			if err := verifyAlreadyReleasedSlot(ctx, dbbillingrecovery.New(tx), tokUUID, slotReleaseAbort); err != nil {
				return err
			}
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
		ClientTool:             nullableString(req.Draft.ClientTool),
	}
	if err := s.insertUsageRecordOrDLQ(ctx, tx, qtx, usageParams, "cache_hit_usage_record_insert_failed"); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("billing: commit cache-hit Tx2: %w", err)
	}
	return nil
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
		ClientTool:             params.ClientTool,
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
	// 列承载,二者不再混用(C1)。无真实输出 token → 记 0。
	// [2026-06-11 更新] 流式「上游全程无 usage」的估算兜底(gatewayhttp streamingCompletionEvent)
	// 会先把内容感知估算基数写入 draft.TokensOutput 再进本函数——该类行 usage_source=inferred 且
	// cost_snapshot 携带 usage_basis=estimated_from_delivered_content 标记;在 C3 计划的
	// usage_source='estimated' 枚举(需 schema 迁移,park 待 Owner)落地前以该标记区分估算行。
	// 使用量估算与真实 token 分离的决策证据见 docs/process/plans/2026-05-29-money-path-worker-claude.md §9。
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
