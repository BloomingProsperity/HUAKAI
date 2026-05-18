package gatewayhttp

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
)

const verifyReceiptBodyMaxBytes = 10 * 1024

type CostReceiptReader interface {
	GetReceipt(ctx context.Context, requestID string, tenantID int64) (*audit.CostReceipt, error)
}

type CostReceiptSigner interface {
	Fingerprint() string
	PublicKey() ed25519.PublicKey
}

type CostReceiptHandlerDeps struct {
	Receipts   CostReceiptReader
	RateTables billing.RateTableSource
	Signer     CostReceiptSigner
	Now        func() time.Time
}

type UserCostReceipt struct {
	SchemaVersion     string          `json:"schema_version"`
	RequestID         string          `json:"request_id"`
	TenantID          int64           `json:"tenant_id,omitempty"`
	TenantScopeRef    string          `json:"tenant_scope_ref"`
	OccurredAt        string          `json:"occurred_at"`
	Cost              UserReceiptCost `json:"cost"`
	ValidationState   string          `json:"validation_state"`
	Verdict           string          `json:"verdict"`
	AdjustmentRefs    []string        `json:"adjustment_refs"`
	CanonicalHash     string          `json:"canonical_hash"`
	Signature         string          `json:"signature"`
	PubkeyFingerprint string          `json:"pubkey_fingerprint"`
}

type UserReceiptCost struct {
	Model               string `json:"model"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CachedTokens        int64  `json:"cached_tokens"`
	CostTotalMicroUSD   int64  `json:"cost_total_micro_usd"`
	RateTableSnapshotID int64  `json:"rate_table_snapshot_id"`
}

type receiptVerifyResponse struct {
	Valid      bool   `json:"valid"`
	KeyStatus  string `json:"key_status"`
	AgeSeconds int64  `json:"age_seconds"`
}

func NewCostReceiptGetHandler(d CostReceiptHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Receipts == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt storage dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		requestID, ok := receiptRequestIDFromPath(w, r)
		if !ok {
			return
		}
		receipt, err := d.Receipts.GetReceipt(r.Context(), requestID, ident.TenantID)
		if errors.Is(err, audit.ErrReceiptNotFound) || errors.Is(err, audit.ErrReceiptInputsNotFound) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
			return
		}
		if errors.Is(err, audit.ErrReceiptUnavailable) {
			writeJSONError(w, http.StatusAccepted, "receipt_unavailable", "receipt is not final yet")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "receipt_read_failed", "receipt backend unavailable")
			return
		}
		if receipt == nil || receipt.TenantID != ident.TenantID {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
			return
		}
		out, err := userCostReceiptFromAudit(r.Context(), receipt)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "receipt_format_failed", "receipt format failed")
			return
		}
		writeAuditJSON(w, http.StatusOK, out)
	}
}

func NewCostReceiptVerifyHandler(d CostReceiptHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Signer == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt signer dependency unset")
			return
		}
		requestID, ok := receiptVerifyRequestIDFromPath(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, verifyReceiptBodyMaxBytes)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "receipt verify body must be <= 10KB")
				return
			}
			writeJSONError(w, http.StatusBadRequest, "body_read_error", err.Error())
			return
		}
		var req UserCostReceipt
		if err := json.Unmarshal(raw, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if strings.TrimSpace(req.RequestID) != requestID {
			writeJSONError(w, http.StatusBadRequest, "request_id_mismatch", "receipt request_id must match path")
			return
		}
		canonical, err := canonicalHashFromUserReceipt(r.Context(), req)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_receipt", "receipt canonical fields are invalid")
			return
		}
		canonicalHex := hex.EncodeToString(canonical)
		canonicalHashMatches := strings.EqualFold(strings.TrimSpace(req.CanonicalHash), canonicalHex)
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Signature))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_signature", "signature must be base64")
			return
		}
		keyStatus := "active"
		valid := false
		if strings.TrimSpace(req.PubkeyFingerprint) != d.Signer.Fingerprint() {
			keyStatus = "unknown"
		} else if canonicalHashMatches && sign.Verify(d.Signer.PublicKey(), canonical, sig) == nil {
			valid = true
		}
		writeAuditJSON(w, http.StatusOK, receiptVerifyResponse{
			Valid:      valid,
			KeyStatus:  keyStatus,
			AgeSeconds: receiptAgeSeconds(req.OccurredAt, d.now()),
		})
	}
}

func NewPricingRateTableHandler(d CostReceiptHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.RateTables == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "rate table dependency unset")
			return
		}
		version := strings.TrimSpace(r.URL.Query().Get("version"))
		if version == "" {
			writeJSONError(w, http.StatusBadRequest, "version_required", "version query parameter required")
			return
		}
		table, err := d.RateTables.GetRateTable(r.Context(), version)
		if errors.Is(err, billing.ErrRateTableNotFound) {
			writeJSONError(w, http.StatusNotFound, "rate_table_not_found", "rate table version not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "rate_table_read_failed", "rate table backend unavailable")
			return
		}
		writeAuditJSON(w, http.StatusOK, table)
	}
}

func NewPricingSnapshotsHandler(d CostReceiptHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.RateTables == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "rate table dependency unset")
			return
		}
		snapshots, err := d.RateTables.ListRateTableSnapshots(r.Context())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "rate_table_read_failed", "rate table backend unavailable")
			return
		}
		writeAuditJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
	}
}

func NewPricingSnapshotHandler(d CostReceiptHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.RateTables == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "rate table dependency unset")
			return
		}
		snapshotID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "snapshot_id")), 10, 64)
		if err != nil || snapshotID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "snapshot_id_invalid", "snapshot_id path parameter must be a positive integer")
			return
		}
		table, err := d.RateTables.GetRateTableSnapshot(r.Context(), snapshotID)
		if errors.Is(err, billing.ErrRateTableNotFound) {
			writeJSONError(w, http.StatusNotFound, "rate_table_not_found", "rate table snapshot not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "rate_table_read_failed", "rate table backend unavailable")
			return
		}
		writeAuditJSON(w, http.StatusOK, table)
	}
}

func receiptRequestIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
	if requestID == "" {
		requestID = strings.TrimSpace(chi.URLParam(r, "*"))
	}
	return validateReceiptPathRequestID(w, requestID)
}

func receiptVerifyRequestIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
	if requestID == "" {
		raw := strings.TrimSpace(chi.URLParam(r, "*"))
		if raw == "" || !strings.HasSuffix(raw, "/verify") {
			writeJSONError(w, http.StatusNotFound, "receipt_verify_route_not_found", "receipt verify path must end with /verify")
			return "", false
		}
		requestID = strings.TrimSuffix(raw, "/verify")
	}
	return validateReceiptPathRequestID(w, requestID)
}

func validateReceiptPathRequestID(w http.ResponseWriter, requestID string) (string, bool) {
	if requestID == "" {
		writeJSONError(w, http.StatusBadRequest, "request_id_required", "request_id path parameter required")
		return "", false
	}
	if len(requestID) > MaxRequestIDLength {
		writeJSONError(w, http.StatusBadRequest, "request_id_too_long", "request_id length must be <= 256 bytes")
		return "", false
	}
	return requestID, true
}

func userCostReceiptFromAudit(ctx context.Context, receipt *audit.CostReceipt) (UserCostReceipt, error) {
	out := UserCostReceipt{
		SchemaVersion:  audit.ReceiptSchemaVersion,
		RequestID:      receipt.RequestID,
		TenantScopeRef: auditledger.TenantScopeRef(receipt.TenantID),
		OccurredAt:     receipt.CreatedAt.UTC().Format(time.RFC3339Nano),
		Cost: UserReceiptCost{
			Model:               receipt.Model,
			InputTokens:         receipt.InputTokens,
			OutputTokens:        receipt.OutputTokens,
			CachedTokens:        receipt.CachedTokens,
			CostTotalMicroUSD:   receipt.CostUSDMicros,
			RateTableSnapshotID: receipt.RateTableSnapshotID,
		},
		ValidationState:   "valid",
		Verdict:           "match",
		AdjustmentRefs:    []string{},
		Signature:         base64.StdEncoding.EncodeToString(receipt.SignedHash),
		PubkeyFingerprint: string(receipt.SignerFingerprint),
	}
	canonical, err := audit.CanonicalReceiptHashForPayload(ctx, canonicalPayloadFromUserReceipt(out))
	if err != nil {
		return UserCostReceipt{}, err
	}
	out.CanonicalHash = hex.EncodeToString(canonical)
	return out, nil
}

func canonicalPayloadFromUserReceipt(receipt UserCostReceipt) audit.ReceiptCanonicalPayload {
	return audit.ReceiptCanonicalPayload{
		SchemaVersion:       firstNonEmpty(receipt.SchemaVersion, audit.ReceiptSchemaVersion),
		RequestID:           strings.TrimSpace(receipt.RequestID),
		TenantScopeRef:      strings.TrimSpace(receipt.TenantScopeRef),
		Model:               strings.TrimSpace(receipt.Cost.Model),
		InputTokens:         receipt.Cost.InputTokens,
		OutputTokens:        receipt.Cost.OutputTokens,
		CachedTokens:        receipt.Cost.CachedTokens,
		CostTotalMicroUSD:   receipt.Cost.CostTotalMicroUSD,
		RateTableSnapshotID: receipt.Cost.RateTableSnapshotID,
		CreatedAt:           canonicalReceiptTime(receipt.OccurredAt),
		ValidationState:     strings.TrimSpace(receipt.ValidationState),
		Verdict:             strings.TrimSpace(receipt.Verdict),
		AdjustmentRefs:      canonicalAdjustmentRefs(receipt.AdjustmentRefs),
	}
}

func canonicalPayloadV1FromUserReceipt(receipt UserCostReceipt) audit.ReceiptCanonicalPayloadV1 {
	return audit.ReceiptCanonicalPayloadV1{
		SchemaVersion:       audit.ReceiptSchemaVersionV1,
		RequestID:           strings.TrimSpace(receipt.RequestID),
		TenantID:            receipt.TenantID,
		Model:               strings.TrimSpace(receipt.Cost.Model),
		InputTokens:         receipt.Cost.InputTokens,
		OutputTokens:        receipt.Cost.OutputTokens,
		CachedTokens:        receipt.Cost.CachedTokens,
		CostTotalMicroUSD:   receipt.Cost.CostTotalMicroUSD,
		RateTableSnapshotID: receipt.Cost.RateTableSnapshotID,
		CreatedAt:           canonicalReceiptTime(receipt.OccurredAt),
	}
}

func canonicalHashFromUserReceipt(ctx context.Context, receipt UserCostReceipt) ([]byte, error) {
	switch strings.TrimSpace(firstNonEmpty(receipt.SchemaVersion, audit.ReceiptSchemaVersion)) {
	case audit.ReceiptSchemaVersionV1:
		return audit.CanonicalReceiptHashForPayloadV1(ctx, canonicalPayloadV1FromUserReceipt(receipt))
	case audit.ReceiptSchemaVersion:
		return audit.CanonicalReceiptHashForPayload(ctx, canonicalPayloadFromUserReceipt(receipt))
	default:
		return nil, audit.ErrReceiptInvalidDerivedData
	}
}

func canonicalAdjustmentRefs(refs []string) []string {
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

func canonicalReceiptTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func receiptAgeSeconds(occurredAt string, now time.Time) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(occurredAt))
	if err != nil {
		return 0
	}
	age := now.UTC().Sub(parsed.UTC())
	if age < 0 {
		return 0
	}
	return int64(age.Seconds())
}

func (d CostReceiptHandlerDeps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
