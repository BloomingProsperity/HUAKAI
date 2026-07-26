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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
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
	ClientIP    *clientip.Resolver
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
	if !h.limiter.Allow(h.deps.ClientIP.ClientIP(r), trustNow(h.deps.Now)) {
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
	// 域分离 —— 签名有效 ≠「这是有效 HUAKAI trust receipt」。audit ledger 用
	// 同一 key 家族签 entry_hash / trust.ledger.v1 等载荷;若不校验被签 canonical 字节
	// 的语义域,任何被该 key 签过的 base64 字节都能拿到 signed-only=valid 的伪 receipt 判定。
	// 验签通过后再要求 canonical 确为 trust.receipt.v1;object 分支已在
	// trustReceiptFromJSONObject 强校验 schema_version,此处补齐 base64 分支(放在验签之后,
	// 不改变「篡改字节 → signature_mismatch」既有语义)。
	occurredAt, perr := parseCanonicalTrustReceipt(canonical)
	if perr != nil {
		// reason 复用 OpenAPI TrustVerifyResponse.reason 已声明的 payload_invalid(契约内),
		// status=unverified 表示「签名有效但载荷不是可验证的 trust receipt」。
		return VerifyResponse{Valid: false, Status: "unverified", SignatureValid: true, KeyStatus: keyStatus, Reason: "payload_invalid", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	// 撤销优先于窗口校验:若 key 已 CRL 撤销(泄漏/作废),即便同时落在有效窗口外,也要
	// 如实报 revoked/key_revoked,保证撤销/泄漏对客户端与运维可见。
	if keyStatus == "revoked" {
		if reason == "" {
			reason = "key_revoked"
		}
		return VerifyResponse{Valid: false, Status: "unverified", SignatureValid: true, KeyStatus: keyStatus, Reason: reason, CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
	}
	// receipt 必须由签名时仍在有效窗口内的 key 签发。occurred_at 已知且落在 key
	// 有效窗口外 → 拒(堵泄漏旧 key 签新日期 receipt、未来 key 提前生效)。occurred_at 缺省
	// (零值)无法判定签名时刻,豁免以免误伤无 occurred_at 的旧 receipt。
	// 仅在使用真实 pubkey registry(带真实 EffectiveFrom/To)时强制窗口:signer-only 回退
	// 无 registry,lookupKey 会用验证时刻 fabricate EffectiveFrom,据此否决会误杀正常历史
	// receipt。
	if h.deps.Registry != nil && !occurredAt.IsZero() && auditledger.SignatureOutsideKeyWindow(occurredAt, key) {
		return VerifyResponse{Valid: false, Status: "unverified", SignatureValid: true, KeyStatus: key.Status(), Reason: "signature_outside_key_window", CanonicalHash: canonicalHash, SchemaVersion: trustSchemaVersion}
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

// parseCanonicalTrustReceipt 确认 canonical 字节是一份 trust.receipt.v1,并提取
// occurred_at 供 key 有效窗口校验使用(域分离 + 窗口校验)。object 分支由
// trustReceiptFromJSONObject 校验 schema_version;base64 分支历史上直接放行任意被签
// 字节,故在验签通过后调用此函数完成域分离。非 JSON(如 audit ledger 的 entry_hash
// 原始字节)或 schema_version 非 trust.receipt.v1(如 trust.ledger.v1)一律返回 error。
// occurred_at 缺省时返回零值 time(不报错),调用方据此豁免窗口校验。
func parseCanonicalTrustReceipt(canonical []byte) (time.Time, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
		OccurredAt    string `json:"occurred_at"`
	}
	if err := json.Unmarshal(canonical, &probe); err != nil {
		return time.Time{}, fmt.Errorf("payload 不是 canonical trust receipt: %w", err)
	}
	if strings.TrimSpace(probe.SchemaVersion) != trustSchemaVersion {
		return time.Time{}, fmt.Errorf("payload schema_version=%q, 期望 %s", probe.SchemaVersion, trustSchemaVersion)
	}
	if s := strings.TrimSpace(probe.OccurredAt); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return time.Time{}, fmt.Errorf("payload occurred_at 非法: %w", err)
		}
		return t.UTC(), nil
	}
	return time.Time{}, nil
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

// ipRateLimiterMaxBuckets 限定 buckets map 的条目上限。公开匿名端点(/v1/trust/verify)被大量不同
// 源 IP 打来时,若 buckets 只增不删会随独立 IP 数无界增长耗尽内存。与 cmd/gateway/rate_limit.go 的
// maxBucketsPerTier(50000)及 loginthrottle 的 MaxKeys 上限做法一致。
const ipRateLimiterMaxBuckets = 50000

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
	bucket, existed := l.buckets[ip]
	if bucket.start.IsZero() || now.Sub(bucket.start) >= l.window {
		bucket = ipBucket{start: now, count: 0}
	}
	// 内存卫生:此前 buckets 只增不删 → 无界增长 DoS。新建条目前若已达上限,先惰性清扫过期桶;清扫后
	// 仍满则整表丢弃(下个窗口重建,界定内存),镜像 rate_limit.go 的"满则 reset"。
	if !existed && len(l.buckets) >= ipRateLimiterMaxBuckets {
		l.evictExpiredLocked(now)
		if len(l.buckets) >= ipRateLimiterMaxBuckets {
			l.buckets = make(map[string]ipBucket)
		}
	}
	if bucket.count >= l.limit {
		l.buckets[ip] = bucket
		return false
	}
	bucket.count++
	l.buckets[ip] = bucket
	return true
}

// evictExpiredLocked 删除所有已过窗口的桶。调用方须持 l.mu。
func (l *ipRateLimiter) evictExpiredLocked(now time.Time) {
	for ip, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, ip)
		}
	}
}
