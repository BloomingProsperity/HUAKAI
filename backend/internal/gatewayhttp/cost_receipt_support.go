package gatewayhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

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
	requestID := namedReceiptRequestIDFromPath(r)
	if requestID == "" {
		requestID = strings.TrimSpace(chi.URLParam(r, "*"))
	}
	return validateReceiptPathRequestID(w, requestID)
}

func receiptVerifyRequestIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	requestID := namedReceiptRequestIDFromPath(r)
	if requestID == "" {
		raw := strings.TrimSpace(chi.URLParam(r, "*"))
		if raw == "" || !strings.HasSuffix(raw, "/verify") {
			http.NotFound(w, r)
			return "", false
		}
		requestID = strings.TrimSuffix(raw, "/verify")
	}
	return validateReceiptPathRequestID(w, requestID)
}

func namedReceiptRequestIDFromPath(r *http.Request) string {
	requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
	if requestID != "" {
		return requestID
	}
	host := strings.TrimSpace(chi.URLParam(r, "request_id_host"))
	tail := strings.TrimSpace(chi.URLParam(r, "request_id_tail"))
	if host == "" || tail == "" {
		return ""
	}
	return host + "/" + tail
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
	if strings.Count(requestID, "/") > 1 {
		writeJSONError(w, http.StatusNotFound, "receipt_route_not_found", "receipt request_id path may contain at most one slash")
		return "", false
	}
	return requestID, true
}

func receiptBelongsToSession(receipt *audit.CostReceipt, ident sessionauth.SessionIdentity) bool {
	return receipt != nil && receipt.TenantID == ident.TenantID && receipt.UserID > 0 && receipt.UserID == ident.UserID
}

func userCostReceiptFromAudit(ctx context.Context, receipt *audit.CostReceipt) (UserCostReceipt, error) {
	out := UserCostReceipt{
		SchemaVersion:   audit.ReceiptSchemaVersion,
		RequestID:       receipt.RequestID,
		ReceiptSequence: receipt.ReceiptSequence,
		TenantScopeRef:  auditledger.TenantScopeRef(receipt.TenantID),
		OccurredAt:      receipt.CreatedAt.UTC().Format(time.RFC3339Nano),
		Cost: UserReceiptCost{
			Model:               receipt.Model,
			InputTokens:         receipt.InputTokens,
			OutputTokens:        receipt.OutputTokens,
			CachedTokens:        receipt.CachedTokens,
			CostTotalMicroUSD:   receipt.CostUSDMicros,
			RateTableSnapshotID: receipt.RateTableSnapshotID,
		},
		ValidationState:   audit.NormalizeReceiptValidationState(receipt.ValidationState),
		Verdict:           audit.NormalizeReceiptVerdict(receipt.Verdict),
		AdjustmentRefs:    canonicalAdjustmentRefs(receipt.AdjustmentRefs),
		Signature:         receiptSignatureString(receipt.SignedHash),
		PubkeyFingerprint: string(receipt.SignerFingerprint),
	}
	canonical, err := canonicalHashFromUserReceipt(ctx, out)
	if err != nil {
		return UserCostReceipt{}, err
	}
	canonicalSum := sha256.Sum256(canonical)
	out.CanonicalHash = hex.EncodeToString(canonicalSum[:])
	return out, nil
}

func receiptSignatureString(signature []byte) string {
	if len(signature) == 0 {
		return ""
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(signature)); err == nil && len(decoded) == ed25519.SignatureSize {
		return string(signature)
	}
	return base64.StdEncoding.EncodeToString(signature)
}

func auditReceiptFromUserReceipt(receipt UserCostReceipt, tenantID int64) *audit.CostReceipt {
	return &audit.CostReceipt{
		RequestID:           strings.TrimSpace(receipt.RequestID),
		TenantID:            tenantID,
		ReceiptSequence:     receipt.ReceiptSequence,
		Model:               strings.TrimSpace(receipt.Cost.Model),
		InputTokens:         receipt.Cost.InputTokens,
		OutputTokens:        receipt.Cost.OutputTokens,
		CachedTokens:        receipt.Cost.CachedTokens,
		CostUSDMicros:       receipt.Cost.CostTotalMicroUSD,
		RateTableSnapshotID: receipt.Cost.RateTableSnapshotID,
		ValidationState:     strings.TrimSpace(receipt.ValidationState),
		Verdict:             strings.TrimSpace(receipt.Verdict),
		AdjustmentRefs:      canonicalAdjustmentRefs(receipt.AdjustmentRefs),
		CreatedAt:           userReceiptTime(receipt.OccurredAt),
	}
}

func userReceiptBelongsToTenant(receipt UserCostReceipt, tenantID int64) bool {
	switch receiptSchemaVersion(receipt) {
	case audit.ReceiptSchemaVersionV1:
		return receipt.TenantID == tenantID
	case audit.ReceiptSchemaVersion:
		return strings.TrimSpace(receipt.TenantScopeRef) == auditledger.TenantScopeRef(tenantID)
	default:
		return false
	}
}

func canonicalHashFromUserReceipt(ctx context.Context, receipt UserCostReceipt) ([]byte, error) {
	_ = ctx
	return canonicalBytesFromUserReceipt(receipt)
}

func canonicalBytesFromUserReceipt(receipt UserCostReceipt) ([]byte, error) {
	switch receiptSchemaVersion(receipt) {
	case audit.ReceiptSchemaVersionV1, audit.ReceiptSchemaVersion:
		return trustreceipt.Canonical(trustReceiptFromUserReceipt(receipt))
	default:
		return nil, audit.ErrReceiptInvalidDerivedData
	}
}

func trustReceiptFromUserReceipt(receipt UserCostReceipt) trustreceipt.TrustReceiptV1 {
	tenantScopeRef := strings.TrimSpace(receipt.TenantScopeRef)
	if tenantScopeRef == "" && receipt.TenantID > 0 {
		tenantScopeRef = auditledger.TenantScopeRef(receipt.TenantID)
	}
	model := strings.TrimSpace(receipt.Cost.Model)
	return trustreceipt.TrustReceiptV1{
		RequestID:       strings.TrimSpace(receipt.RequestID),
		ReceiptSequence: int(receipt.ReceiptSequence),
		TenantScopeRef:  tenantScopeRef,
		OccurredAt:      userReceiptTime(receipt.OccurredAt),
		Provider:        "",
		RequestedModel:  model,
		RoutedModel:     model,
		UpstreamModel:   model,
		DeliveredModel:  model,
		CostCents:       costMicrosToCents(receipt.Cost.CostTotalMicroUSD),
		TokenCounts: trustreceipt.TokenCounts{
			Input:  receipt.Cost.InputTokens,
			Output: receipt.Cost.OutputTokens,
			Cached: receipt.Cost.CachedTokens,
		},
		PriceSnapshot: trustreceipt.PriceSnapshot{
			RateTableSnapshotID: receipt.Cost.RateTableSnapshotID,
			CurrencyCode:        "USD",
		},
		ValidationState:           audit.NormalizeReceiptValidationState(receipt.ValidationState),
		RedactedMetadataAllowlist: trustReceiptMetadata(receipt.Cost.CostTotalMicroUSD, receipt.Verdict, receipt.AdjustmentRefs),
	}
}

func trustReceiptMetadata(costMicros int64, verdict string, adjustmentRefs []string) map[string]any {
	return map[string]any{
		"adjustment_refs": strings.Join(canonicalAdjustmentRefs(adjustmentRefs), "\n"),
		"cost_usd_micros": costMicros,
		"verdict":         audit.NormalizeReceiptVerdict(verdict),
	}
}

func costMicrosToCents(micros int64) int64 {
	if micros <= 0 {
		return 0
	}
	return (micros + 5_000) / 10_000
}

func receiptSchemaVersion(receipt UserCostReceipt) string {
	return strings.TrimSpace(firstNonEmpty(receipt.SchemaVersion, audit.ReceiptSchemaVersion))
}

func receiptSchemaVersionSupported(version string) bool {
	switch strings.TrimSpace(version) {
	case audit.ReceiptSchemaVersionV1, audit.ReceiptSchemaVersion:
		return true
	default:
		return false
	}
}

func supportedReceiptSchemaVersions() []string {
	return []string{audit.ReceiptSchemaVersionV1, audit.ReceiptSchemaVersion}
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

func userReceiptTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
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
