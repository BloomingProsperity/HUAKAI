//go:build integration_pg

package gatewayhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestSignedReceiptMismatchRefundEndToEndPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openGatewayHTTPIntegrationPool(t, ctx)
	seed := seedReceiptRefundIntegration(t, ctx, pool)

	signer := mustReceiptSigner(t)
	ledger, err := auditledger.NewPostgresLedger(pool, signer)
	if err != nil {
		t.Fatalf("构造审计账本：%v", err)
	}
	if _, err := ledger.Append(ctx, mustPrepareGatewayHTTPLedgerEntry(t, ctx, auditledger.LedgerEntry{
		RequestID: seed.requestID,
		TenantID:  seed.tenantID,
		Timestamp: seed.now.Format(time.RFC3339Nano),
		ModelChain: &proto.ModelChain{
			Requested:        seed.model,
			RouteDecided:     seed.model,
			UpstreamReported: seed.model,
			Verdict:          "match",
		},
	})); err != nil {
		t.Fatalf("写入原请求审计事实：%v", err)
	}

	receiptSource, err := audit.NewPGXReceiptSource(pool)
	if err != nil {
		t.Fatalf("构造收据事实源：%v", err)
	}
	settler := billing.NewSettler(pool)
	formatter, err := audit.NewReceiptFormatter(
		ledger,
		receiptSource,
		signer,
		audit.WithReceiptNow(func() time.Time { return seed.now }),
	)
	if err != nil {
		t.Fatalf("构造收据格式器：%v", err)
	}
	receiptStore, err := audit.NewPGXReceiptStorage(pool)
	if err != nil {
		t.Fatalf("构造收据存储：%v", err)
	}

	current, err := formatter.DeriveReceipt(ctx, seed.requestID)
	if err != nil {
		t.Fatalf("从真实账务事实派生当前收据：%v", err)
	}
	if current.CostUSDMicros != seed.currentChargeMicros || current.InputTokens != seed.inputTokens {
		t.Fatalf("当前可信事实 cost=%d tokens=%d，期望 cost=%d tokens=%d",
			current.CostUSDMicros, current.InputTokens, seed.currentChargeMicros, seed.inputTokens)
	}

	oldReceipt := *current
	oldReceipt.CostUSDMicros = seed.originalChargeMicros
	oldReceipt.ReceiptSequence = 0
	oldReceipt.ValidationState = audit.ReceiptValidationStateValid
	oldReceipt.Verdict = audit.ReceiptVerdictMatch
	oldReceipt.AdjustmentRefs = nil
	signedOld, err := formatter.SignReceipt(ctx, &oldReceipt)
	if err != nil {
		t.Fatalf("签发原始收费收据：%v", err)
	}
	if err := receiptStore.AppendReceipt(ctx, signedOld); err != nil {
		t.Fatalf("持久化原始收费收据：%v", err)
	}
	originalSignature := string(signedOld.SignedHash)

	pendingStore, err := audit.NewPGXRefundPendingStore(pool)
	if err != nil {
		t.Fatalf("构造退款待办存储：%v", err)
	}
	dlqStore := dlq.NewStore(pool)
	dlqService := dlq.NewService(dlqStore)
	refundWorker := audit.NewMismatchRefundWorker(
		pendingStore,
		settler,
		formatter,
		audit.WithRefundLedger(ledger),
		audit.WithRefundReceiptSink(receiptStore),
		audit.WithRefundTxPool(pool),
		audit.WithRefundNow(func() time.Time { return seed.now }),
	)
	dlqService.Register(dlq.EventKindAuditMismatchRefund, refundWorker.Handler())
	refundQueue := audit.NewMismatchRefundQueue(dlqService,
		audit.WithRefundEligibilityVerifier(settler),
		audit.WithRefundNow(func() time.Time { return seed.now }))
	handler := receiptRouter(CostReceiptHandlerDeps{
		Receipts:        receiptStore,
		DerivedReceipts: formatter,
		MismatchRefunds: refundQueue,
		Signer:          signer,
	})

	submitted, err := userCostReceiptFromAudit(ctx, signedOld)
	if err != nil {
		t.Fatalf("生成用户提交收据：%v", err)
	}
	first := verifyReceiptRefundIntegration(t, handler, seed, submitted)
	wantRefundMicros := seed.originalChargeMicros - seed.currentChargeMicros
	if first.Valid || !first.SignatureValid || first.Status != "mismatch" ||
		first.Verdict != audit.ReceiptValidationStateMismatchPending ||
		first.DeltaMicroUSD != wantRefundMicros || first.RefundEventID <= 0 ||
		!receiptRefundContains(first.FieldsMismatch, "cost_total_micro_usd") {
		t.Fatalf("差额识别响应不完整：%+v，期望退款 %d", first, wantRefundMicros)
	}
	assertReceiptRefundBalance(t, ctx, pool, seed, "执行退款前", "9.98000000")

	delivered, err := dlqService.Replay(ctx, seed.tenantID, first.RefundEventID, "integration-refund-worker")
	if err != nil {
		t.Fatalf("执行退款恢复任务：%v", err)
	}
	if delivered == nil || delivered.ID != first.RefundEventID || delivered.Status != dlq.StatusDelivered {
		t.Fatalf("退款任务交付状态错误：%+v", delivered)
	}
	assertReceiptRefundCommitted(t, ctx, pool, receiptStore, seed, first.RefundEventID, wantRefundMicros, originalSignature)

	// 同一份可信旧收据再次提交只能命中同一任务，不能再次回补余额或追加退款事件。
	second := verifyReceiptRefundIntegration(t, handler, seed, submitted)
	if second.RefundEventID != first.RefundEventID || second.DeltaMicroUSD != wantRefundMicros {
		t.Fatalf("重复验证未收敛到原任务：first=%+v second=%+v", first, second)
	}
	assertReceiptRefundCommitted(t, ctx, pool, receiptStore, seed, first.RefundEventID, wantRefundMicros, originalSignature)
}

type receiptRefundIntegrationSeed struct {
	tenantID             int64
	userID               int64
	apiKeyID             int64
	providerID           int64
	poolGroupID          int64
	channelID            int64
	providerAccountID    int64
	claimID              int64
	requestID            string
	model                string
	now                  time.Time
	inputTokens          int64
	originalChargeMicros int64
	currentChargeMicros  int64
}

func seedReceiptRefundIntegration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) receiptRefundIntegrationSeed {
	t.Helper()
	suffix := fmt.Sprintf("receipt-refund-%d", time.Now().UTC().UnixNano())
	short := suffix[len(suffix)-12:]
	seed := receiptRefundIntegrationSeed{
		requestID:            "req-" + suffix,
		model:                "gpt-refund-e2e",
		now:                  time.Now().UTC().Truncate(time.Microsecond),
		inputTokens:          100,
		originalChargeMicros: 20_000,
		currentChargeMicros:  12_000,
	}

	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "tenant-"+suffix).Scan(&seed.tenantID); err != nil {
		t.Fatalf("写入租户：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (tenant_id, email, display_name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, suffix+"@example.test", "Receipt Refund E2E",
	).Scan(&seed.userID); err != nil {
		t.Fatalf("写入用户：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO api_keys (tenant_id, user_id, name, key_hash, key_prefix, status)
		 VALUES ($1, $2, $3, $4, $5, 'active') RETURNING id`,
		seed.tenantID, seed.userID, "key-"+short, "hash-"+suffix, "hk_e2e_"+short,
	).Scan(&seed.apiKeyID); err != nil {
		t.Fatalf("写入 API key：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
		 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
		seed.tenantID, "provider-"+short, "Provider "+short,
	).Scan(&seed.providerID); err != nil {
		t.Fatalf("写入 provider：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		seed.tenantID, "pool-"+short,
	).Scan(&seed.poolGroupID); err != nil {
		t.Fatalf("写入账号池：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		seed.tenantID, seed.poolGroupID, "channel-"+short,
	).Scan(&seed.channelID); err != nil {
		t.Fatalf("写入渠道：%v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type)
		 VALUES ($1, $2, $3, $4, 'api_key') RETURNING id`,
		seed.tenantID, seed.providerID, seed.channelID, "account-"+short,
	).Scan(&seed.providerAccountID); err != nil {
		t.Fatalf("写入上游账号：%v", err)
	}

	acquisitionToken := uuid.New()
	fingerprint := "fingerprint-" + suffix
	if err := pool.QueryRow(ctx, `
		INSERT INTO billing_ledger_claims (
			tenant_id, idempotency_key, request_fingerprint, api_key_id, user_id,
			logical_request_id, endpoint_family, requested_model, pooling_group_id,
			provider_account_id, acquisition_token, attempt_seq, billing_policy_version,
			request_class, predicted_cost, actual_cost, currency_code, status, settled_at,
			lease_expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 'chat', $7, $8,
			$9, $10, 1, 'refund-e2e-v1',
			'standard', $11, $11, 'USD', 'committed', $12, $13
		) RETURNING id`,
		seed.tenantID,
		"idempotency-"+suffix,
		fingerprint,
		seed.apiKeyID,
		seed.userID,
		"logical-"+suffix,
		seed.model,
		seed.poolGroupID,
		seed.providerAccountID,
		acquisitionToken,
		decimal.NewFromInt(seed.originalChargeMicros).Div(decimal.NewFromInt(1_000_000)),
		seed.now,
		seed.now.Add(time.Minute),
	).Scan(&seed.claimID); err != nil {
		t.Fatalf("写入已结算 claim：%v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_balances (tenant_id, user_id, balance, held) VALUES ($1, $2, $3, 0)`,
		seed.tenantID, seed.userID, decimal.RequireFromString("9.98000000"),
	); err != nil {
		t.Fatalf("写入扣款后余额：%v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO balance_holds (claim_id, tenant_id, user_id, amount, captured, state, resolved_at)
		VALUES ($1, $2, $3, $4, $4, 'captured', $5)`,
		seed.claimID,
		seed.tenantID,
		seed.userID,
		decimal.NewFromInt(seed.originalChargeMicros).Div(decimal.NewFromInt(1_000_000)),
		seed.now,
	); err != nil {
		t.Fatalf("写入已捕获余额持有：%v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO usage_records (
			tenant_id, claim_id, api_key_id, user_id, provider_account_id,
			acquisition_token, attempt_seq, tokens_input, tokens_output, actual_cost,
			input_cost, output_cost, end_class, usage_source, pending_reconciliation,
			requested_at, settled_at, requested_model, upstream_model, stream,
			settlement_source, snapshot_version
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, 1, $7, 0, $8,
			$8, 0, 'non_streaming', 'reported', false,
			$9, $9, $10, $10, false,
			'provider_upstream', 'refund-e2e-snapshot-v1'
		)`,
		seed.tenantID,
		seed.claimID,
		seed.apiKeyID,
		seed.userID,
		seed.providerAccountID,
		acquisitionToken,
		seed.inputTokens,
		decimal.NewFromInt(seed.currentChargeMicros).Div(decimal.NewFromInt(1_000_000)),
		seed.now,
		seed.model,
	); err != nil {
		t.Fatalf("写入当前可信用量事实：%v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO billing_events (
			tenant_id, claim_id, event_type, actual_cost, actual_cost_signed,
			stream_state, delivered_token_count, fingerprint, audit_request_id, occurred_at
		) VALUES ($1, $2, 'claim_committed', $3, $3, 2, 0, $4, $5, $6)`,
		seed.tenantID,
		seed.claimID,
		decimal.NewFromInt(seed.currentChargeMicros).Div(decimal.NewFromInt(1_000_000)),
		fingerprint,
		seed.requestID,
		seed.now,
	); err != nil {
		t.Fatalf("写入当前可信账务事实：%v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_cost_receipt_owners WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_cost_receipts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_refund_pending WHERE claim_id=$1`, seed.claimID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_record_dlq WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM audit_ledger_entries WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_refund_operations WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_events WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM usage_records WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM balance_holds WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM billing_ledger_claims WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM user_balances WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM provider_accounts WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM channels WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM pool_groups WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM providers WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM api_keys WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE tenant_id=$1`, seed.tenantID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM tenants WHERE id=$1`, seed.tenantID)
	})
	return seed
}

func verifyReceiptRefundIntegration(t *testing.T, handler http.Handler, seed receiptRefundIntegrationSeed, receipt UserCostReceipt) receiptVerifyResponse {
	t.Helper()
	rec := doReceiptRequest(
		t,
		handler,
		http.MethodPost,
		"/v1/receipts/"+seed.requestID+"/verify",
		receipt,
		sessionauth.SessionIdentity{TenantID: seed.tenantID, UserID: seed.userID},
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("验证收据 status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response receiptVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析收据验证响应：%v", err)
	}
	return response
}

func assertReceiptRefundCommitted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, receiptStore *audit.PGXReceiptStorage, seed receiptRefundIntegrationSeed, dlqEventID, wantRefundMicros int64, originalSignature string) {
	t.Helper()
	wantBalance := decimal.NewFromInt(9_980_000).Add(decimal.NewFromInt(wantRefundMicros)).Div(decimal.NewFromInt(1_000_000))
	assertReceiptRefundBalance(t, ctx, pool, seed, "执行退款后", wantBalance.StringFixed(8))

	var (
		billingEventID int64
		refunded       decimal.Decimal
		auditRequestID string
		usageSource    string
	)
	if err := pool.QueryRow(ctx, `
			SELECT id, -actual_cost_signed, audit_request_id, usage_source
			FROM billing_events
			WHERE tenant_id=$1 AND claim_id=$2
			  AND event_type='reconciliation_appended' AND actual_cost_signed < 0`,
		seed.tenantID, seed.claimID,
	).Scan(&billingEventID, &refunded, &auditRequestID, &usageSource); err != nil {
		t.Fatalf("读取退款账务事件：%v", err)
	}
	wantRefund := decimal.NewFromInt(wantRefundMicros).Div(decimal.NewFromInt(1_000_000))
	if !refunded.Equal(wantRefund) || strings.TrimSpace(auditRequestID) == "" || usageSource != audit.AuditMismatchRefundReason {
		t.Fatalf("退款账务事件 amount=%s audit_request_id=%q usage_source=%q，期望 amount=%s usage_source=%q",
			refunded, auditRequestID, usageSource, wantRefund, audit.AuditMismatchRefundReason)
	}
	var refundEventCount int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM billing_events
		WHERE tenant_id=$1 AND claim_id=$2 AND event_type='reconciliation_appended' AND actual_cost_signed < 0`,
		seed.tenantID, seed.claimID,
	).Scan(&refundEventCount); err != nil || refundEventCount != 1 {
		t.Fatalf("退款账务事件数量=%d err=%v，期望 1", refundEventCount, err)
	}
	var (
		operationKey         string
		operationFingerprint string
		operationOutcome     string
		operationApplied     int64
		operationCovered     int64
		operationEventID     int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT idempotency_key, request_fingerprint, outcome,
		       applied_amount_micro_usd, covered_amount_micro_usd, billing_event_id
		FROM billing_refund_operations
		WHERE tenant_id=$1 AND claim_id=$2`, seed.tenantID, seed.claimID).Scan(
		&operationKey, &operationFingerprint, &operationOutcome,
		&operationApplied, &operationCovered, &operationEventID,
	); err != nil {
		t.Fatalf("读取退款操作事实：%v", err)
	}
	if operationKey != fmt.Sprintf("audit_mismatch_refund:%d", seed.claimID) ||
		len(operationFingerprint) != 64 || operationOutcome != "applied" ||
		operationApplied != wantRefundMicros || operationCovered != wantRefundMicros ||
		operationEventID != billingEventID {
		t.Fatalf("退款操作事实不完整：key=%q fingerprint=%q outcome=%q applied=%d covered=%d event=%d",
			operationKey, operationFingerprint, operationOutcome, operationApplied, operationCovered, operationEventID)
	}

	var ledgerID string
	if err := pool.QueryRow(ctx,
		`SELECT ledger_id FROM audit_ledger_entries WHERE tenant_id=$1 AND request_id=$2`,
		seed.tenantID, auditRequestID,
	).Scan(&ledgerID); err != nil {
		t.Fatalf("读取退款审计账本：%v", err)
	}
	var pendingStatus string
	var pendingDelta int64
	if err := pool.QueryRow(ctx,
		`SELECT status, delta_micro_usd FROM audit_refund_pending WHERE claim_id=$1 AND tenant_id=$2`,
		seed.claimID, seed.tenantID,
	).Scan(&pendingStatus, &pendingDelta); err != nil {
		t.Fatalf("读取退款待办：%v", err)
	}
	if pendingStatus != "completed" || pendingDelta != wantRefundMicros {
		t.Fatalf("退款待办 status=%q delta=%d，期望 completed/%d", pendingStatus, pendingDelta, wantRefundMicros)
	}
	var dlqStatus string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM usage_record_dlq WHERE id=$1 AND tenant_id=$2 AND claim_id=$3 AND event_kind=$4`,
		dlqEventID, seed.tenantID, seed.claimID, string(dlq.EventKindAuditMismatchRefund),
	).Scan(&dlqStatus); err != nil {
		t.Fatalf("读取退款任务状态：%v", err)
	}
	if dlqStatus != string(dlq.StatusDelivered) {
		t.Fatalf("退款任务状态=%q，期望 delivered", dlqStatus)
	}
	var dlqCount int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM usage_record_dlq
		WHERE tenant_id=$1 AND claim_id=$2 AND event_kind=$3`,
		seed.tenantID, seed.claimID, string(dlq.EventKindAuditMismatchRefund),
	).Scan(&dlqCount); err != nil || dlqCount != 1 {
		t.Fatalf("退款任务数量=%d err=%v，期望 1", dlqCount, err)
	}

	original, err := receiptStore.GetReceiptBySequence(ctx, seed.requestID, seed.tenantID, 0)
	if err != nil {
		t.Fatalf("读取原始收据：%v", err)
	}
	if original.CostUSDMicros != seed.originalChargeMicros || string(original.SignedHash) != originalSignature ||
		original.ValidationState != audit.ReceiptValidationStateValid || len(original.AdjustmentRefs) != 0 {
		t.Fatalf("原始收据被改写：%+v", original)
	}
	latest, err := receiptStore.GetReceiptForUser(ctx, seed.requestID, seed.tenantID, seed.userID)
	if err != nil {
		t.Fatalf("读取退款后收据：%v", err)
	}
	if latest.ReceiptSequence != 1 || latest.CostUSDMicros != seed.currentChargeMicros ||
		latest.ValidationState != audit.ReceiptValidationStateMismatchRefunded || len(latest.SignedHash) == 0 ||
		!receiptRefundContains(latest.AdjustmentRefs, fmt.Sprintf("billing_event:%d", billingEventID)) ||
		!receiptRefundContains(latest.AdjustmentRefs, "audit_ledger:"+ledgerID) {
		t.Fatalf("退款后收据不完整：%+v", latest)
	}
}

func assertReceiptRefundBalance(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seed receiptRefundIntegrationSeed, stage, want string) {
	t.Helper()
	var balance decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT balance FROM user_balances WHERE tenant_id=$1 AND user_id=$2`,
		seed.tenantID, seed.userID,
	).Scan(&balance); err != nil {
		t.Fatalf("%s读取余额：%v", stage, err)
	}
	wantBalance := decimal.RequireFromString(want)
	if !balance.Equal(wantBalance) {
		t.Fatalf("%s余额=%s，期望 %s", stage, balance, wantBalance)
	}
}

func receiptRefundContains(values []string, want string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
