package gatewayhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
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
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

const verifyReceiptBodyMaxBytes = 10 * 1024

type CostReceiptReader interface {
	GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*audit.CostReceipt, error)
	GetReceiptForAdmin(ctx context.Context, requestID string, tenantID int64) (*audit.CostReceipt, error)
}

type CostReceiptDeriver interface {
	DeriveReceipt(ctx context.Context, requestID string) (*audit.CostReceipt, error)
}

type MismatchRefundEnqueuer interface {
	EnqueueMismatchRefund(ctx context.Context, receipt *audit.CostReceipt, verdict audit.MismatchVerdict) (int64, error)
}

type CostReceiptSigner interface {
	Fingerprint() string
	PublicKey() ed25519.PublicKey
}

type CostReceiptHandlerDeps struct {
	Receipts        CostReceiptReader
	DerivedReceipts CostReceiptDeriver
	MismatchRefunds MismatchRefundEnqueuer
	RateTables      billing.RateTableSource
	Signer          CostReceiptSigner
	PubkeyRegistry  auditledger.PubkeyRegistry
	Revocations     trusthttp.Revocations
	Now             func() time.Time
}

type UserCostReceipt struct {
	SchemaVersion     string          `json:"schema_version"`
	RequestID         string          `json:"request_id"`
	ReceiptSequence   int32           `json:"receipt_sequence"`
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
	Valid             bool     `json:"valid"`
	Status            string   `json:"status,omitempty"`
	SignatureValid    bool     `json:"signature_valid"`
	KeyStatus         string   `json:"key_status"`
	Reason            string   `json:"reason,omitempty"`
	CanonicalHash     string   `json:"canonical_hash,omitempty"`
	SchemaVersion     string   `json:"schema_version,omitempty"`
	AgeSeconds        int64    `json:"age_seconds"`
	ReceiptSequence   int32    `json:"receipt_sequence"`
	Verdict           string   `json:"verdict,omitempty"`
	DeltaMicroUSD     int64    `json:"delta_micro_usd,omitempty"`
	FieldsMismatch    []string `json:"fields_mismatch,omitempty"`
	RefundEventID     int64    `json:"refund_event_id,omitempty"`
	SupportedVersions []string `json:"supported_versions,omitempty"`
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
		if ident.UserID <= 0 {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
			return
		}
		receipt, err := d.Receipts.GetReceiptForUser(r.Context(), requestID, ident.TenantID, ident.UserID)
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
		if !receiptBelongsToSession(receipt, ident) {
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

// admin receipt handler 转 RR-W5-006 后续切片:需带 admin.OperatorAuth + scope 校验 + 挂路由。

func NewCostReceiptVerifyHandler(d CostReceiptHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Signer == nil && d.PubkeyRegistry == nil && d.Receipts == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt verify dependency unset")
			return
		}
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
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
		if len(strings.TrimSpace(string(raw))) == 0 {
			verifyStoredCostReceiptByID(w, r, d, requestID, ident)
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
		if !receiptSchemaVersionSupported(receiptSchemaVersion(req)) {
			writeAuditJSON(w, http.StatusOK, receiptVerifyResponse{
				Valid:             false,
				Status:            "missing",
				KeyStatus:         "unknown",
				Reason:            "schema_unsupported",
				AgeSeconds:        receiptAgeSeconds(req.OccurredAt, d.now()),
				ReceiptSequence:   req.ReceiptSequence,
				Verdict:           "schema_unsupported",
				SupportedVersions: supportedReceiptSchemaVersions(),
				SchemaVersion:     "trust.receipt.v1",
			})
			return
		}
		canonical, err := canonicalHashFromUserReceipt(r.Context(), req)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_receipt", "receipt canonical fields are invalid")
			return
		}
		canonicalSum := sha256.Sum256(canonical)
		canonicalHex := hex.EncodeToString(canonicalSum[:])
		canonicalHashMatches := strings.EqualFold(strings.TrimSpace(req.CanonicalHash), canonicalHex)
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Signature))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_signature", "signature must be base64")
			return
		}
		keyStatus := "unknown"
		reason := ""
		valid := false
		signatureValid := false
		status := "mismatch"
		verification, err := verifyReceiptTrustSignature(r.Context(), d, canonical, sig, strings.TrimSpace(req.PubkeyFingerprint))
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "receipt_pubkey_registry_error", err.Error())
			return
		}
		keyStatus = verification.KeyStatus
		reason = verification.Reason
		signatureValid = verification.Valid
		valid = canonicalHashMatches && verification.Valid && verification.Reason != "key_revoked"
		if verification.Valid && verification.Reason == "key_revoked" {
			status = "unverified"
		}
		if verification.Valid && !canonicalHashMatches {
			reason = "canonical_hash_mismatch"
		}
		if valid {
			status = "signed-only"
		}
		verifyVerdict := ""
		var mismatch audit.MismatchVerdict
		var refundEventID int64
		if valid && !userReceiptBelongsToTenant(req, ident.TenantID) {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
			return
		}
		if valid && ident.UserID <= 0 {
			writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
			return
		}
		if valid && d.Receipts != nil {
			stored, err := d.Receipts.GetReceiptForUser(r.Context(), requestID, ident.TenantID, ident.UserID)
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
			if !receiptBelongsToSession(stored, ident) {
				writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
				return
			}
		}
		if valid && d.DerivedReceipts != nil {
			derived, err := d.DerivedReceipts.DeriveReceipt(r.Context(), requestID)
			if err != nil || derived == nil {
				verifyVerdict = audit.ReceiptVerdictUnknown
			} else {
				// 防止已签名的跨租户 receipt 借同一 request_id 触发退款队列。
				if !receiptBelongsToSession(derived, ident) || !userReceiptBelongsToTenant(req, derived.TenantID) {
					writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
					return
				}
				submitted := auditReceiptFromUserReceipt(req, derived.TenantID)
				mismatch, err = audit.DetectReceiptMismatch(derived, submitted)
				if err != nil {
					writeJSONError(w, http.StatusBadRequest, "invalid_receipt", "receipt mismatch fields are invalid")
					return
				}
				verifyVerdict = mismatch.State
				if mismatch.State == audit.ReceiptValidationStateMismatchPending {
					valid = false
					status = "mismatch"
					if mismatch.RefundEligible() && d.MismatchRefunds != nil {
						if !receiptBelongsToSession(derived, ident) {
							writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
							return
						}
						refundEventID, err = d.MismatchRefunds.EnqueueMismatchRefund(r.Context(), derived, mismatch)
						if err != nil {
							writeJSONError(w, http.StatusServiceUnavailable, "refund_enqueue_failed", "mismatch refund could not be queued")
							return
						}
					}
				}
			}
		}
		writeAuditJSON(w, http.StatusOK, receiptVerifyResponse{
			Valid:           valid,
			Status:          status,
			SignatureValid:  signatureValid,
			KeyStatus:       keyStatus,
			Reason:          reason,
			CanonicalHash:   canonicalHex,
			SchemaVersion:   "trust.receipt.v1",
			AgeSeconds:      receiptAgeSeconds(req.OccurredAt, d.now()),
			ReceiptSequence: req.ReceiptSequence,
			Verdict:         verifyVerdict,
			DeltaMicroUSD:   mismatch.DeltaMicroUSD,
			FieldsMismatch:  mismatch.FieldsMismatch,
			RefundEventID:   refundEventID,
		})
	}
}

type costReceiptSequenceReader interface {
	GetReceiptBySequence(ctx context.Context, requestID string, tenantID int64, sequence int32) (*audit.CostReceipt, error)
}

type costReceiptDisplayReader interface {
	GetReceiptByDisplayID(ctx context.Context, displayID string, tenantID, userID int64) (*audit.CostReceipt, error)
}

func verifyStoredCostReceiptByID(w http.ResponseWriter, r *http.Request, d CostReceiptHandlerDeps, pathID string, ident sessionauth.SessionIdentity) {
	if d.Receipts == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt storage dependency unset")
		return
	}
	receipt, err := storedReceiptForVerify(r.Context(), d.Receipts, pathID, ident)
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
	if !receiptBelongsToSession(receipt, ident) {
		writeJSONError(w, http.StatusNotFound, "receipt_not_found", "receipt not found")
		return
	}

	canonical, err := canonicalBytesFromCostReceipt(receipt)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_receipt", "receipt canonical fields are invalid")
		return
	}
	canonicalSum := sha256.Sum256(canonical)
	canonicalHex := hex.EncodeToString(canonicalSum[:])
	resp := receiptVerifyResponse{
		Valid:           false,
		Status:          "unverified",
		SignatureValid:  false,
		KeyStatus:       "unknown",
		Reason:          "receipt_unsigned",
		CanonicalHash:   canonicalHex,
		SchemaVersion:   "trust.receipt.v1",
		AgeSeconds:      receiptAgeSeconds(receipt.CreatedAt.UTC().Format(time.RFC3339Nano), d.now()),
		ReceiptSequence: receipt.ReceiptSequence,
	}
	if len(receipt.SignedHash) == 0 || len(receipt.SignerFingerprint) == 0 {
		writeAuditJSON(w, http.StatusOK, resp)
		return
	}
	if d.Signer == nil && d.PubkeyRegistry == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "receipt verify key dependency unset")
		return
	}
	sig, err := receiptSignatureBytes(receipt.SignedHash)
	if err != nil {
		resp.Status = "mismatch"
		resp.KeyStatus = "unknown"
		resp.Reason = "invalid_signature"
		writeAuditJSON(w, http.StatusOK, resp)
		return
	}
	verification, err := verifyReceiptTrustSignature(r.Context(), d, canonical, sig, string(receipt.SignerFingerprint))
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "receipt_pubkey_registry_error", err.Error())
		return
	}
	resp.SignatureValid = verification.Valid
	resp.KeyStatus = verification.KeyStatus
	resp.Reason = verification.Reason
	resp.Status = "mismatch"
	if verification.Valid {
		if verification.Reason == "key_revoked" {
			resp.Status = "unverified"
			resp.Reason = "key_revoked"
		} else {
			resp.Valid = true
			resp.Status = "signed-only"
			resp.Reason = ""
		}
	}
	writeAuditJSON(w, http.StatusOK, resp)
}

func storedReceiptForVerify(ctx context.Context, reader CostReceiptReader, pathID string, ident sessionauth.SessionIdentity) (*audit.CostReceipt, error) {
	if strings.HasPrefix(strings.TrimSpace(pathID), "receipt_") {
		if dr, ok := reader.(costReceiptDisplayReader); ok {
			return dr.GetReceiptByDisplayID(ctx, strings.TrimSpace(pathID), ident.TenantID, ident.UserID)
		}
		return nil, audit.ErrReceiptNotFound
	}
	requestID, sequence, hasSequence := receiptLookupParts(pathID)
	if hasSequence {
		if sr, ok := reader.(costReceiptSequenceReader); ok {
			receipt, err := sr.GetReceiptBySequence(ctx, requestID, ident.TenantID, sequence)
			if err != nil {
				return nil, err
			}
			if receipt == nil || receipt.UserID != ident.UserID || receipt.UserID <= 0 {
				return nil, audit.ErrReceiptNotFound
			}
			return receipt, nil
		}
	}
	receipt, err := reader.GetReceiptForUser(ctx, requestID, ident.TenantID, ident.UserID)
	if err != nil {
		return nil, err
	}
	if hasSequence && receipt.ReceiptSequence != sequence {
		return nil, audit.ErrReceiptNotFound
	}
	return receipt, nil
}

func receiptLookupParts(pathID string) (string, int32, bool) {
	pathID = strings.TrimSpace(pathID)
	idx := strings.LastIndex(pathID, ":")
	if idx <= 0 || idx == len(pathID)-1 {
		return pathID, 0, false
	}
	seq, err := strconv.ParseInt(pathID[idx+1:], 10, 32)
	if err != nil || seq < 0 {
		return pathID, 0, false
	}
	return pathID[:idx], int32(seq), true
}

func canonicalBytesFromCostReceipt(receipt *audit.CostReceipt) ([]byte, error) {
	return audit.FinalTrustReceiptCanonical(receipt)
}

func receiptSignatureBytes(signature []byte) ([]byte, error) {
	raw := []byte(strings.TrimSpace(string(signature)))
	if decoded, err := base64.StdEncoding.DecodeString(string(raw)); err == nil && len(decoded) == ed25519.SignatureSize {
		return decoded, nil
	}
	if len(raw) == ed25519.SignatureSize {
		return append([]byte(nil), raw...), nil
	}
	return nil, errors.New("invalid ed25519 signature")
}

func verifyReceiptTrustSignature(ctx context.Context, d CostReceiptHandlerDeps, canonical []byte, sig []byte, fingerprint string) (auditledger.SignatureVerification, error) {
	normalizedFingerprint := strings.TrimSpace(fingerprint)
	if d.PubkeyRegistry != nil {
		verification, err := auditledger.VerifySignatureWithRegistry(ctx, d.PubkeyRegistry, canonical, sig, []byte(normalizedFingerprint))
		if errors.Is(err, auditledger.ErrInvalidPubkeyFingerprint) {
			return auditledger.SignatureVerification{Valid: false, KeyStatus: "unknown", Reason: "unknown_signer"}, nil
		}
		if err != nil || !verification.Valid {
			return verification, err
		}
		return applyReceiptRevocationOverlay(d, verification, normalizedFingerprint)
	}
	if d.Signer == nil || normalizedFingerprint != d.Signer.Fingerprint() {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: "unknown", Reason: "unknown_signer"}, nil
	}
	if sign.Verify(d.Signer.PublicKey(), canonical, sig) != nil {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: "active", Reason: "signature_mismatch"}, nil
	}
	return applyReceiptRevocationOverlay(d, auditledger.SignatureVerification{Valid: true, KeyStatus: "active"}, normalizedFingerprint)
}

func applyReceiptRevocationOverlay(d CostReceiptHandlerDeps, verification auditledger.SignatureVerification, fingerprint string) (auditledger.SignatureVerification, error) {
	revocations, err := receiptRevocationsFromDeps(d.Revocations)
	if err != nil {
		return auditledger.SignatureVerification{}, err
	}
	if _, ok := revocations.Lookup(fingerprint); ok {
		verification.KeyStatus = "revoked"
		verification.Reason = "key_revoked"
	}
	return verification, nil
}

func receiptRevocationsFromDeps(revocations trusthttp.Revocations) (trusthttp.Revocations, error) {
	if revocations != nil {
		return revocations, nil
	}
	return trusthttp.LoadRevocationsFromEnv()
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

func canonicalPayloadFromUserReceipt(receipt UserCostReceipt) audit.ReceiptCanonicalPayload {
	return audit.ReceiptCanonicalPayload{
		SchemaVersion:       receiptSchemaVersion(receipt),
		RequestID:           strings.TrimSpace(receipt.RequestID),
		ReceiptSequence:     receipt.ReceiptSequence,
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
