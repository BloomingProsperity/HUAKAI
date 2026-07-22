package audit

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
)

const (
	ReceiptSchemaVersionV1 = "audit.receipt.v1"
	ReceiptSchemaVersionV2 = "audit.receipt.v2"
	ReceiptSchemaVersion   = ReceiptSchemaVersionV2
)
const receiptRequestIDMaxLength = 256

const (
	ReceiptValidationStateValid              = "valid"
	ReceiptValidationStateProvisional        = "provisional"
	ReceiptValidationStateMismatchPending    = "mismatch_pending"
	ReceiptValidationStateMismatchRefunded   = "mismatch_refunded"
	ReceiptValidationStateNotBillable        = "not_billable"
	ReceiptValidationStateReceiptUnavailable = "receipt_unavailable"
	ReceiptValidationStateUnknown            = "unknown"

	ReceiptVerdictMatch                 = "match"
	ReceiptVerdictSubstitutionRefund    = "substitution_refund"
	ReceiptVerdictMismatchRefundPending = "mismatch_refund_pending"
	ReceiptVerdictUnknown               = "unknown"
)

var (
	ErrReceiptFormatterNil       = errors.New("audit: receipt formatter is nil")
	ErrReceiptRequestIDRequired  = errors.New("audit: request_id required")
	ErrReceiptLedgerRequired     = errors.New("audit: ledger required")
	ErrReceiptSourceRequired     = errors.New("audit: receipt source required")
	ErrReceiptSignerRequired     = errors.New("audit: receipt signer required")
	ErrReceiptRequired           = errors.New("audit: receipt required")
	ErrReceiptInputsNotFound     = errors.New("audit: receipt inputs not found")
	ErrReceiptUnavailable        = errors.New("audit: receipt billed but usage pending DLQ")
	ErrReceiptInvalidDerivedData = errors.New("audit: invalid receipt derived data")
	ErrCostOverflow              = errors.New("audit: cost overflow")
)

const maxCostMicroUSDInt64 int64 = 1<<63 - 1

var maxCostMicroUSDDecimal = decimal.NewFromInt(maxCostMicroUSDInt64)

// CostReceipt 是用户侧可验证消费凭证的内部结构。
type CostReceipt struct {
	RequestID           string
	TenantID            int64
	UserID              int64
	ClaimID             int64
	OwnerSource         string
	ReceiptSequence     int32
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CachedTokens        int64
	CostUSDMicros       int64
	RateTableSnapshotID int64
	ValidationState     string
	Verdict             string
	AdjustmentRefs      []string
	SignerFingerprint   []byte
	SignedHash          []byte
	CreatedAt           time.Time
}

// ReceiptInputs 是从 billing/usage/audit JOIN 派生出的最小输入集。
type ReceiptInputs struct {
	TenantID            int64
	UserID              int64
	ClaimID             int64
	OwnerSource         string
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CachedTokens        int64
	CostUSDMicros       int64
	RateTableSnapshotID int64
	CreatedAt           time.Time
}

// ReceiptInputSource 负责把现有账务事实读成 receipt 输入，不拥有第二套账本。
type ReceiptInputSource interface {
	LookupReceiptInputs(ctx context.Context, requestID string, tenantID int64) (ReceiptInputs, error)
}

type receiptSigner interface {
	Sign(ctx context.Context, payload []byte) (signature []byte, pubkeyFingerprint string, err error)
}

type legacyReceiptSigner interface {
	Sign([]byte) []byte
	Fingerprint() string
	PublicKey() ed25519.PublicKey
}

type legacyReceiptSignerAdapter struct {
	inner legacyReceiptSigner
}

func (a legacyReceiptSignerAdapter) Sign(_ context.Context, payload []byte) ([]byte, string, error) {
	if a.inner == nil {
		return nil, "", ErrReceiptSignerRequired
	}
	return a.inner.Sign(payload), a.inner.Fingerprint(), nil
}

// ReceiptFormatter 从 audit ledger + billing facts 生成用户可验证 receipt。
type ReceiptFormatter struct {
	ledger auditledger.Ledger
	source ReceiptInputSource
	signer receiptSigner
	now    func() time.Time
}

type ReceiptFormatterOption func(*ReceiptFormatter)

// NewReceiptFormatter 构造只依赖既有账务事实的回执格式化器。
func NewReceiptFormatter(
	ledger auditledger.Ledger,
	source ReceiptInputSource,
	signer any,
	opts ...ReceiptFormatterOption,
) (*ReceiptFormatter, error) {
	if ledger == nil {
		return nil, ErrReceiptLedgerRequired
	}
	if source == nil {
		return nil, ErrReceiptSourceRequired
	}
	if signer == nil {
		return nil, ErrReceiptSignerRequired
	}
	normalizedSigner, err := normalizeReceiptSigner(signer)
	if err != nil {
		return nil, err
	}
	rf := &ReceiptFormatter{
		ledger: ledger,
		source: source,
		signer: normalizedSigner,
		now:    func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(rf)
		}
	}
	if rf.now == nil {
		rf.now = func() time.Time { return time.Now().UTC() }
	}
	return rf, nil
}

// WithReceiptNow 注入时间源，测试用固定时间。
func WithReceiptNow(now func() time.Time) ReceiptFormatterOption {
	return func(rf *ReceiptFormatter) {
		if now != nil {
			rf.now = now
		}
	}
}

// DeriveReceipt 只从现有 ledger/billing/usage 事实派生 receipt，不写账务事实。
func (rf *ReceiptFormatter) DeriveReceipt(ctx context.Context, requestID string) (*CostReceipt, error) {
	if rf == nil {
		return nil, ErrReceiptFormatterNil
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return nil, err
	}
	if rf.ledger == nil {
		return nil, ErrReceiptLedgerRequired
	}
	if rf.source == nil {
		return nil, ErrReceiptSourceRequired
	}

	entry, err := rf.ledger.GetByRequestID(ctx, requestID)
	if err != nil {
		return nil, fmt.Errorf("audit: lookup ledger entry for receipt: %w", err)
	}
	if entry.TenantID <= 0 {
		return nil, fmt.Errorf("%w: ledger tenant_id missing", ErrReceiptInvalidDerivedData)
	}

	inputs, err := rf.source.LookupReceiptInputs(ctx, requestID, entry.TenantID)
	if err != nil {
		return nil, err
	}
	if inputs.TenantID == 0 {
		inputs.TenantID = entry.TenantID
	}
	if inputs.TenantID != entry.TenantID {
		return nil, fmt.Errorf("%w: tenant mismatch", ErrReceiptInvalidDerivedData)
	}
	if err := validateReceiptInputs(inputs); err != nil {
		return nil, err
	}

	model := strings.TrimSpace(inputs.Model)
	if model == "" {
		model = modelFromLedgerEntry(entry)
	}
	if model == "" {
		return nil, fmt.Errorf("%w: model missing", ErrReceiptInvalidDerivedData)
	}
	createdAt := inputs.CreatedAt
	if createdAt.IsZero() {
		createdAt = timestampFromLedgerEntry(entry)
	}
	if createdAt.IsZero() {
		createdAt = rf.now()
	}

	return &CostReceipt{
		RequestID:           requestID,
		TenantID:            entry.TenantID,
		UserID:              inputs.UserID,
		ClaimID:             inputs.ClaimID,
		OwnerSource:         receiptOwnerSourceFromInputs(inputs),
		Model:               model,
		InputTokens:         inputs.InputTokens,
		OutputTokens:        inputs.OutputTokens,
		CachedTokens:        inputs.CachedTokens,
		CostUSDMicros:       inputs.CostUSDMicros,
		RateTableSnapshotID: inputs.RateTableSnapshotID,
		ValidationState:     receiptValidationStateFromLedger(entry),
		Verdict:             receiptVerdictFromLedger(entry),
		CreatedAt:           createdAt.UTC(),
	}, nil
}

// SignReceipt 对 receipt 的 trust.receipt.v1 canonical 做 ed25519 签名,返回带签名副本。
// canonical 与验签侧(cost_receipt_handler.canonicalBytesFromCostReceipt→FinalTrustReceiptCanonical)
// 及正常结算侧(attachFinalTrustSignature→trustreceipt.SignReceipt)同口径;SignedHash 存 base64,
// 与验签解码一致。用此(退款回执唯一签名入口)签的 receipt 才能通过用户验签。
func (rf *ReceiptFormatter) SignReceipt(ctx context.Context, receipt *CostReceipt) (*CostReceipt, error) {
	if rf == nil {
		return nil, ErrReceiptFormatterNil
	}
	if rf.signer == nil {
		return nil, ErrReceiptSignerRequired
	}
	if receipt == nil {
		return nil, ErrReceiptRequired
	}
	out := cloneReceipt(receipt)
	if out.CreatedAt.IsZero() {
		out.CreatedAt = rf.now().UTC()
	}
	out.ValidationState = NormalizeReceiptValidationState(out.ValidationState)
	out.Verdict = NormalizeReceiptVerdict(out.Verdict)
	out.AdjustmentRefs = normalizedAdjustmentRefs(out.AdjustmentRefs)
	if err := validateReceiptForSigning(out); err != nil {
		return nil, err
	}
	canonical, err := FinalTrustReceiptCanonical(out)
	if err != nil {
		return nil, err
	}
	signature, fingerprint, err := rf.signer.Sign(ctx, canonical)
	if err != nil {
		return nil, fmt.Errorf("audit: sign receipt: %w", err)
	}
	out.SignerFingerprint = []byte(fingerprint)
	out.SignedHash = []byte(base64.StdEncoding.EncodeToString(signature))
	return out, nil
}

const receiptInputsSQL = `
SELECT
    be.tenant_id,
    blc.id::bigint AS claim_id,
    blc.user_id::bigint AS user_id,
    COALESCE(NULLIF(ur.upstream_model, ''), ur.requested_model, blc.requested_model) AS model,
    ur.tokens_input::bigint,
    ur.tokens_output::bigint,
    ur.cache_read_tokens::bigint,
    be.actual_cost::text,
    ur.snapshot_version,
    ur.id::bigint,
    COALESCE(be.occurred_at, ur.settled_at) AS created_at,
    ur.settlement_source
FROM billing_events be
JOIN billing_ledger_claims blc
  ON blc.tenant_id = be.tenant_id
 AND blc.id = be.claim_id
LEFT JOIN usage_records ur
  ON ur.tenant_id = blc.tenant_id
 AND ur.claim_id = blc.id
WHERE be.audit_request_id = $1
  AND be.tenant_id = $2
  AND be.event_type IN ('claim_committed', 'claim_aborted')
ORDER BY be.occurred_at DESC, ur.settled_at DESC NULLS LAST, ur.id DESC NULLS LAST
LIMIT 1`

func validateReceiptInputs(inputs ReceiptInputs) error {
	switch {
	case inputs.TenantID <= 0:
		return fmt.Errorf("%w: tenant_id must be positive", ErrReceiptInvalidDerivedData)
	case inputs.UserID < 0:
		return fmt.Errorf("%w: user_id must be non-negative", ErrReceiptInvalidDerivedData)
	case inputs.ClaimID < 0:
		return fmt.Errorf("%w: claim_id must be non-negative", ErrReceiptInvalidDerivedData)
	case strings.TrimSpace(inputs.OwnerSource) != "" && !validReceiptOwnerSource(inputs.OwnerSource):
		return fmt.Errorf("%w: owner_source unsupported", ErrReceiptInvalidDerivedData)
	case inputs.InputTokens < 0:
		return fmt.Errorf("%w: input_tokens must be non-negative", ErrReceiptInvalidDerivedData)
	case inputs.OutputTokens < 0:
		return fmt.Errorf("%w: output_tokens must be non-negative", ErrReceiptInvalidDerivedData)
	case inputs.CachedTokens < 0:
		return fmt.Errorf("%w: cached_tokens must be non-negative", ErrReceiptInvalidDerivedData)
	case inputs.CostUSDMicros < 0:
		return fmt.Errorf("%w: cost_usd_micros must be non-negative", ErrReceiptInvalidDerivedData)
	case inputs.RateTableSnapshotID <= 0:
		return fmt.Errorf("%w: rate_table_snapshot_id must be positive", ErrReceiptInvalidDerivedData)
	}
	return nil
}

func receiptOwnerSourceFromInputs(inputs ReceiptInputs) string {
	source := strings.TrimSpace(inputs.OwnerSource)
	if source == "" {
		return ReceiptOwnerSourceSettle
	}
	return source
}

func receiptOwnerSourceFromSettlementSource(source string) string {
	switch strings.TrimSpace(source) {
	case billing.SettlementSourceResponseCacheL2:
		return ReceiptOwnerSourceCacheHit
	default:
		return ReceiptOwnerSourceSettle
	}
}

func validateReceiptForSigning(receipt *CostReceipt) error {
	if receipt == nil {
		return ErrReceiptRequired
	}
	if err := validateReceiptRequestID(receipt.RequestID); err != nil {
		return err
	}
	if receipt.ReceiptSequence < 0 {
		return fmt.Errorf("%w: receipt_sequence must be non-negative", ErrReceiptInvalidDerivedData)
	}
	return validateReceiptInputs(ReceiptInputs{
		TenantID:            receipt.TenantID,
		Model:               receipt.Model,
		InputTokens:         receipt.InputTokens,
		OutputTokens:        receipt.OutputTokens,
		CachedTokens:        receipt.CachedTokens,
		CostUSDMicros:       receipt.CostUSDMicros,
		RateTableSnapshotID: receipt.RateTableSnapshotID,
		CreatedAt:           receipt.CreatedAt,
	})
}

func normalizeReceiptSigner(signer any) (receiptSigner, error) {
	if signer == nil {
		return nil, ErrReceiptSignerRequired
	}
	if s, ok := signer.(receiptSigner); ok {
		return s, nil
	}
	if s, ok := signer.(legacyReceiptSigner); ok {
		return legacyReceiptSignerAdapter{inner: s}, nil
	}
	return nil, ErrReceiptSignerRequired
}

func normalizedAdjustmentRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			out = append(out, ref)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func NormalizeReceiptValidationState(state string) string {
	switch strings.TrimSpace(state) {
	case "", ReceiptValidationStateValid:
		return ReceiptValidationStateValid
	case ReceiptValidationStateProvisional:
		return ReceiptValidationStateProvisional
	case ReceiptValidationStateMismatchPending:
		return ReceiptValidationStateMismatchPending
	case ReceiptValidationStateMismatchRefunded:
		return ReceiptValidationStateMismatchRefunded
	case ReceiptValidationStateNotBillable:
		return ReceiptValidationStateNotBillable
	case ReceiptValidationStateReceiptUnavailable:
		return ReceiptValidationStateReceiptUnavailable
	case ReceiptValidationStateUnknown:
		return ReceiptValidationStateUnknown
	default:
		return ReceiptValidationStateUnknown
	}
}

func NormalizeReceiptVerdict(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case "", ReceiptVerdictMatch:
		return ReceiptVerdictMatch
	case ReceiptVerdictSubstitutionRefund:
		return ReceiptVerdictSubstitutionRefund
	case ReceiptVerdictMismatchRefundPending:
		return ReceiptVerdictMismatchRefundPending
	case ReceiptVerdictUnknown:
		return ReceiptVerdictUnknown
	default:
		return ReceiptVerdictUnknown
	}
}

func validateReceiptRequestID(requestID string) error {
	if strings.TrimSpace(requestID) == "" {
		return ErrReceiptRequestIDRequired
	}
	if len(requestID) > receiptRequestIDMaxLength {
		return fmt.Errorf("%w: request_id length must be <= %d", ErrReceiptInvalidDerivedData, receiptRequestIDMaxLength)
	}
	return nil
}

func cloneReceipt(in *CostReceipt) *CostReceipt {
	if in == nil {
		return nil
	}
	out := *in
	out.AdjustmentRefs = normalizedAdjustmentRefs(in.AdjustmentRefs)
	out.SignerFingerprint = append([]byte(nil), in.SignerFingerprint...)
	out.SignedHash = append([]byte(nil), in.SignedHash...)
	return &out
}

func receiptValidationStateFromLedger(entry auditledger.LedgerEntry) string {
	if entry.ModelChain == nil {
		return ReceiptValidationStateValid
	}
	switch strings.TrimSpace(entry.ModelChain.Verdict) {
	case "mismatch":
		return ReceiptValidationStateMismatchPending
	case "unknown":
		return ReceiptValidationStateProvisional
	default:
		return ReceiptValidationStateValid
	}
}

func receiptVerdictFromLedger(entry auditledger.LedgerEntry) string {
	if entry.ModelChain == nil {
		return ReceiptVerdictMatch
	}
	switch strings.TrimSpace(entry.ModelChain.Verdict) {
	case "mismatch":
		return ReceiptVerdictMismatchRefundPending
	case "allowed_alias":
		return ReceiptVerdictSubstitutionRefund
	case "unknown":
		return ReceiptVerdictUnknown
	default:
		return ReceiptVerdictMatch
	}
}

func modelFromLedgerEntry(entry auditledger.LedgerEntry) string {
	if entry.ModelChain == nil {
		return ""
	}
	for _, model := range []string{
		entry.ModelChain.UpstreamReported,
		entry.ModelChain.RouteDecided,
		entry.ModelChain.Requested,
	} {
		if strings.TrimSpace(model) != "" {
			return strings.TrimSpace(model)
		}
	}
	return ""
}

func timestampFromLedgerEntry(entry auditledger.LedgerEntry) time.Time {
	if strings.TrimSpace(entry.Timestamp) == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return ts.UTC()
}

func usdDecimalStringToMicros(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%w: empty cost", ErrReceiptInvalidDerivedData)
	}
	d, err := decimal.NewFromString(value)
	if err != nil {
		return 0, fmt.Errorf("%w: cost parse: %v", ErrReceiptInvalidDerivedData, err)
	}
	if d.IsNegative() {
		return 0, fmt.Errorf("%w: cost must be non-negative", ErrReceiptInvalidDerivedData)
	}
	micros := d.Mul(decimal.NewFromInt(1_000_000)).Round(0)
	if micros.GreaterThan(maxCostMicroUSDDecimal) {
		return 0, ErrCostOverflow
	}
	return micros.IntPart(), nil
}

var (
	snapshotRegistryVersionPattern = regexp.MustCompile(`(?i)(?:^|;)registry:[^:;]+:([0-9]+)(?:;|$)`)
	snapshotRateIDPattern          = regexp.MustCompile(`(?i)(?:rate_table|pricing|rate)[^0-9]*([0-9]+)`)
)

func rateTableSnapshotID(snapshotVersion string, fallback int64) int64 {
	for _, pattern := range []*regexp.Regexp{snapshotRegistryVersionPattern, snapshotRateIDPattern} {
		match := pattern.FindStringSubmatch(snapshotVersion)
		if len(match) == 2 {
			if id, err := strconv.ParseInt(match[1], 10, 64); err == nil && id > 0 {
				return id
			}
		}
	}
	if fallback > 0 {
		return fallback
	}
	return 1
}
