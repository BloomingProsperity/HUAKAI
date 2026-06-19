package gatewayhttp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
)

type auditVerifyLedger interface {
	GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error)
	GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error)
	LatestMerkleRoot(context.Context) ([32]byte, error)
	Size(context.Context) int
}

// AuditVerifyDeps is the narrow dependency interface for user-facing audit
// verification endpoints. Tests and future Postgres wiring can inject any
// implementation with the same read surface.
type AuditVerifyDeps interface {
	AuditLedger() auditVerifyLedger
}

type AuditVerifyStaticDeps struct {
	Ledger   auditVerifyLedger
	Registry auditledger.PubkeyRegistry
	// Revocations 为已吊销的 audit signing key 集合;为 nil 时验签路径回退到
	// LoadRevocationsFromEnv 读运维登记的吊销表(与 trust-receipt 路径同一来源)。
	Revocations trusthttp.Revocations
}

func (d AuditVerifyStaticDeps) AuditLedger() auditVerifyLedger { return d.Ledger }
func (d AuditVerifyStaticDeps) AuditPubkeyRegistry() auditledger.PubkeyRegistry {
	return d.Registry
}
func (d AuditVerifyStaticDeps) AuditRevocations() trusthttp.Revocations { return d.Revocations }

// auditRevocationsFromDeps 解析本次验证使用的吊销表:优先取 deps 显式注入的集合,
// 未配置(nil)时回退到 LoadRevocationsFromEnv(运维登记的 HUAKAI_TRUST_REVOKED_KEYS_JSON/FILE)。
// 与 trust-receipt 路径共用同一吊销来源,确保 audit-ledger 与收据两个验证面的吊销判定一致。
// env 读取失败时返回 error,由调用方失败安全处理(绝不静默跳过吊销检查)。
func auditRevocationsFromDeps(d AuditVerifyDeps) (trusthttp.Revocations, error) {
	if d != nil {
		if provider, ok := d.(interface {
			AuditRevocations() trusthttp.Revocations
		}); ok {
			if revocations := provider.AuditRevocations(); revocations != nil {
				return revocations, nil
			}
		}
	}
	return trusthttp.LoadRevocationsFromEnv()
}

type AuditVerifyRouter interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
}

func MountAuditVerifyRoutes(r AuditVerifyRouter, d AuditVerifyDeps) {
	r.Get("/v1/audit/verify", NewAuditVerifyHandler(d))
	r.Post("/v1/audit/verify", NewAuditVerifyHandler(d))
	r.Get("/v1/audit/merkle-tree.json", NewAuditMerkleTreeHandler(d))
}

const auditVerifyBodyMaxBytes = 4 * 1024

type AuditVerifyRequest struct {
	RequestID      string `json:"request_id"`
	TenantScopeRef string `json:"tenant_scope_ref,omitempty"`
}

type AuditVerifyResponse struct {
	LedgerEntry AuditLedgerEntryJSON `json:"ledger_entry"`
	ChainProof  AuditChainProofJSON  `json:"chain_proof"`
}

type AuditLedgerEntryJSON struct {
	LedgerID       string                 `json:"ledger_id"`
	Timestamp      string                 `json:"timestamp"`
	RequestID      string                 `json:"request_id"`
	TenantID       int64                  `json:"-"`
	TenantScopeRef string                 `json:"tenant_scope_ref,omitempty"`
	HopChain       []proto.HopAttestation `json:"hop_chain"`
	ModelChain     *proto.ModelChain      `json:"model_chain,omitempty"`
}

type AuditChainProofJSON struct {
	PrevMerkleRoot    string `json:"prev_merkle_root"`
	MerkleRoot        string `json:"merkle_root"`
	Signature         string `json:"signature"`
	PubkeyFingerprint string `json:"pubkey_fingerprint"`
	SignatureValid    *bool  `json:"signature_valid,omitempty"`
	KeyStatus         string `json:"key_status,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

type AuditMerkleTreeResponse struct {
	LatestMerkleRoot string `json:"latest_merkle_root"`
	Size             int    `json:"size"`
}

func NewAuditVerifyHandler(d AuditVerifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := auditVerifyRequestFromHTTP(w, r)
		if !ok {
			return
		}
		ledger, ok := auditLedgerFromDeps(d)
		if !ok {
			writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_ledger_not_configured", "audit ledger dependency unset")
			return
		}
		if req.RequestID == "" {
			writeAuditJSONError(w, http.StatusBadRequest, "missing_request_id", "request_id required")
			return
		}
		tenantScopeRef := strings.TrimSpace(req.TenantScopeRef)
		if tenantScopeRef == "" {
			writeAuditJSONError(w, http.StatusBadRequest, "missing_tenant_scope_ref", "tenant_scope_ref required")
			return
		}
		entry, err := ledger.GetByRequestIDAndTenantScope(r.Context(), req.RequestID, tenantScopeRef)
		if errors.Is(err, auditledger.ErrLedgerEntryNotFound) {
			writeAuditJSONError(w, http.StatusNotFound, "audit_entry_not_found", "request_id not found")
			return
		}
		if errors.Is(err, auditledger.ErrLedgerEntryCorrupt) {
			_ = privacy.LogSystem(r.Context(), privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "gatewayhttp.audit_verify",
				RequestID:  req.RequestID,
				ErrorClass: privacy.ErrorClassFor(r.Context(), err),
				Attrs: map[string]any{
					"event_class":  "audit_verify_ledger_entry_corrupt",
					"reason_class": "ledger_corrupt",
				},
			})
			writeAuditJSONError(w, http.StatusInternalServerError, "ledger_corrupt", "audit ledger entry corrupt")
			return
		}
		if err != nil {
			_ = privacy.LogSystem(r.Context(), privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "gatewayhttp.audit_verify",
				RequestID:  req.RequestID,
				ErrorClass: privacy.ErrorClassFor(r.Context(), err),
				Attrs: map[string]any{
					"event_class":  "audit_verify_ledger_lookup_failed",
					"reason_class": "audit_ledger_error",
				},
			})
			writeAuditJSONError(w, http.StatusInternalServerError, "audit_ledger_error", "audit ledger temporarily unavailable")
			return
		}
		if !auditEntryMatchesTenantScope(entry, tenantScopeRef) {
			writeAuditJSONError(w, http.StatusNotFound, "audit_entry_not_found", "request_id not found")
			return
		}
		revocations, rerr := auditRevocationsFromDeps(d)
		if rerr != nil {
			// 吊销表加载失败时**失败安全**:拒绝出具"验证通过"结论,绝不静默跳过吊销检查
			// (否则吊销机制对 audit-ledger 形同虚设)。
			_ = privacy.LogSystem(r.Context(), privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "gatewayhttp.audit_verify",
				RequestID:  req.RequestID,
				ErrorClass: privacy.ErrorClassFor(r.Context(), rerr),
				Attrs: map[string]any{
					"event_class":  "audit_verify_revocations_load_failed",
					"reason_class": "audit_revocations_error",
				},
			})
			writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_revocations_error", "audit revocation list temporarily unavailable")
			return
		}
		writeAuditJSON(w, http.StatusOK, auditVerifyResponseWithRegistry(r.Context(), entry, auditPubkeyRegistryFromDeps(d), revocations))
	}
}

func auditEntryMatchesTenantScope(entry auditledger.LedgerEntry, tenantScopeRef string) bool {
	entryScope := entry.TenantScopeRef
	if entryScope == "" {
		entryScope = auditledger.TenantScopeRef(entry.TenantID)
	}
	return entryScope != "" && entryScope == tenantScopeRef
}

func AuditEntryMatchesTenantScope(entry auditledger.LedgerEntry, tenantScopeRef string) bool {
	return auditEntryMatchesTenantScope(entry, tenantScopeRef)
}

func auditVerifyRequestFromHTTP(w http.ResponseWriter, r *http.Request) (AuditVerifyRequest, bool) {
	switch r.Method {
	case http.MethodGet:
		return AuditVerifyRequest{
			RequestID:      r.URL.Query().Get("request_id"),
			TenantScopeRef: r.URL.Query().Get("tenant_scope_ref"),
		}, true
	case http.MethodPost:
		if r.ContentLength > auditVerifyBodyMaxBytes {
			writeAuditJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "audit verify body must be <= 4KB")
			return AuditVerifyRequest{}, false
		}
		r.Body = http.MaxBytesReader(w, r.Body, auditVerifyBodyMaxBytes)
		var req AuditVerifyRequest
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return req, true
			}
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeAuditJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "audit verify body must be <= 4KB")
				return AuditVerifyRequest{}, false
			}
			writeAuditJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return AuditVerifyRequest{}, false
		}
		var extra any
		if err := dec.Decode(&extra); err != nil {
			if errors.Is(err, io.EOF) {
				return req, true
			}
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeAuditJSONError(w, http.StatusRequestEntityTooLarge, "body_too_large", "audit verify body must be <= 4KB")
				return AuditVerifyRequest{}, false
			}
			writeAuditJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return AuditVerifyRequest{}, false
		}
		writeAuditJSONError(w, http.StatusBadRequest, "invalid_json", "audit verify body must contain a single JSON object")
		return AuditVerifyRequest{}, false
	default:
		writeAuditJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
		return AuditVerifyRequest{}, false
	}
}

func NewAuditMerkleTreeHandler(d AuditVerifyDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeAuditJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		ledger, ok := auditLedgerFromDeps(d)
		if !ok {
			writeAuditJSONError(w, http.StatusServiceUnavailable, "audit_ledger_not_configured", "audit ledger dependency unset")
			return
		}
		root, err := ledger.LatestMerkleRoot(r.Context())
		if err != nil {
			_ = privacy.LogSystem(r.Context(), privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "gatewayhttp.audit_verify",
				ErrorClass: privacy.ErrorClassFor(r.Context(), err),
				Attrs: map[string]any{
					"event_class":  "audit_merkle_root_lookup_failed",
					"reason_class": "audit_ledger_error",
				},
			})
			writeAuditJSONError(w, http.StatusInternalServerError, "audit_ledger_error", "audit ledger temporarily unavailable")
			return
		}
		writeAuditJSON(w, http.StatusOK, AuditMerkleTreeResponse{
			LatestMerkleRoot: rootHex(root),
			Size:             ledger.Size(r.Context()),
		})
	}
}

func auditLedgerFromDeps(d AuditVerifyDeps) (auditVerifyLedger, bool) {
	if d == nil {
		return nil, false
	}
	ledger := d.AuditLedger()
	return ledger, ledger != nil
}

func auditPubkeyRegistryFromDeps(d AuditVerifyDeps) auditledger.PubkeyRegistry {
	if d == nil {
		return nil
	}
	provider, ok := d.(interface {
		AuditPubkeyRegistry() auditledger.PubkeyRegistry
	})
	if !ok {
		return nil
	}
	return provider.AuditPubkeyRegistry()
}

func auditVerifyResponseWithRegistry(ctx context.Context, entry auditledger.LedgerEntry, registry auditledger.PubkeyRegistry, revocations trusthttp.Revocations) AuditVerifyResponse {
	resp := auditVerifyResponse(entry)
	if registry == nil {
		return resp
	}
	verification, err := verifyAuditLedgerEntrySignature(ctx, registry, entry, revocations)
	if err != nil {
		valid := false
		resp.ChainProof.SignatureValid = &valid
		resp.ChainProof.KeyStatus = "unknown"
		resp.ChainProof.Reason = "signature_verify_error"
		return resp
	}
	resp.ChainProof.SignatureValid = &verification.Valid
	resp.ChainProof.KeyStatus = verification.KeyStatus
	resp.ChainProof.Reason = verification.Reason
	return resp
}

func AuditVerifyResponseForEntry(ctx context.Context, entry auditledger.LedgerEntry, registry auditledger.PubkeyRegistry, revocations trusthttp.Revocations) AuditVerifyResponse {
	return auditVerifyResponseWithRegistry(ctx, entry, registry, revocations)
}

func auditVerifyResponse(entry auditledger.LedgerEntry) AuditVerifyResponse {
	scopeRef := entry.TenantScopeRef
	if scopeRef == "" {
		scopeRef = auditledger.TenantScopeRef(entry.TenantID)
	}
	return AuditVerifyResponse{
		LedgerEntry: AuditLedgerEntryJSON{
			LedgerID:       entry.LedgerID,
			Timestamp:      entry.Timestamp,
			RequestID:      entry.RequestID,
			TenantID:       entry.TenantID,
			TenantScopeRef: scopeRef,
			HopChain:       entry.HopChain,
			ModelChain:     entry.ModelChain,
		},
		ChainProof: AuditChainProofJSON{
			PrevMerkleRoot:    rootHex(entry.PrevMerkleRoot),
			MerkleRoot:        rootHex(entry.MerkleRoot),
			Signature:         entry.Signature,
			PubkeyFingerprint: entry.PubkeyFingerprint,
		},
	}
}

func verifyAuditLedgerEntrySignature(ctx context.Context, registry auditledger.PubkeyRegistry, entry auditledger.LedgerEntry, revocations trusthttp.Revocations) (auditledger.SignatureVerification, error) {
	entryHash, err := auditledger.EntryHash(&entry)
	if err != nil {
		return auditledger.SignatureVerification{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(entry.Signature)
	if err != nil {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: "unknown", Reason: "invalid_signature"}, nil
	}
	verification, err := auditledger.VerifySignatureWithRegistry(ctx, registry, entryHash[:], sig, []byte(entry.PubkeyFingerprint))
	if err != nil || !verification.Valid {
		return verification, err
	}
	key, err := auditledger.LookupPubkey(ctx, registry, []byte(entry.PubkeyFingerprint))
	if err != nil {
		return auditledger.SignatureVerification{}, err
	}
	entryTime, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: verification.KeyStatus, Reason: "invalid_entry_timestamp"}, nil
	}
	if signatureOutsideKeyWindow(entryTime, key) {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: key.Status(), Reason: "signature_outside_key_window"}, nil
	}
	// 吊销检查(防篡改的关键一环):签名密码学有效且在 key 有效窗口内,但若该 signing key 已被运维
	// 登记吊销(泄露后),持有泄露私钥者仍能伪造条目,故必须判为不可信——降级为 key_revoked。与
	// signature_outside_key_window 一样把 Valid 置 false(非密码学失效但整体不可信)。
	if _, revoked := revocations.Lookup(strings.TrimSpace(entry.PubkeyFingerprint)); revoked {
		return auditledger.SignatureVerification{Valid: false, KeyStatus: "revoked", Reason: "key_revoked"}, nil
	}
	return verification, nil
}

func signatureOutsideKeyWindow(ts time.Time, key *auditledger.Pubkey) bool {
	// 委托到 auditledger 的共享实现:receipt 验证路径与 audit-ledger 路径
	// 共用同一 key 有效窗口策略,单一真相源。
	return auditledger.SignatureOutsideKeyWindow(ts, key)
}

func rootHex(root [32]byte) string {
	return hex.EncodeToString(root[:])
}

func writeAuditJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAuditJSONError(w http.ResponseWriter, status int, code, message string) {
	writeAuditJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func ParseAuditRootHex(s string) ([32]byte, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return [32]byte{}, err
	}
	if len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("audit root must be 32 bytes, got %d", len(raw))
	}
	var out [32]byte
	copy(out[:], raw)
	return out, nil
}
