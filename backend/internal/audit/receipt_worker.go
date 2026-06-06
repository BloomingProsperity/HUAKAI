package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

type ReceiptAppender interface {
	AppendReceipt(ctx context.Context, receipt *CostReceipt) error
}

type ReceiptRecoveryEnqueuer interface {
	Enqueue(ctx context.Context, e dlq.Event) (int64, error)
}

type ReferralQualifier interface {
	QualifyPendingReferral(ctx context.Context, tenantID, refereeUserID, billingEventID int64) error
}

type ReferralRewardIssuer interface {
	ApplyReferralReward(ctx context.Context, input payment.ReferralRewardInput) (payment.ReferralRewardResult, error)
}

type ReferralRewardSettings interface {
	Get(context.Context, platformsettings.SettingKey) (platformsettings.StoredSetting, error)
}

type receiptFormatterService interface {
	DeriveReceipt(ctx context.Context, requestID string) (*CostReceipt, error)
	SignReceipt(ctx context.Context, receipt *CostReceipt) (*CostReceipt, error)
}

type ReceiptHookErrorHandler func(ctx context.Context, requestID string, err error)

type ReceiptHookOption func(*ReceiptHookHandler)

// ReceiptHookHandler 在 Tx2 成功后从既有账务事实派生并写入 receipt snapshot。
type ReceiptHookHandler struct {
	formatter   receiptFormatterService
	storage     ReceiptAppender
	trustSigner *sign.Signer
	onError     ReceiptHookErrorHandler
	recovery    ReceiptRecoveryEnqueuer
}

func NewReceiptHookHandler(formatter receiptFormatterService, storage ReceiptAppender, opts ...ReceiptHookOption) *ReceiptHookHandler {
	h := &ReceiptHookHandler{formatter: formatter, storage: storage}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

func WithReceiptHookErrorHandler(fn ReceiptHookErrorHandler) ReceiptHookOption {
	return func(h *ReceiptHookHandler) {
		h.onError = fn
	}
}

func WithReceiptHookTrustSigner(signer *sign.Signer) ReceiptHookOption {
	return func(h *ReceiptHookHandler) {
		h.trustSigner = signer
	}
}

func WithReceiptHookRecoveryEnqueuer(q ReceiptRecoveryEnqueuer) ReceiptHookOption {
	return func(h *ReceiptHookHandler) {
		h.recovery = q
	}
}

type ReceiptRecoverySource string

const ReceiptRecoverySourceSettleHook ReceiptRecoverySource = "settle_hook"

const receiptRecoveryEnqueueTimeout = 2 * time.Second

var (
	ErrReceiptRecoveryPayloadInvalid = errors.New("audit: receipt recovery payload invalid")
	ErrReceiptRecoveryWrongKind      = errors.New("audit: receipt recovery wrong dlq kind")
)

type ReceiptRecoveryPayload struct {
	Source    ReceiptRecoverySource `json:"source"`
	RequestID string                `json:"request_id"`
	TenantID  int64                 `json:"tenant_id"`
	ClaimID   int64                 `json:"claim_id"`
}

func (p ReceiptRecoveryPayload) Validate() error {
	switch {
	case p.Source != ReceiptRecoverySourceSettleHook:
		return fmt.Errorf("%w: source=%q", ErrReceiptRecoveryPayloadInvalid, p.Source)
	case strings.TrimSpace(p.RequestID) == "":
		return fmt.Errorf("%w: request_id required", ErrReceiptRecoveryPayloadInvalid)
	case p.TenantID <= 0:
		return fmt.Errorf("%w: tenant_id required", ErrReceiptRecoveryPayloadInvalid)
	case p.ClaimID <= 0:
		return fmt.Errorf("%w: claim_id required", ErrReceiptRecoveryPayloadInvalid)
	default:
		return nil
	}
}

func (p ReceiptRecoveryPayload) Encode() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

func DecodeReceiptRecoveryPayload(raw []byte) (ReceiptRecoveryPayload, error) {
	var p ReceiptRecoveryPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return ReceiptRecoveryPayload{}, fmt.Errorf("audit: decode receipt recovery payload: %w", err)
	}
	if err := p.Validate(); err != nil {
		return ReceiptRecoveryPayload{}, err
	}
	return p, nil
}

func (h *ReceiptHookHandler) AppendSettledReceipt(ctx context.Context, req billing.SettleRequest) error {
	return h.appendSettledReceipt(ctx, req, true)
}

func (h *ReceiptHookHandler) appendSettledReceipt(ctx context.Context, req billing.SettleRequest, enqueueRecovery bool) error {
	if h == nil {
		return nil
	}
	if h.formatter == nil {
		return ErrReceiptFormatterNil
	}
	if h.storage == nil {
		return ErrReceiptStorageRequired
	}
	requestID := strings.TrimSpace(req.AuditRequestID)
	if requestID == "" {
		return nil
	}
	receipt, err := h.formatter.DeriveReceipt(ctx, requestID)
	if err != nil {
		err = fmt.Errorf("audit: derive receipt after settle: %w", err)
		if enqueueRecovery {
			h.enqueueReceiptRecovery(ctx, req, nil, err)
		}
		return err
	}
	h.attachFinalTrustSignature(ctx, req, receipt)
	if err := h.storage.AppendReceipt(ctx, receipt); err != nil {
		if errors.Is(err, ErrReceiptDuplicate) {
			return nil
		}
		err = fmt.Errorf("audit: append receipt after settle: %w", err)
		if enqueueRecovery {
			h.enqueueReceiptRecovery(ctx, req, receipt, err)
		}
		return err
	}
	return nil
}

func (h *ReceiptHookHandler) HandleReceiptRecovery(ctx context.Context, record dlq.Record) error {
	if h == nil {
		return ErrReceiptFormatterNil
	}
	if record.EventKind != dlq.EventKindCostReceiptAppend {
		return fmt.Errorf("%w: %q", ErrReceiptRecoveryWrongKind, record.EventKind)
	}
	payload, err := DecodeReceiptRecoveryPayload(record.Payload)
	if err != nil {
		return err
	}
	if record.TenantID != 0 && record.TenantID != payload.TenantID {
		return fmt.Errorf("%w: record tenant_id=%d payload tenant_id=%d", ErrReceiptRecoveryPayloadInvalid, record.TenantID, payload.TenantID)
	}
	if record.ClaimID != nil && *record.ClaimID != payload.ClaimID {
		return fmt.Errorf("%w: record claim_id=%d payload claim_id=%d", ErrReceiptRecoveryPayloadInvalid, *record.ClaimID, payload.ClaimID)
	}
	return h.appendSettledReceipt(ctx, billing.SettleRequest{
		TenantID:       payload.TenantID,
		ClaimID:        payload.ClaimID,
		AuditRequestID: payload.RequestID,
	}, false)
}

func (h *ReceiptHookHandler) attachFinalTrustSignature(ctx context.Context, req billing.SettleRequest, receipt *CostReceipt) {
	if receipt == nil {
		return
	}
	finalReceipt := trustreceipt.BuildFinalFromSettleEvent(billing.SettleRequest{}, nil, finalReceiptFactsFromCostReceipt(receipt))
	finalReceipt.RedactedMetadataAllowlist = finalTrustReceiptMetadataFromCostReceipt(receipt)
	sigB64, fingerprint, err := trustreceipt.SignReceipt(h.trustSigner, finalReceipt)
	if err != nil {
		receipt.SignerFingerprint = []byte{}
		receipt.SignedHash = []byte{}
		h.report(ctx, req.AuditRequestID, err)
		return
	}
	receipt.SignerFingerprint = []byte(fingerprint)
	receipt.SignedHash = []byte(sigB64)
}

func finalTrustReceiptMetadataFromCostReceipt(receipt *CostReceipt) map[string]any {
	if receipt == nil {
		return map[string]any{}
	}
	return map[string]any{
		"adjustment_refs": strings.Join(normalizedAdjustmentRefs(receipt.AdjustmentRefs), "\n"),
		"cost_usd_micros": receipt.CostUSDMicros,
		"verdict":         NormalizeReceiptVerdict(receipt.Verdict),
	}
}

func FinalTrustReceiptCanonical(receipt *CostReceipt) ([]byte, error) {
	if receipt == nil {
		return nil, ErrReceiptRequired
	}
	finalReceipt := trustreceipt.BuildFinalFromSettleEvent(billing.SettleRequest{}, nil, finalReceiptFactsFromCostReceipt(receipt))
	finalReceipt.RedactedMetadataAllowlist = finalTrustReceiptMetadataFromCostReceipt(receipt)
	return trustreceipt.Canonical(finalReceipt)
}

func FinalTrustReceiptDisplayID(receipt *CostReceipt) (string, error) {
	if receipt == nil {
		return "", ErrReceiptRequired
	}
	finalReceipt := trustreceipt.BuildFinalFromSettleEvent(billing.SettleRequest{}, nil, finalReceiptFactsFromCostReceipt(receipt))
	finalReceipt.RedactedMetadataAllowlist = finalTrustReceiptMetadataFromCostReceipt(receipt)
	hash, err := trustreceipt.CanonicalHash(finalReceipt)
	if err != nil {
		return "", err
	}
	return trustreceipt.DisplayReceiptID(hash), nil
}

func finalReceiptFactsFromCostReceipt(receipt *CostReceipt) trustreceipt.FinalReceiptFacts {
	if receipt == nil {
		return trustreceipt.FinalReceiptFacts{}
	}
	return trustreceipt.FinalReceiptFacts{
		RequestID:           receipt.RequestID,
		ReceiptSequence:     int(receipt.ReceiptSequence),
		TenantID:            receipt.TenantID,
		OccurredAt:          receipt.CreatedAt,
		Model:               receipt.Model,
		InputTokens:         receipt.InputTokens,
		OutputTokens:        receipt.OutputTokens,
		CachedTokens:        receipt.CachedTokens,
		CostUSDMicros:       receipt.CostUSDMicros,
		RateTableSnapshotID: receipt.RateTableSnapshotID,
		ValidationState:     NormalizeReceiptValidationState(receipt.ValidationState),
	}
}

func (h *ReceiptHookHandler) report(ctx context.Context, requestID string, err error) {
	if h == nil || h.onError == nil || err == nil {
		return
	}
	h.onError(ctx, strings.TrimSpace(requestID), err)
}

func (h *ReceiptHookHandler) enqueueReceiptRecovery(ctx context.Context, req billing.SettleRequest, receipt *CostReceipt, cause error) {
	if h == nil || h.recovery == nil || cause == nil {
		return
	}
	requestID := strings.TrimSpace(req.AuditRequestID)
	if requestID == "" {
		return
	}
	tenantID := req.TenantID
	claimID := req.ClaimID
	if receipt != nil {
		if tenantID == 0 {
			tenantID = receipt.TenantID
		}
		if claimID == 0 {
			claimID = receipt.ClaimID
		}
	}
	payload := ReceiptRecoveryPayload{
		Source:    ReceiptRecoverySourceSettleHook,
		RequestID: requestID,
		TenantID:  tenantID,
		ClaimID:   claimID,
	}
	raw, err := payload.Encode()
	if err != nil {
		h.report(ctx, requestID, err)
		return
	}
	enqueueCtx, cancel := receiptRecoveryEnqueueContext(ctx)
	defer cancel()
	_, err = h.recovery.Enqueue(enqueueCtx, dlq.Event{
		TenantID:       tenantID,
		ClaimID:        claimID,
		EventKind:      dlq.EventKindCostReceiptAppend,
		Lane:           dlq.LaneForKind(dlq.EventKindCostReceiptAppend),
		Payload:        raw,
		FailureReason:  cause.Error(),
		IdempotencyKey: fmt.Sprintf("cost_receipt_append:%d:%d:%s", tenantID, claimID, requestID),
		SourceTable:    "user_cost_receipts",
		SourceID:       claimID,
	})
	if err != nil {
		h.report(ctx, requestID, fmt.Errorf("audit: enqueue receipt recovery: %w", err))
	}
}

func receiptRecoveryEnqueueContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), receiptRecoveryEnqueueTimeout)
}

// ReceiptHookSettler 包装真实 settler；账务成功后 fail-open 写 receipt,
// 失败则交给 durable recovery 队列重放。
type ReceiptHookSettler struct {
	inner                  billing.Settler
	hook                   *ReceiptHookHandler
	referralQualifier      ReferralQualifier
	referralRewardIssuer   ReferralRewardIssuer
	referralRewardSettings ReferralRewardSettings
}

type ReceiptHookSettlerOption func(*ReceiptHookSettler)

func WithReceiptHookReferralQualifier(qualifier ReferralQualifier) ReceiptHookSettlerOption {
	return func(s *ReceiptHookSettler) {
		s.referralQualifier = qualifier
	}
}

func WithReceiptHookReferralRewardIssuer(issuer ReferralRewardIssuer) ReceiptHookSettlerOption {
	return func(s *ReceiptHookSettler) {
		s.referralRewardIssuer = issuer
	}
}

func WithReceiptHookReferralRewardSettings(settings ReferralRewardSettings) ReceiptHookSettlerOption {
	return func(s *ReceiptHookSettler) {
		s.referralRewardSettings = settings
	}
}

func NewReceiptHookSettler(inner billing.Settler, hook *ReceiptHookHandler, opts ...ReceiptHookSettlerOption) billing.Settler {
	if hook == nil && len(opts) == 0 {
		return inner
	}
	settler := &ReceiptHookSettler{inner: inner, hook: hook}
	for _, opt := range opts {
		if opt != nil {
			opt(settler)
		}
	}
	if settler.hook == nil && settler.referralQualifier == nil && settler.referralRewardIssuer == nil {
		return inner
	}
	return settler
}

func (s *ReceiptHookSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	res, err := s.inner.Settle(ctx, req)
	if err != nil {
		return res, err
	}
	if s.hook != nil {
		if hookErr := s.hook.AppendSettledReceipt(ctx, req); hookErr != nil {
			s.hook.report(ctx, req.AuditRequestID, hookErr)
		}
	}
	s.qualifyReferral(ctx, req, res)
	return res, nil
}

func (s *ReceiptHookSettler) qualifyReferral(ctx context.Context, req billing.SettleRequest, res *billing.SettleResult) {
	if s == nil || (s.referralQualifier == nil && s.referralRewardIssuer == nil) || res == nil || res.BillingEventID <= 0 {
		return
	}
	tenantID := res.TenantID
	if tenantID <= 0 {
		tenantID = req.TenantID
	}
	refereeUserID := res.UserID
	if refereeUserID <= 0 {
		refereeUserID = req.UserID
	}
	if tenantID <= 0 || refereeUserID <= 0 {
		return
	}
	if s.applyReferralRewardIfEnabled(ctx, req, tenantID, refereeUserID, res.BillingEventID) {
		return
	}
	if s.referralQualifier == nil {
		return
	}
	if err := s.referralQualifier.QualifyPendingReferral(ctx, tenantID, refereeUserID, res.BillingEventID); err != nil && s.hook != nil {
		s.hook.report(ctx, req.AuditRequestID, fmt.Errorf("audit: qualify referral after settle: %w", err))
	}
}

func (s *ReceiptHookSettler) applyReferralRewardIfEnabled(ctx context.Context, req billing.SettleRequest, tenantID, refereeUserID, billingEventID int64) bool {
	if s == nil || s.referralRewardIssuer == nil || s.referralRewardSettings == nil {
		return false
	}
	enabled, rewardCents, err := s.referralRewardConfig(ctx)
	if err != nil {
		if s.hook != nil {
			s.hook.report(ctx, req.AuditRequestID, fmt.Errorf("audit: referral reward config: %w", err))
		}
		return false
	}
	if !enabled || rewardCents <= 0 {
		return false
	}
	if _, err := s.referralRewardIssuer.ApplyReferralReward(ctx, payment.ReferralRewardInput{
		TenantID:       tenantID,
		RefereeUserID:  refereeUserID,
		BillingEventID: billingEventID,
		RewardCents:    rewardCents,
		CurrencyCode:   "USD",
		Now:            time.Now().UTC(),
	}); err != nil && s.hook != nil {
		s.hook.report(ctx, req.AuditRequestID, fmt.Errorf("audit: issue referral reward after settle: %w", err))
	}
	return true
}

func (s *ReceiptHookSettler) referralRewardConfig(ctx context.Context) (bool, int64, error) {
	enabledRow, err := s.referralRewardSettings.Get(ctx, platformsettings.KeyReferralRewardEnabled)
	if err != nil {
		return false, 0, err
	}
	enabled, err := parseReferralRewardBool(enabledRow.Value)
	if err != nil || !enabled {
		return enabled, 0, err
	}
	centsRow, err := s.referralRewardSettings.Get(ctx, platformsettings.KeyReferralRewardCents)
	if err != nil {
		return false, 0, err
	}
	cents, err := strconv.ParseInt(strings.TrimSpace(centsRow.Value), 10, 64)
	if err != nil || cents < 0 {
		return false, 0, platformsettings.ErrInvalidValue
	}
	return true, cents, nil
}

func parseReferralRewardBool(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, platformsettings.ErrInvalidValue
	}
}

func (s *ReceiptHookSettler) Abort(ctx context.Context, tenantID, claimID int64, reason, auditRequestID string, observedInputTokens int64, protocolLoss json.RawMessage) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	return s.inner.Abort(ctx, tenantID, claimID, reason, auditRequestID, observedInputTokens, protocolLoss)
}

func (s *ReceiptHookSettler) CommitCacheHit(ctx context.Context, req billing.SettleRequest) error {
	if s == nil || s.inner == nil {
		return billing.ErrPoolNotConfigured
	}
	if err := s.inner.CommitCacheHit(ctx, req); err != nil {
		return err
	}
	// L2 cache 命中走 CommitCacheHit 而非 Settle; CommitCacheHit 现在写了
	// provider-less usage_records 行, receipt 源能 join 到 → 补跑 receipt hook
	// 生成 user_cost_receipts, 与正常结算一致。
	if s.hook != nil {
		if hookErr := s.hook.AppendSettledReceipt(ctx, req); hookErr != nil {
			s.hook.report(ctx, req.AuditRequestID, hookErr)
		}
	}
	return nil
}

func (s *ReceiptHookSettler) Refund(ctx context.Context, req billing.RefundRequest) (*billing.RefundResult, error) {
	if s == nil || s.inner == nil {
		return nil, billing.ErrPoolNotConfigured
	}
	return s.inner.Refund(ctx, req)
}
