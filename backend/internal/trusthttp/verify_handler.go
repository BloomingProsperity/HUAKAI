package trusthttp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/trustreceipt"
)

const (
	verifyBodyMaxBytes = 10 * 1024
	anonVerifyLimit    = 60
)

type VerifyDeps struct {
	Signer      publicKeySigner
	Registry    auditledger.PubkeyRegistry
	Revocations Revocations
	Now         func() time.Time
}

type VerifyResponse struct {
	Valid          bool     `json:"valid"`
	Status         string   `json:"status"`
	SignatureValid bool     `json:"signature_valid"`
	KeyStatus      string   `json:"key_status"`
	Reason         string   `json:"reason,omitempty"`
	FieldsMismatch []string `json:"fields_mismatch,omitempty"`
	CanonicalHash  string   `json:"canonical_hash"`
	SchemaVersion  string   `json:"schema_version"`
}

type verifyRequest struct {
	Payload           json.RawMessage `json:"payload"`
	Signature         string          `json:"signature"`
	PubkeyFingerprint string          `json:"pubkey_fingerprint"`
}

type verifyHandler struct {
	deps    VerifyDeps
	limiter *ipRateLimiter
}

func NewVerifyHandler(d VerifyDeps) http.Handler {
	return &verifyHandler{
		deps:    d,
		limiter: newIPRateLimiter(anonVerifyLimit, time.Minute),
	}
}

func (h *verifyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeTrustJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if !h.limiter.Allow(clientIP(r), trustNow(h.deps.Now)) {
		writeTrustJSONError(w, http.StatusTooManyRequests, "rate_limited", "anonymous trust verify limit is 60/min per IP")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, verifyBodyMaxBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeTrustJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "trust verify body must be <= 10KB")
			return
		}
		writeTrustJSONError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return
	}
	resp := h.verify(r.Context(), raw)
	writeTrustJSON(w, http.StatusOK, resp)
}

func (h *verifyHandler) verify(ctx context.Context, raw []byte) VerifyResponse {
	var req verifyRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return VerifyResponse{Valid: false, Status: "missing", KeyStatus: "unknown", Reason: "invalid_json", SchemaVersion: trustSchemaVersion}
	}
	if len(bytes.TrimSpace(req.Payload)) == 0 || strings.TrimSpace(req.Signature) == "" || strings.TrimSpace(req.PubkeyFingerprint) == "" {
		return VerifyResponse{Valid: false, Status: "missing", KeyStatus: "unknown", Reason: "required_field_missing", SchemaVersion: trustSchemaVersion}
	}
	canonical, err := canonicalPayloadFromRequest(req.Payload)
	if err != nil {
		return VerifyResponse{Valid: false, Status: "missing", KeyStatus: "unknown", Reason: "payload_invalid", SchemaVersion: trustSchemaVersion}
	}
	sum := sha256.Sum256(canonical)
	canonicalHash := hex.EncodeToString(sum[:])

	key, keyStatus, reason, err := h.lookupKey(ctx, strings.TrimSpace(req.PubkeyFingerprint))
	if err != nil {
		return VerifyResponse{Valid: false, Status: "mismatch", SignatureValid: false, KeyStatus: "unknown", Reason: "unknown_signer", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	sig, err := decodeBase64Flexible(strings.TrimSpace(req.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return VerifyResponse{Valid: false, Status: "mismatch", SignatureValid: false, KeyStatus: keyStatus, Reason: "invalid_signature", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	if len(key.PublicKey) != ed25519.PublicKeySize || strings.ToLower(strings.TrimSpace(key.Algorithm)) != auditledger.AuditSignerAlgorithmEd25519 {
		return VerifyResponse{Valid: false, Status: "mismatch", SignatureValid: false, KeyStatus: keyStatus, Reason: "invalid_public_key", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), canonical, sig) {
		return VerifyResponse{Valid: false, Status: "mismatch", SignatureValid: false, KeyStatus: keyStatus, Reason: "signature_mismatch", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	// S1-031: 域分离 —— 签名有效 ≠「这是有效 HUAKAI trust receipt」。audit ledger 用
	// 同一 key 家族签 entry_hash / trust.ledger.v1 等载荷;若不校验被签 canonical 字节
	// 的语义域,任何被该 key 签过的 base64 字节都能拿到 signed-only=valid 的伪 receipt 判定。
	// 验签通过后再要求 canonical 确为 trust.receipt.v1;object 分支已在
	// trustReceiptFromJSONObject 强校验 schema_version,此处补齐 base64 分支(放在验签之后,
	// 不改变「篡改字节 → signature_mismatch」既有语义)。
	if err := requireCanonicalTrustReceipt(canonical); err != nil {
		// reason 复用 OpenAPI TrustVerifyResponse.reason 已声明的 payload_invalid(契约内),
		// status=unverified 表示「签名有效但载荷不是可验证的 trust receipt」(codex #8 P2:不引入未声明枚举值)。
		return VerifyResponse{Valid: false, Status: "unverified", SignatureValid: true, KeyStatus: keyStatus, Reason: "payload_invalid", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	if keyStatus == "revoked" {
		if reason == "" {
			reason = "key_revoked"
		}
		return VerifyResponse{Valid: false, Status: "unverified", SignatureValid: true, KeyStatus: keyStatus, Reason: reason, CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	return VerifyResponse{Valid: true, Status: "signed-only", SignatureValid: true, KeyStatus: keyStatus, CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
}

func (h *verifyHandler) lookupKey(ctx context.Context, fingerprint string) (*auditledger.Pubkey, string, string, error) {
	fp, err := normalizeFingerprintString(fingerprint)
	if err != nil {
		return nil, "unknown", "unknown_signer", err
	}
	var key *auditledger.Pubkey
	if h.deps.Registry != nil {
		key, err = auditledger.LookupPubkey(ctx, h.deps.Registry, []byte(fp))
		if errors.Is(err, auditledger.ErrPubkeyNotFound) || errors.Is(err, auditledger.ErrLedgerPubkeyNotFound) || errors.Is(err, auditledger.ErrInvalidPubkeyFingerprint) {
			return nil, "unknown", "unknown_signer", err
		}
		if err != nil {
			return nil, "unknown", "unknown_signer", err
		}
	} else if h.deps.Signer != nil && h.deps.Signer.Fingerprint() == fp {
		key, err = auditledger.PubkeyFromSigner(h.deps.Signer, trustNow(h.deps.Now))
		if err != nil {
			return nil, "unknown", "unknown_signer", err
		}
	} else {
		return nil, "unknown", "unknown_signer", auditledger.ErrPubkeyNotFound
	}
	keyStatus := key.Status()
	revocations, err := revocationsFromDeps(h.deps.Revocations)
	if err != nil {
		return nil, "unknown", "revocation_config_invalid", err
	}
	if _, ok := revocations.Lookup(fp); ok {
		return key, "revoked", "key_revoked", nil
	}
	return key, keyStatus, "", nil
}

// requireCanonicalTrustReceipt 确认 canonical 字节是一份 trust.receipt.v1,
// 为 base64 分支提供与 object 分支等同的域约束(S1-031)。object 分支由
// trustReceiptFromJSONObject 校验 schema_version;base64 分支历史上直接放行任意被签
// 字节,故在验签通过后调用此函数完成域分离。非 JSON(如 audit ledger 的 entry_hash
// 原始字节)或 schema_version 非 trust.receipt.v1(如 trust.ledger.v1)一律拒绝。
func requireCanonicalTrustReceipt(canonical []byte) error {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(canonical, &probe); err != nil {
		return fmt.Errorf("payload 不是 canonical trust receipt: %w", err)
	}
	if strings.TrimSpace(probe.SchemaVersion) != trustSchemaVersion {
		return fmt.Errorf("payload schema_version=%q, 期望 %s", probe.SchemaVersion, trustSchemaVersion)
	}
	return nil
}

func canonicalPayloadFromRequest(raw json.RawMessage) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("payload required")
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, err
		}
		return decodeBase64Flexible(encoded)
	}
	if raw[0] != '{' {
		return nil, errors.New("payload must be base64 string or object")
	}
	receipt, err := trustReceiptFromJSONObject(raw)
	if err != nil {
		return nil, err
	}
	return trustreceipt.Canonical(receipt)
}

type trustReceiptJSON struct {
	SchemaVersion             string                        `json:"schema_version"`
	RequestID                 string                        `json:"request_id"`
	ReceiptSequence           int                           `json:"receipt_sequence"`
	TenantScopeRef            string                        `json:"tenant_scope_ref"`
	OccurredAt                string                        `json:"occurred_at"`
	Provider                  string                        `json:"provider"`
	RequestedModel            string                        `json:"requested_model"`
	RoutedModel               string                        `json:"routed_model"`
	UpstreamModel             string                        `json:"upstream_model"`
	DeliveredModel            string                        `json:"delivered_model"`
	CostCents                 int64                         `json:"cost_cents"`
	TokenCounts               trustreceipt.TokenCounts      `json:"token_counts"`
	PriceSnapshot             trustReceiptPriceSnapshotJSON `json:"price_snapshot"`
	ValidationState           string                        `json:"validation_state"`
	RedactedMetadataAllowlist map[string]any                `json:"redacted_metadata_allowlist"`
}

type trustReceiptPriceSnapshotJSON struct {
	RateTableSnapshotID int64  `json:"rate_table_snapshot_id"`
	SnapshotVersion     string `json:"snapshot_version"`
	CurrencyCode        string `json:"currency_code"`
}

func trustReceiptFromJSONObject(raw []byte) (trustreceipt.TrustReceiptV1, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var in trustReceiptJSON
	if err := dec.Decode(&in); err != nil {
		return trustreceipt.TrustReceiptV1{}, err
	}
	if strings.TrimSpace(in.SchemaVersion) != trustSchemaVersion {
		return trustreceipt.TrustReceiptV1{}, fmt.Errorf("schema_version must be %s", trustSchemaVersion)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(in.OccurredAt))
	if err != nil && strings.TrimSpace(in.OccurredAt) != "" {
		return trustreceipt.TrustReceiptV1{}, err
	}
	metadata, err := normalizeMetadata(in.RedactedMetadataAllowlist)
	if err != nil {
		return trustreceipt.TrustReceiptV1{}, err
	}
	return trustreceipt.TrustReceiptV1{
		RequestID:       strings.TrimSpace(in.RequestID),
		ReceiptSequence: in.ReceiptSequence,
		TenantScopeRef:  strings.TrimSpace(in.TenantScopeRef),
		OccurredAt:      occurredAt.UTC(),
		Provider:        strings.TrimSpace(in.Provider),
		RequestedModel:  strings.TrimSpace(in.RequestedModel),
		RoutedModel:     strings.TrimSpace(in.RoutedModel),
		UpstreamModel:   strings.TrimSpace(in.UpstreamModel),
		DeliveredModel:  strings.TrimSpace(in.DeliveredModel),
		CostCents:       in.CostCents,
		TokenCounts:     in.TokenCounts,
		PriceSnapshot: trustreceipt.PriceSnapshot{
			RateTableSnapshotID: in.PriceSnapshot.RateTableSnapshotID,
			SnapshotVersion:     in.PriceSnapshot.SnapshotVersion,
			CurrencyCode:        in.PriceSnapshot.CurrencyCode,
		},
		ValidationState:           strings.TrimSpace(in.ValidationState),
		RedactedMetadataAllowlist: metadata,
	}, nil
}

func normalizeMetadata(in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for key, value := range in {
		switch v := value.(type) {
		case string, bool, int64:
			out[key] = v
		case json.Number:
			i, err := v.Int64()
			if err != nil {
				return nil, err
			}
			out[key] = i
		default:
			return nil, fmt.Errorf("unsupported metadata value %T", value)
		}
	}
	return out, nil
}

func decodeBase64Flexible(value string) ([]byte, error) {
	cleaned := strings.TrimSpace(value)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		out, err := encoding.DecodeString(cleaned)
		if err == nil {
			return out, nil
		}
	}
	return nil, errors.New("base64 decode failed")
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}

type ipRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]ipBucket
}

type ipBucket struct {
	start time.Time
	count int
}

func newIPRateLimiter(limit int, window time.Duration) *ipRateLimiter {
	return &ipRateLimiter{limit: limit, window: window, buckets: map[string]ipBucket{}}
}

func (l *ipRateLimiter) Allow(ip string, now time.Time) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[ip]
	if bucket.start.IsZero() || now.Sub(bucket.start) >= l.window {
		bucket = ipBucket{start: now, count: 0}
	}
	if bucket.count >= l.limit {
		l.buckets[ip] = bucket
		return false
	}
	bucket.count++
	l.buckets[ip] = bucket
	return true
}
