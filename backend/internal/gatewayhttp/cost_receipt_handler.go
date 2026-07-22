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

	"github.com/BloomingProsperity/HUAKAI/internal/audit"
	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
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
		valid = canonicalHashMatches && verification.Valid && !receiptVerificationRejected(verification.Reason)
		if verification.Valid && receiptVerificationRejected(verification.Reason) {
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
							if errors.Is(err, billing.ErrRefundNoCapturedCharge) {
								reason = "no_refundable_captured_charge"
								refundEventID = 0
							} else if errors.Is(err, billing.ErrRefundAmountNotCovered) {
								reason = "refund_exceeds_captured_charge"
								refundEventID = 0
							} else {
								writeJSONError(w, http.StatusServiceUnavailable, "refund_enqueue_failed", "mismatch refund could not be queued")
								return
							}
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
		if receiptVerificationRejected(verification.Reason) {
			resp.Status = "unverified"
			resp.Reason = verification.Reason
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

// receiptVerificationRejected 报告某 verification.Reason 是否表示「签名密码学有效但 receipt
// 不被采信」——目前为 key 撤销与签名落在 key 有效窗口外。两个 verify 调用点据此
// 统一判为 unverified、valid=false。
func receiptVerificationRejected(reason string) bool {
	return reason == "key_revoked" || reason == "signature_outside_key_window"
}

// receiptOccurredAtFromCanonical 从 canonical trust.receipt.v1 字节解析 occurred_at,供 key
// 有效窗口校验。缺省/不可解析返回 ok=false,调用方据此豁免窗口校验。
func receiptOccurredAtFromCanonical(canonical []byte) (time.Time, bool) {
	var probe struct {
		OccurredAt string `json:"occurred_at"`
	}
	if err := json.Unmarshal(canonical, &probe); err != nil {
		return time.Time{}, false
	}
	s := strings.TrimSpace(probe.OccurredAt)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
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
		verification, err = applyReceiptRevocationOverlay(d, verification, normalizedFingerprint)
		if err != nil || verification.Reason == "key_revoked" {
			return verification, err
		}
		// 强制签名 key 有效窗口。receipt 的 occurred_at(从 canonical trust.receipt.v1
		// 解析)须落在签名 key 的 [EffectiveFrom, EffectiveTo] 内,堵泄漏旧 key 签新日期 receipt。
		// 仅 registry 路径有真实窗口(signer-only 无 registry,见 trust verify 同款豁免);occurred_at
		// 缺省则豁免。窗口外保持 Valid=true(签名密码学有效),靠 Reason 让调用方判 unverified。
		if occurredAt, ok := receiptOccurredAtFromCanonical(canonical); ok {
			key, lerr := auditledger.LookupPubkey(ctx, d.PubkeyRegistry, []byte(normalizedFingerprint))
			if lerr != nil {
				return auditledger.SignatureVerification{}, lerr
			}
			if auditledger.SignatureOutsideKeyWindow(occurredAt, key) {
				verification.Reason = "signature_outside_key_window"
			}
		}
		return verification, nil
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
