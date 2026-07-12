package auditexporthttp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/exporthttp"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
)

const (
	defaultMaxRows      = 10000
	auditProofFilename  = "audit-proof-%s.json"
	auditExportFilename = "audit-export.json"
)

var (
	minAuditExportTime = time.Time{}
	maxAuditExportTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
)

type Ledger interface {
	GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error)
	ListByRange(context.Context, string, time.Time, time.Time, int) ([]auditledger.LedgerEntry, error)
	ListByRequestIDs(context.Context, string, []string, int) ([]auditledger.LedgerEntry, error)
	LatestMerkleRoot(context.Context) ([32]byte, error)
}

type Deps struct {
	Ledger   Ledger
	Registry auditledger.PubkeyRegistry
	// Revocations 为已吊销的 audit signing key 集合;为 nil 时回退到 LoadRevocationsFromEnv
	// (运维登记的 HUAKAI_TRUST_REVOKED_KEYS_JSON/FILE),与 audit verify / trust-receipt 同一来源。
	Revocations trusthttp.Revocations

	MaxRows int
}

// resolveRevocations 解析本次导出/证明使用的吊销表:deps 显式注入优先,未配置(nil)回退 env。
// env 读取失败返回 error,由调用方失败安全处理(绝不静默跳过吊销检查)。
func (d Deps) resolveRevocations() (trusthttp.Revocations, error) {
	if d.Revocations != nil {
		return d.Revocations, nil
	}
	return trusthttp.LoadRevocationsFromEnv()
}

type ExportBundle struct {
	Entries          []gatewayhttp.AuditVerifyResponse `json:"entries"`
	ChainEntries     []gatewayhttp.AuditVerifyResponse `json:"chain_entries"`
	LatestMerkleRoot string                            `json:"latest_merkle_root"`
	Pubkeys          []PubkeyJSON                      `json:"pubkeys"`
	SelfAttestation  SelfAttestationJSON               `json:"self_attestation"`
	Request          ExportRequestJSON                 `json:"request"`
}

type PubkeyJSON struct {
	Algorithm         string `json:"algorithm"`
	Fingerprint       string `json:"fingerprint"`
	PubkeyFingerprint string `json:"pubkey_fingerprint"`
	PublicKeyBase64   string `json:"public_key_base64"`
	KeyStatus         string `json:"key_status,omitempty"`
	EffectiveFrom     string `json:"effective_from,omitempty"`
	EffectiveTo       string `json:"effective_to,omitempty"`
}

type SelfAttestationJSON struct {
	ChainValid         bool   `json:"chain_valid"`
	VerifiedEntryCount int    `json:"verified_entry_count"`
	SelectedEntryCount int    `json:"selected_entry_count"`
	Method             string `json:"method"`
}

type ExportRequestJSON struct {
	TenantScopeRef string   `json:"tenant_scope_ref"`
	From           string   `json:"from,omitempty"`
	To             string   `json:"to,omitempty"`
	RequestIDs     []string `json:"request_ids,omitempty"`
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/proof/{request_id}.json", NewProofDownloadHandler(d))
	r.Get("/export", NewExportHandler(d))
}

// authorizedTenantScope 从**认证会话上下文**派生本租户的 tenant_scope_ref,并据此堵住审计导出的
// 跨租户 IDOR:租户授权范围一律取自已认证身份,绝不让请求里的 tenant_scope_ref 决定范围
// (它是按 tenant_id 离线可枚举的派生值,不是凭证)。这是失败闭合的主防线——即便路由层漏挂认证
// 中间件,处理器自身也拒绝无会话请求。
//   - 无会话 / 身份非法(tenant_id<=0)→ 写 401,返回 ok=false;
//   - 请求显式带了 tenant_scope_ref 且与认证身份派生值不一致 → 写 403(拒跨租户探测),返回 ok=false;
//   - 否则返回认证身份派生的 scope_ref(供后续 ledger 查询作为唯一授权范围)。
func authorizedTenantScope(w http.ResponseWriter, r *http.Request) (string, bool) {
	ident, ok := sessionauth.SessionFromContext(r.Context())
	if !ok || ident.TenantID <= 0 {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return "", false
	}
	authScope := auditledger.TenantScopeRef(ident.TenantID)
	if authScope == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return "", false
	}
	if supplied := strings.TrimSpace(r.URL.Query().Get("tenant_scope_ref")); supplied != "" && supplied != authScope {
		writeJSONError(w, http.StatusForbidden, "tenant_scope_forbidden", "tenant_scope_ref does not match the authenticated tenant")
		return "", false
	}
	return authScope, true
}

func NewProofDownloadHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		tenantScopeRef, ok := authorizedTenantScope(w, r)
		if !ok {
			return
		}
		requestID := strings.TrimSpace(chi.URLParam(r, "request_id"))
		if requestID == "" {
			writeJSONError(w, http.StatusBadRequest, "missing_request_id", "request_id required")
			return
		}
		if d.Ledger == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_ledger_not_configured", "audit ledger dependency unset")
			return
		}
		entry, err := d.Ledger.GetByRequestIDAndTenantScope(r.Context(), requestID, tenantScopeRef)
		if !writeLedgerLookupError(w, err) {
			return
		}
		if !gatewayhttp.AuditEntryMatchesTenantScope(entry, tenantScopeRef) {
			writeJSONError(w, http.StatusNotFound, "audit_entry_not_found", "request_id not found")
			return
		}
		revocations, rerr := d.resolveRevocations()
		if rerr != nil {
			// 吊销表加载失败时失败安全:绝不在跳过吊销检查的情况下出具证明。
			writeJSONError(w, http.StatusServiceUnavailable, "audit_revocations_error", "audit revocation list temporarily unavailable")
			return
		}
		resp := gatewayhttp.AuditVerifyResponseForEntry(r.Context(), entry, d.Registry, revocations)
		writeJSONAttachment(w, http.StatusOK, fmt.Sprintf(auditProofFilename, safeFilenamePart(requestID)), resp)
	}
}

func NewExportHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		tenantScopeRef, ok := authorizedTenantScope(w, r)
		if !ok {
			return
		}
		maxRows := d.maxRows()
		filter, ok := parseExportFilter(w, r, maxRows)
		if !ok {
			return
		}
		if d.Ledger == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "audit_ledger_not_configured", "audit ledger dependency unset")
			return
		}
		selected, chainEntries, reqMeta, err := collectExportEntries(r.Context(), d.Ledger, tenantScopeRef, filter, maxRows)
		if err != nil {
			writeExportReadError(w, err)
			return
		}
		if len(selected) > maxRows || len(chainEntries) > maxRows {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "audit_export_too_large", "audit export row limit exceeded")
			return
		}
		if err := auditledger.VerifyChain(chainEntries); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "audit_export_chain_invalid", "audit export chain failed self-attestation")
			return
		}
		bundle, err := buildBundle(r.Context(), d, selected, chainEntries, reqMeta)
		if err != nil {
			writeExportReadError(w, err)
			return
		}
		writeJSONAttachment(w, http.StatusOK, auditExportFilename, bundle)
	}
}

func (d Deps) maxRows() int {
	if d.MaxRows > 0 && d.MaxRows < defaultMaxRows {
		return d.MaxRows
	}
	return defaultMaxRows
}

type exportFilter struct {
	byRequestIDs bool
	from         time.Time
	to           time.Time
	requestIDs   []string
}

func parseExportFilter(w http.ResponseWriter, r *http.Request, maxRows int) (exportFilter, bool) {
	rawIDs := strings.TrimSpace(r.URL.Query().Get("request_ids"))
	hasRange := strings.TrimSpace(r.URL.Query().Get("from")) != "" || strings.TrimSpace(r.URL.Query().Get("to")) != ""
	if rawIDs != "" {
		if hasRange {
			writeJSONError(w, http.StatusBadRequest, "ambiguous_export_filter", "use either request_ids or from/to")
			return exportFilter{}, false
		}
		requestIDs, ok := parseRequestIDs(w, rawIDs, maxRows)
		if !ok {
			return exportFilter{}, false
		}
		return exportFilter{byRequestIDs: true, requestIDs: requestIDs}, true
	}
	window, ok := exporthttp.ParseExportRange(w, r)
	if !ok {
		return exportFilter{}, false
	}
	return exportFilter{from: window.From, to: window.To}, true
}

func parseRequestIDs(w http.ResponseWriter, raw string, maxRows int) ([]string, bool) {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		requestID := strings.TrimSpace(part)
		if requestID == "" {
			continue
		}
		if len(requestID) > 256 {
			writeJSONError(w, http.StatusBadRequest, "request_id_invalid", "request_ids entries must be 256 characters or less")
			return nil, false
		}
		if _, ok := seen[requestID]; ok {
			continue
		}
		seen[requestID] = struct{}{}
		out = append(out, requestID)
		if len(out) > maxRows {
			writeJSONError(w, http.StatusBadRequest, "request_ids_too_many", "request_ids exceeds audit export row limit")
			return nil, false
		}
	}
	if len(out) == 0 {
		writeJSONError(w, http.StatusBadRequest, "request_ids_required", "request_ids must include at least one request id")
		return nil, false
	}
	return out, true
}

func collectExportEntries(ctx context.Context, ledger Ledger, tenantScopeRef string, filter exportFilter, maxRows int) ([]auditledger.LedgerEntry, []auditledger.LedgerEntry, ExportRequestJSON, error) {
	if filter.byRequestIDs {
		selected, err := ledger.ListByRequestIDs(ctx, tenantScopeRef, filter.requestIDs, maxRows+1)
		if err != nil {
			return nil, nil, ExportRequestJSON{}, err
		}
		chainEntries, err := ledger.ListByRange(ctx, tenantScopeRef, minAuditExportTime, maxAuditExportTime, maxRows+1)
		if err != nil {
			return nil, nil, ExportRequestJSON{}, err
		}
		return selected, chainEntries, ExportRequestJSON{TenantScopeRef: tenantScopeRef, RequestIDs: filter.requestIDs}, nil
	}
	selected, err := ledger.ListByRange(ctx, tenantScopeRef, filter.from, filter.to, maxRows+1)
	if err != nil {
		return nil, nil, ExportRequestJSON{}, err
	}
	chainEntries, err := ledger.ListByRange(ctx, tenantScopeRef, minAuditExportTime, filter.to, maxRows+1)
	if err != nil {
		return nil, nil, ExportRequestJSON{}, err
	}
	return selected, chainEntries, ExportRequestJSON{
		TenantScopeRef: tenantScopeRef,
		From:           filter.from.UTC().Format(time.RFC3339),
		To:             filter.to.UTC().Format(time.RFC3339),
	}, nil
}

func buildBundle(ctx context.Context, d Deps, selected, chainEntries []auditledger.LedgerEntry, req ExportRequestJSON) (ExportBundle, error) {
	root, err := d.Ledger.LatestMerkleRoot(ctx)
	if err != nil {
		return ExportBundle{}, err
	}
	pubkeys, err := auditPubkeys(ctx, d.Registry)
	if err != nil {
		return ExportBundle{}, err
	}
	// 每次导出只解析一次吊销表(批量条目共用),失败则整单失败安全(由 NewExportHandler 转 503),
	// 绝不在跳过吊销检查的情况下出具自证导出。
	revocations, err := d.resolveRevocations()
	if err != nil {
		return ExportBundle{}, err
	}
	return ExportBundle{
		Entries:          verifyResponses(ctx, selected, d.Registry, revocations),
		ChainEntries:     verifyResponses(ctx, chainEntries, d.Registry, revocations),
		LatestMerkleRoot: rootHex(root),
		Pubkeys:          pubkeys,
		SelfAttestation: SelfAttestationJSON{
			ChainValid:         true,
			VerifiedEntryCount: len(chainEntries),
			SelectedEntryCount: len(selected),
			Method:             "auditledger.VerifyChain",
		},
		Request: req,
	}, nil
}

func verifyResponses(ctx context.Context, entries []auditledger.LedgerEntry, registry auditledger.PubkeyRegistry, revocations trusthttp.Revocations) []gatewayhttp.AuditVerifyResponse {
	out := make([]gatewayhttp.AuditVerifyResponse, 0, len(entries))
	for _, entry := range entries {
		out = append(out, gatewayhttp.AuditVerifyResponseForEntry(ctx, entry, registry, revocations))
	}
	return out
}

func auditPubkeys(ctx context.Context, registry auditledger.PubkeyRegistry) ([]PubkeyJSON, error) {
	keys, err := auditledger.ListPubkeys(ctx, registry)
	if err != nil {
		return nil, err
	}
	out := make([]PubkeyJSON, 0, len(keys))
	for _, key := range keys {
		if key == nil {
			continue
		}
		resp := PubkeyJSON{
			Algorithm:         key.Algorithm,
			Fingerprint:       string(key.Fingerprint),
			PubkeyFingerprint: string(key.Fingerprint),
			PublicKeyBase64:   base64.StdEncoding.EncodeToString(key.PublicKey),
			KeyStatus:         key.Status(),
		}
		if !key.EffectiveFrom.IsZero() {
			resp.EffectiveFrom = key.EffectiveFrom.UTC().Format(time.RFC3339)
		}
		if key.EffectiveTo != nil {
			resp.EffectiveTo = key.EffectiveTo.UTC().Format(time.RFC3339)
		}
		out = append(out, resp)
	}
	return out, nil
}

func writeLedgerLookupError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, auditledger.ErrLedgerEntryNotFound) {
		writeJSONError(w, http.StatusNotFound, "audit_entry_not_found", "request_id not found")
		return false
	}
	if errors.Is(err, auditledger.ErrLedgerEntryCorrupt) {
		writeJSONError(w, http.StatusInternalServerError, "ledger_corrupt", "audit ledger entry corrupt")
		return false
	}
	writeJSONError(w, http.StatusInternalServerError, "audit_ledger_error", "audit ledger temporarily unavailable")
	return false
}

func writeExportReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, auditledger.ErrLedgerEntryCorrupt) {
		writeJSONError(w, http.StatusInternalServerError, "ledger_corrupt", "audit ledger entry corrupt")
		return
	}
	writeJSONError(w, http.StatusServiceUnavailable, "audit_export_failed", "audit export temporarily unavailable")
}

func writeJSONAttachment(w http.ResponseWriter, status int, filename string, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func rootHex(root [32]byte) string {
	return hex.EncodeToString(root[:])
}

func safeFilenamePart(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "request"
	}
	var b strings.Builder
	for _, ch := range raw {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == '-', ch == '_', ch == '.':
			b.WriteRune(ch)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 96 {
			break
		}
	}
	if b.Len() == 0 {
		return "request"
	}
	return b.String()
}
