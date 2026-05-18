package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
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
)

// CostReceipt 是用户侧可验证消费凭证的内部结构。
type CostReceipt struct {
	RequestID           string
	TenantID            int64
	ClaimID             int64
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
	ClaimID             int64
	Model               string
	InputTokens         int64
	OutputTokens        int64
	CachedTokens        int64
	CostUSDMicros       int64
	RateTableSnapshotID int64
	CreatedAt           time.Time
}

// ReceiptCanonicalPayloadV1 是历史 receipt 的签名输入；仅用于兼容老签名。
type ReceiptCanonicalPayloadV1 struct {
	SchemaVersion       string `json:"schema_version"`
	RequestID           string `json:"request_id"`
	TenantID            int64  `json:"tenant_id"`
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CostTotalMicroUSD   int64  `json:"cost_total_micro_usd"`
	RateTableSnapshotID int64  `json:"rate_table_snapshot_id"`
	CreatedAt           string `json:"created_at"`
}

// ReceiptCanonicalPayloadV2 是用户可见 receipt 的签名输入；只放不可逆租户引用。
type ReceiptCanonicalPayloadV2 struct {
	SchemaVersion       string   `json:"schema_version"`
	RequestID           string   `json:"request_id"`
	ReceiptSequence     int32    `json:"receipt_sequence"`
	TenantScopeRef      string   `json:"tenant_scope_ref"`
	Model               string   `json:"model"`
	InputTokens         int64    `json:"input_tokens"`
	OutputTokens        int64    `json:"output_tokens"`
	CachedTokens        int64    `json:"cached_tokens"`
	CostTotalMicroUSD   int64    `json:"cost_total_micro_usd"`
	RateTableSnapshotID int64    `json:"rate_table_snapshot_id"`
	CreatedAt           string   `json:"created_at"`
	ValidationState     string   `json:"validation_state"`
	Verdict             string   `json:"verdict"`
	AdjustmentRefs      []string `json:"adjustment_refs"`
}

type ReceiptCanonicalPayload = ReceiptCanonicalPayloadV2

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
	ledger   auditledger.Ledger
	billing  billing.Settler
	source   ReceiptInputSource
	signer   receiptSigner
	redactor privacy.Redactor
	now      func() time.Time
}

type ReceiptFormatterOption func(*ReceiptFormatter)

// NewReceiptFormatter 构造 receipt formatter；billingSettler 只作为上游账务边界依赖保存。
func NewReceiptFormatter(
	ledger auditledger.Ledger,
	billingSettler billing.Settler,
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
		ledger:   ledger,
		billing:  billingSettler,
		source:   source,
		signer:   normalizedSigner,
		redactor: privacy.DefaultRedactor(),
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(rf)
		}
	}
	if rf.redactor == nil {
		rf.redactor = privacy.DefaultRedactor()
	}
	if rf.now == nil {
		rf.now = func() time.Time { return time.Now().UTC() }
	}
	return rf, nil
}

// WithReceiptRedactor 替换 canonical payload 使用的隐私守门器。
func WithReceiptRedactor(redactor privacy.Redactor) ReceiptFormatterOption {
	return func(rf *ReceiptFormatter) {
		if redactor != nil {
			rf.redactor = redactor
		}
	}
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
		ClaimID:             inputs.ClaimID,
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

// SignReceipt 对 canonical receipt hash 做 ed25519 签名，返回带签名副本。
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
	canonical, err := canonicalReceiptHashWithRedactor(ctx, rf.redactor, out)
	if err != nil {
		return nil, err
	}
	signature, fingerprint, err := rf.signer.Sign(ctx, canonical)
	if err != nil {
		return nil, fmt.Errorf("audit: sign receipt: %w", err)
	}
	out.SignerFingerprint = []byte(fingerprint)
	out.SignedHash = append([]byte(nil), signature...)
	return out, nil
}

func canonicalReceiptHash(receipt *CostReceipt) ([]byte, error) {
	return canonicalReceiptHashWithRedactor(context.Background(), privacy.DefaultRedactor(), receipt)
}

func canonicalReceiptHashWithRedactor(ctx context.Context, redactor privacy.Redactor, receipt *CostReceipt) ([]byte, error) {
	if receipt == nil {
		return nil, ErrReceiptRequired
	}
	payload := ReceiptCanonicalPayload{
		SchemaVersion:       ReceiptSchemaVersion,
		RequestID:           receipt.RequestID,
		ReceiptSequence:     receipt.ReceiptSequence,
		TenantScopeRef:      auditledger.TenantScopeRef(receipt.TenantID),
		Model:               receipt.Model,
		InputTokens:         receipt.InputTokens,
		OutputTokens:        receipt.OutputTokens,
		CachedTokens:        receipt.CachedTokens,
		CostTotalMicroUSD:   receipt.CostUSDMicros,
		RateTableSnapshotID: receipt.RateTableSnapshotID,
		CreatedAt:           receipt.CreatedAt.UTC().Format(time.RFC3339Nano),
		ValidationState:     NormalizeReceiptValidationState(receipt.ValidationState),
		Verdict:             NormalizeReceiptVerdict(receipt.Verdict),
		AdjustmentRefs:      normalizedAdjustmentRefs(receipt.AdjustmentRefs),
	}
	return CanonicalReceiptHashWithRedactor(ctx, redactor, payload)
}

// CanonicalReceiptHashForPayload 返回 detached verify 使用的 receipt hash。
func CanonicalReceiptHashForPayload(ctx context.Context, payload ReceiptCanonicalPayload) ([]byte, error) {
	return CanonicalReceiptHashWithRedactor(ctx, privacy.DefaultRedactor(), payload)
}

// CanonicalReceiptHashForPayloadV1 返回历史 receipt 的 detached verify hash。
func CanonicalReceiptHashForPayloadV1(ctx context.Context, payload ReceiptCanonicalPayloadV1) ([]byte, error) {
	return CanonicalReceiptHashV1WithRedactor(ctx, privacy.DefaultRedactor(), payload)
}

// CanonicalReceiptHashWithRedactor 对 v2 用户可见字段做稳定 JSON + SHA-256。
func CanonicalReceiptHashWithRedactor(ctx context.Context, redactor privacy.Redactor, payload ReceiptCanonicalPayload) ([]byte, error) {
	if err := validateReceiptCanonicalPayloadV2(&payload); err != nil {
		return nil, err
	}
	return canonicalReceiptPayloadHash(ctx, redactor, payload)
}

// CanonicalReceiptHashV1WithRedactor 对 v1 历史字段做稳定 JSON + SHA-256。
func CanonicalReceiptHashV1WithRedactor(ctx context.Context, redactor privacy.Redactor, payload ReceiptCanonicalPayloadV1) ([]byte, error) {
	if err := validateReceiptCanonicalPayloadV1(&payload); err != nil {
		return nil, err
	}
	return canonicalReceiptPayloadHash(ctx, redactor, payload)
}

func canonicalReceiptPayloadHash(ctx context.Context, redactor privacy.Redactor, payload any) ([]byte, error) {
	if redactor == nil {
		redactor = privacy.DefaultRedactor()
	}
	raw, err := redactor.SanitizePayload(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("audit: canonical receipt redaction: %w", err)
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

// SQLReceiptSource 是当前 PostgreSQL schema 的 receipt 派生读取器。
type SQLReceiptSource struct {
	db    *sql.DB
	query receiptQueryer
}

// NewSQLReceiptSource 用 database/sql 包装现有 PostgreSQL 连接。
func NewSQLReceiptSource(db *sql.DB) (*SQLReceiptSource, error) {
	if db == nil {
		return nil, errors.New("audit: sql db required")
	}
	return &SQLReceiptSource{db: db, query: sqlReceiptDB{db: db}}, nil
}

// LookupReceiptInputs 读取 audit/billing/usage 的现有事实并转换为 receipt 输入。
func (s *SQLReceiptSource) LookupReceiptInputs(ctx context.Context, requestID string, tenantID int64) (ReceiptInputs, error) {
	if s == nil {
		return ReceiptInputs{}, ErrReceiptSourceRequired
	}
	if err := validateReceiptRequestID(requestID); err != nil {
		return ReceiptInputs{}, err
	}
	query := s.query
	if query == nil && s.db != nil {
		query = sqlReceiptDB{db: s.db}
	}
	if query == nil {
		return ReceiptInputs{}, errors.New("audit: sql receipt source db required")
	}

	var (
		inputs         ReceiptInputs
		costUSD        string
		snapshot       sql.NullString
		claimID        int64
		usageRecordID  sql.NullInt64
		createdAt      time.Time
		modelNullable  sql.NullString
		inputTokens    sql.NullInt64
		outputTokens   sql.NullInt64
		cacheReadToken sql.NullInt64
	)
	row := query.QueryRowContext(ctx, receiptInputsSQL, requestID, tenantID)
	if err := row.Scan(
		&inputs.TenantID,
		&claimID,
		&modelNullable,
		&inputTokens,
		&outputTokens,
		&cacheReadToken,
		&costUSD,
		&snapshot,
		&usageRecordID,
		&createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReceiptInputs{}, ErrReceiptInputsNotFound
		}
		return ReceiptInputs{}, fmt.Errorf("audit: lookup receipt inputs: %w", err)
	}
	if !usageRecordID.Valid {
		return ReceiptInputs{}, ErrReceiptUnavailable
	}
	costMicros, err := usdDecimalStringToMicros(costUSD)
	if err != nil {
		return ReceiptInputs{}, err
	}
	inputs.ClaimID = claimID
	inputs.Model = modelNullable.String
	inputs.InputTokens = inputTokens.Int64
	inputs.OutputTokens = outputTokens.Int64
	inputs.CachedTokens = cacheReadToken.Int64
	inputs.CostUSDMicros = costMicros
	inputs.RateTableSnapshotID = rateTableSnapshotID(snapshot.String, usageRecordID.Int64)
	inputs.CreatedAt = createdAt
	if err := validateReceiptInputs(inputs); err != nil {
		return ReceiptInputs{}, err
	}
	return inputs, nil
}

const receiptInputsSQL = `
SELECT
    be.tenant_id,
    blc.id::bigint AS claim_id,
    COALESCE(NULLIF(ur.upstream_model, ''), ur.requested_model, blc.requested_model) AS model,
    ur.tokens_input::bigint,
    ur.tokens_output::bigint,
    ur.cache_read_tokens::bigint,
    be.actual_cost::text,
    ur.snapshot_version,
    ur.id::bigint,
    COALESCE(be.occurred_at, ur.settled_at) AS created_at
FROM audit_ledger_entries ale
JOIN billing_events be
  ON be.tenant_id = ale.tenant_id
 AND be.audit_request_id = ale.request_id
JOIN billing_ledger_claims blc
  ON blc.tenant_id = be.tenant_id
 AND blc.id = be.claim_id
LEFT JOIN usage_records ur
  ON ur.tenant_id = blc.tenant_id
 AND ur.claim_id = blc.id
WHERE ale.request_id = $1
  AND ale.tenant_id = $2
  AND be.event_type IN ('claim_committed', 'claim_aborted')
ORDER BY be.occurred_at DESC, ur.settled_at DESC NULLS LAST, ur.id DESC NULLS LAST
LIMIT 1`

func validateReceiptInputs(inputs ReceiptInputs) error {
	switch {
	case inputs.TenantID <= 0:
		return fmt.Errorf("%w: tenant_id must be positive", ErrReceiptInvalidDerivedData)
	case inputs.ClaimID < 0:
		return fmt.Errorf("%w: claim_id must be non-negative", ErrReceiptInvalidDerivedData)
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

func validateReceiptCanonicalPayloadV1(payload *ReceiptCanonicalPayloadV1) error {
	if payload == nil {
		return ErrReceiptRequired
	}
	if strings.TrimSpace(payload.SchemaVersion) == "" {
		payload.SchemaVersion = ReceiptSchemaVersionV1
	}
	if payload.SchemaVersion != ReceiptSchemaVersionV1 {
		return fmt.Errorf("%w: schema_version unsupported", ErrReceiptInvalidDerivedData)
	}
	if err := validateReceiptRequestID(payload.RequestID); err != nil {
		return err
	}
	return validateReceiptInputs(ReceiptInputs{
		TenantID:            payload.TenantID,
		Model:               payload.Model,
		InputTokens:         payload.InputTokens,
		OutputTokens:        payload.OutputTokens,
		CachedTokens:        payload.CachedTokens,
		CostUSDMicros:       payload.CostTotalMicroUSD,
		RateTableSnapshotID: payload.RateTableSnapshotID,
	})
}

func validateReceiptCanonicalPayloadV2(payload *ReceiptCanonicalPayloadV2) error {
	if payload == nil {
		return ErrReceiptRequired
	}
	if strings.TrimSpace(payload.SchemaVersion) == "" {
		payload.SchemaVersion = ReceiptSchemaVersion
	}
	if payload.SchemaVersion != ReceiptSchemaVersion {
		return fmt.Errorf("%w: schema_version unsupported", ErrReceiptInvalidDerivedData)
	}
	if err := validateReceiptRequestID(payload.RequestID); err != nil {
		return err
	}
	if payload.ReceiptSequence < 0 {
		return fmt.Errorf("%w: receipt_sequence must be non-negative", ErrReceiptInvalidDerivedData)
	}
	if strings.TrimSpace(payload.TenantScopeRef) == "" {
		return fmt.Errorf("%w: tenant_scope_ref missing", ErrReceiptInvalidDerivedData)
	}
	payload.ValidationState = strings.TrimSpace(payload.ValidationState)
	if payload.ValidationState == "" {
		return fmt.Errorf("%w: validation_state missing", ErrReceiptInvalidDerivedData)
	}
	payload.Verdict = strings.TrimSpace(payload.Verdict)
	if payload.Verdict == "" {
		return fmt.Errorf("%w: verdict missing", ErrReceiptInvalidDerivedData)
	}
	payload.AdjustmentRefs = normalizedAdjustmentRefs(payload.AdjustmentRefs)
	return validateReceiptInputs(ReceiptInputs{
		TenantID:            1,
		Model:               payload.Model,
		InputTokens:         payload.InputTokens,
		OutputTokens:        payload.OutputTokens,
		CachedTokens:        payload.CachedTokens,
		CostUSDMicros:       payload.CostTotalMicroUSD,
		RateTableSnapshotID: payload.RateTableSnapshotID,
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
	default:
		return ReceiptValidationStateValid
	}
}

func NormalizeReceiptVerdict(verdict string) string {
	switch strings.TrimSpace(verdict) {
	case ReceiptVerdictSubstitutionRefund:
		return ReceiptVerdictSubstitutionRefund
	case ReceiptVerdictMismatchRefundPending:
		return ReceiptVerdictMismatchRefundPending
	case ReceiptVerdictUnknown:
		return ReceiptVerdictUnknown
	default:
		return ReceiptVerdictMatch
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
	return d.Mul(decimal.NewFromInt(1_000_000)).Round(0).IntPart(), nil
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
