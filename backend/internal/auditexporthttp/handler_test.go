package auditexporthttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/trusthttp"
)

func TestAuditProofDownload_Attachment(t *testing.T) {
	// Mutation: remove Content-Disposition from the proof download writer; this
	// test fails because the response is no longer a browser download.
	ctx := context.Background()
	ledger, registry := newAuditExportTestLedger(t)
	entry := appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{
		RequestID: "req_proof_download",
		TenantID:  7,
		Timestamp: "2026-06-02T10:00:00Z",
		HopChain:  []proto.HopAttestation{{Hop: proto.HopIngress, Timestamp: "2026-06-02T10:00:00Z"}},
	})

	rec := invokeAuditProof(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/proof/"+entry.RequestID+".json?tenant_scope_ref="+auditledger.TenantScopeRef(7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="audit-proof-req_proof_download.json"` {
		t.Fatalf("Content-Disposition=%q want audit proof attachment", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}

	verifyRec := httptest.NewRecorder()
	verifyReq := httptest.NewRequest(http.MethodGet, "/v1/audit/verify?request_id="+entry.RequestID+"&tenant_scope_ref="+auditledger.TenantScopeRef(7), nil)
	gatewayhttp.NewAuditVerifyHandler(gatewayhttp.AuditVerifyStaticDeps{Ledger: ledger, Registry: registry})(verifyRec, verifyReq.WithContext(ctx))
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verifyRec.Code, verifyRec.Body.String())
	}
	got := decodeVerifyResponse(t, rec.Body.Bytes())
	want := decodeVerifyResponse(t, verifyRec.Body.Bytes())
	if got.LedgerEntry.RequestID != want.LedgerEntry.RequestID ||
		got.LedgerEntry.TenantScopeRef != want.LedgerEntry.TenantScopeRef ||
		got.ChainProof.MerkleRoot != want.ChainProof.MerkleRoot ||
		got.ChainProof.Signature != want.ChainProof.Signature ||
		got.ChainProof.PubkeyFingerprint != want.ChainProof.PubkeyFingerprint {
		t.Fatalf("proof download body differs from verify body\ngot=%+v\nwant=%+v", got, want)
	}
}

func TestAuditExport_RangeBundleSelfAttests(t *testing.T) {
	// Mutation: skip auditledger.VerifyChain before writing the bundle, or include
	// a tampered ledger entry; this test fails because the tampered export must
	// return 500 instead of a self-attesting attachment.
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_export_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_export_2", TenantID: 7, Timestamp: "2026-06-02T10:05:00Z"})

	rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="audit-export.json"` {
		t.Fatalf("Content-Disposition=%q want audit export attachment", got)
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_export_1", "req_export_2"}) {
		t.Fatalf("entries=%v want exported range entries", got)
	}
	if len(bundle.Pubkeys) == 0 {
		t.Fatalf("bundle missing pubkey metadata: %+v", bundle)
	}
	if !bundle.SelfAttestation.ChainValid {
		t.Fatalf("self attestation not marked valid: %+v", bundle.SelfAttestation)
	}
	if err := auditledger.VerifyChain(bundleLedgerEntries(t, bundle.ChainEntries)); err != nil {
		t.Fatalf("bundle chain entries do not self-attest: %v", err)
	}

	tampered := &tamperingRangeLedger{Ledger: ledger}
	badRec := invokeAuditExport(t, Deps{Ledger: tampered, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if badRec.Code != http.StatusInternalServerError {
		t.Fatalf("tampered status=%d want 500 body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestAuditExport_TenantScoped(t *testing.T) {
	// Mutation: drop tenant_scope_ref filtering in the range reader; this test
	// fails because tenant B's request appears in tenant A's export bundle.
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_tenant_a_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_tenant_a_2", TenantID: 7, Timestamp: "2026-06-02T10:05:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_tenant_b", TenantID: 8, Timestamp: "2026-06-02T10:10:00Z"})

	rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_tenant_a_1", "req_tenant_a_2"}) {
		t.Fatalf("tenant scoped entries=%v want only tenant A", got)
	}
	for _, entry := range bundle.ChainEntries {
		if entry.LedgerEntry.TenantScopeRef != auditledger.TenantScopeRef(7) {
			t.Fatalf("chain proof leaked non-tenant-A entry: %+v", entry.LedgerEntry)
		}
	}
}

func TestAuditExport_RangeValidation(t *testing.T) {
	// Mutation: remove from>to or 366-day range checks; these requests return
	// 200/503 instead of the required public 400 validation errors.
	ledger, registry := newAuditExportTestLedger(t)
	cases := []string{
		"/v1/audit/export?tenant_scope_ref=" + auditledger.TenantScopeRef(7) + "&from=2026-06-03T00:00:00Z&to=2026-06-02T00:00:00Z",
		"/v1/audit/export?tenant_scope_ref=" + auditledger.TenantScopeRef(7) + "&from=2025-01-01T00:00:00Z&to=2026-06-02T00:00:00Z",
	}
	for _, target := range cases {
		rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d want 400 body=%s", target, rec.Code, rec.Body.String())
		}
	}
}

func TestAuditExport_RequestIDsBundleFiltersAndAttests(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_id_a_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_id_a_2", TenantID: 7, Timestamp: "2026-06-02T10:05:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_id_b", TenantID: 8, Timestamp: "2026-06-02T10:10:00Z"})

	rec := invokeAuditExport(t, Deps{Ledger: ledger, Registry: registry}, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&request_ids=req_id_b,req_id_a_2,req_id_a_1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_id_a_1", "req_id_a_2"}) {
		t.Fatalf("request id entries=%v want tenant A matches in chain order", got)
	}
	if err := auditledger.VerifyChain(bundleLedgerEntries(t, bundle.ChainEntries)); err != nil {
		t.Fatalf("request id chain entries do not self-attest: %v", err)
	}
}

// revocationsForFingerprint 构造一个把指定 fingerprint 登记为已吊销的吊销表(测试用)。
func revocationsForFingerprint(t testing.TB, fingerprint string) trusthttp.Revocations {
	t.Helper()
	revocations, err := trusthttp.ParseRevocationsJSON([]byte(`{"revoked":[{"fingerprint":"` + fingerprint + `","reason_class":"key_compromise"}]}`))
	if err != nil {
		t.Fatalf("ParseRevocationsJSON: %v", err)
	}
	return revocations
}

// TestAuditExport_RevokedKeyDowngradedToKeyRevoked 守审计 wy94u3tn9 最后一个 S1:当某条目的 audit
// signing key 已被吊销时,导出 bundle 必须把该条目降级为 key_revoked(KeyStatus=revoked、
// SignatureValid=false),而不是因签名密码学有效就报通过。判别(变异):删 verifyAuditLedgerEntrySignature
// 里的吊销 overlay → 吊销 key 的条目 KeyStatus 仍 active / SignatureValid=true → 本测试变红。
func TestAuditExport_RevokedKeyDowngradedToKeyRevoked(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	entry := appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_revoked", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry, Revocations: revocationsForFingerprint(t, entry.PubkeyFingerprint)}

	rec := invokeAuditExport(t, deps, "/v1/audit/export?from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if len(bundle.Entries) != 1 {
		t.Fatalf("entries=%d want 1", len(bundle.Entries))
	}
	cp := bundle.Entries[0].ChainProof
	if cp.KeyStatus != "revoked" {
		t.Fatalf("KeyStatus=%q want revoked(吊销 key 未降级=验签忽略吊销 S1)", cp.KeyStatus)
	}
	if cp.SignatureValid == nil || *cp.SignatureValid {
		t.Fatalf("SignatureValid=%v want false(吊销 key 即便签名有效也不可信)", cp.SignatureValid)
	}
	if cp.Reason != "key_revoked" {
		t.Fatalf("Reason=%q want key_revoked", cp.Reason)
	}
}

// TestAuditExport_NonRevokedKeyStaysValid 守"只在 fingerprint 命中吊销时才降级":把一个**别的**
// fingerprint 登记为吊销,本条目的 key 未吊销 → 仍应 SignatureValid=true、KeyStatus 非 revoked。
// 判别(变异):若吊销 overlay 无条件降级 → 未吊销 key 也被误判 → 本测试变红。
func TestAuditExport_NonRevokedKeyStaysValid(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_ok", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry, Revocations: revocationsForFingerprint(t, "deadbeefdeadbeef")}

	rec := invokeAuditExport(t, deps, "/v1/audit/export?from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	cp := bundle.Entries[0].ChainProof
	if cp.KeyStatus == "revoked" || cp.Reason == "key_revoked" {
		t.Fatalf("未吊销 key 被误判 revoked:KeyStatus=%q Reason=%q", cp.KeyStatus, cp.Reason)
	}
	if cp.SignatureValid == nil || !*cp.SignatureValid {
		t.Fatalf("SignatureValid=%v want true(未吊销有效 key)", cp.SignatureValid)
	}
}

// TestAuditProof_RevokedKeyDowngraded 守 proof 下载路径同样应用吊销降级。
// 判别(变异):删吊销 overlay → proof 对吊销 key 仍 SignatureValid=true → 本测试变红。
func TestAuditProof_RevokedKeyDowngraded(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	entry := appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_proof_revoked", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry, Revocations: revocationsForFingerprint(t, entry.PubkeyFingerprint)}

	rec := invokeAuditProof(t, deps, "/v1/audit/proof/"+entry.RequestID+".json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp gatewayhttp.AuditVerifyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode proof: %v", err)
	}
	if resp.ChainProof.KeyStatus != "revoked" || resp.ChainProof.Reason != "key_revoked" {
		t.Fatalf("proof KeyStatus=%q Reason=%q want revoked/key_revoked", resp.ChainProof.KeyStatus, resp.ChainProof.Reason)
	}
	if resp.ChainProof.SignatureValid == nil || *resp.ChainProof.SignatureValid {
		t.Fatalf("proof SignatureValid=%v want false", resp.ChainProof.SignatureValid)
	}
}

// TestAuditExport_RevocationLoadFailureFailsSafe 守"绝不静默跳过吊销检查"这一安全核心:当吊销表
// 来源(env)配置非法、加载失败时,导出必须失败安全返回 503,而不是当作"无吊销"照常出具自证导出。
// 判别(变异):把 resolveRevocations 改成加载失败时吞掉 error、返回空表继续 → 导出又返回 200 →
// 本测试变红(吊销机制将对 audit-ledger 形同虚设)。
func TestAuditExport_RevocationLoadFailureFailsSafe(t *testing.T) {
	// 故意把吊销表 JSON env 配成非法,使 LoadRevocationsFromEnv 报错。
	t.Setenv(trusthttp.TrustRevocationsJSONEnv, "{not valid json")
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_failsafe", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry} // Revocations 为 nil → 回退到(已被污染的)env

	rec := invokeAuditExport(t, deps, "/v1/audit/export?from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("吊销表加载失败应失败安全 503,实际 status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func newAuditExportTestLedger(t testing.TB) (*auditledger.MemoryLedger, auditledger.PubkeyRegistry) {
	t.Helper()
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	key, err := auditledger.PubkeyFromSigner(signer, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("pubkey from signer: %v", err)
	}
	return ledger, auditledger.NewMemoryPubkeyRegistry(key)
}

func appendAuditExportEntry(t testing.TB, ledger *auditledger.MemoryLedger, entry auditledger.LedgerEntry) auditledger.LedgerEntry {
	t.Helper()
	prepared, err := auditledger.PrepareEntry(context.Background(), entry)
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	out, err := ledger.Append(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	return out
}

// TestAuditExport_UnauthenticatedRejected 守审计 wy94u3tn9 的 S0:未认证请求绝不能导出任何审计链。
// 处理器从认证会话上下文派生租户,无会话即 401(失败闭合)——即便带上按 tenant_id 离线可枚举的
// scope_ref 也无济于事。判别(变异):删 authorizedTenantScope 的 SessionFromContext 401 守卫 →
// 未认证又能拿到 200 + 整条链 → 本测试 export/proof 两条断言变红。
func TestAuditExport_UnauthenticatedRejected(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_secret", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry}

	exportRec := invokeAuditExportAs(t, deps, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z", 0)
	if exportRec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证 export status=%d want 401 body=%s", exportRec.Code, exportRec.Body.String())
	}
	proofRec := invokeAuditProofAs(t, deps, "/v1/audit/proof/req_secret.json?tenant_scope_ref="+auditledger.TenantScopeRef(7), 0)
	if proofRec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证 proof status=%d want 401 body=%s", proofRec.Code, proofRec.Body.String())
	}
}

// TestAuditExport_CrossTenantScopeForbidden 守跨租户拒绝:认证为租户 8,却请求租户 7 的(离线枚举得到的)
// scope_ref → 必须 403,绝不能拿到租户 7 的链。判别(变异):删 authorizedTenantScope 的"supplied !=
// authScope → 403"守卫 → 租户 8 能拉租户 7 的审计链 → 本测试变红。
func TestAuditExport_CrossTenantScopeForbidden(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_victim", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry}

	exportRec := invokeAuditExportAs(t, deps, "/v1/audit/export?tenant_scope_ref="+auditledger.TenantScopeRef(7)+"&from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z", 8)
	if exportRec.Code != http.StatusForbidden {
		t.Fatalf("跨租户 export status=%d want 403 body=%s", exportRec.Code, exportRec.Body.String())
	}
	proofRec := invokeAuditProofAs(t, deps, "/v1/audit/proof/req_victim.json?tenant_scope_ref="+auditledger.TenantScopeRef(7), 8)
	if proofRec.Code != http.StatusForbidden {
		t.Fatalf("跨租户 proof status=%d want 403 body=%s", proofRec.Code, proofRec.Body.String())
	}
}

// TestAuditExport_DerivesScopeFromSession 守"授权范围取自认证身份,不信请求里的 scope_ref":即便请求
// 不带 tenant_scope_ref,认证为租户 7 也只导出租户 7 的链,租户 8 的条目绝不出现。判别(变异):若改成
// 信任请求里的 scope_ref / 不按认证身份派生 → 不带 scope_ref 时会失败或跨租户泄露 → 本测试变红。
func TestAuditExport_DerivesScopeFromSession(t *testing.T) {
	ledger, registry := newAuditExportTestLedger(t)
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_a_1", TenantID: 7, Timestamp: "2026-06-02T10:00:00Z"})
	appendAuditExportEntry(t, ledger, auditledger.LedgerEntry{RequestID: "req_b_1", TenantID: 8, Timestamp: "2026-06-02T10:05:00Z"})
	deps := Deps{Ledger: ledger, Registry: registry}

	rec := invokeAuditExportAs(t, deps, "/v1/audit/export?from=2026-06-02T00:00:00Z&to=2026-06-03T00:00:00Z", 7)
	if rec.Code != http.StatusOK {
		t.Fatalf("认证租户 7(不带 scope_ref)status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var bundle ExportBundle
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if got := verifyRequestIDs(bundle.Entries); !equalStrings(got, []string{"req_a_1"}) {
		t.Fatalf("entries=%v want 仅租户 7(scope 取自认证身份,不信请求)", got)
	}
}

func invokeAuditProof(t testing.TB, deps Deps, target string) *httptest.ResponseRecorder {
	// 既有用例默认以租户 7 的认证会话调用(与测试夹具里的 tenant_scope_ref=TenantScopeRef(7) 一致)。
	return invokeAuditProofAs(t, deps, target, 7)
}

// invokeAuditProofAs 以指定租户的认证会话注入上下文后调用证明下载;tenantID<=0 表示不注入会话(模拟未认证)。
func invokeAuditProofAs(t testing.TB, deps Deps, target string, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/audit", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = withAuditSession(req, tenantID)
	r.ServeHTTP(rec, req)
	return rec
}

// withAuditSession 在请求上下文里注入一个租户级认证会话;tenantID<=0 时原样返回(无会话=未认证)。
func withAuditSession(req *http.Request, tenantID int64) *http.Request {
	if tenantID <= 0 {
		return req
	}
	return req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: tenantID, UserID: 100}))
}

func invokeAuditExport(t testing.TB, deps Deps, target string) *httptest.ResponseRecorder {
	// 既有用例默认以租户 7 的认证会话调用(与测试夹具里的 tenant_scope_ref=TenantScopeRef(7) 一致)。
	return invokeAuditExportAs(t, deps, target, 7)
}

// invokeAuditExportAs 以指定租户的认证会话注入上下文后调用导出;tenantID<=0 表示不注入会话(模拟未认证)。
func invokeAuditExportAs(t testing.TB, deps Deps, target string, tenantID int64) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/audit", func(r chi.Router) {
		MountRoutes(r, deps)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req = withAuditSession(req, tenantID)
	r.ServeHTTP(rec, req)
	return rec
}

func decodeVerifyResponse(t testing.TB, body []byte) gatewayhttp.AuditVerifyResponse {
	t.Helper()
	var out gatewayhttp.AuditVerifyResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode verify response: %v", err)
	}
	return out
}

func verifyRequestIDs(entries []gatewayhttp.AuditVerifyResponse) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.LedgerEntry.RequestID)
	}
	return out
}

func bundleLedgerEntries(t testing.TB, entries []gatewayhttp.AuditVerifyResponse) []auditledger.LedgerEntry {
	t.Helper()
	out := make([]auditledger.LedgerEntry, 0, len(entries))
	for _, entry := range entries {
		prev, err := gatewayhttp.ParseAuditRootHex(entry.ChainProof.PrevMerkleRoot)
		if err != nil {
			t.Fatalf("parse prev root: %v", err)
		}
		root, err := gatewayhttp.ParseAuditRootHex(entry.ChainProof.MerkleRoot)
		if err != nil {
			t.Fatalf("parse root: %v", err)
		}
		out = append(out, auditledger.LedgerEntry{
			LedgerID:          entry.LedgerEntry.LedgerID,
			Timestamp:         entry.LedgerEntry.Timestamp,
			RequestID:         entry.LedgerEntry.RequestID,
			TenantScopeRef:    entry.LedgerEntry.TenantScopeRef,
			HopChain:          entry.LedgerEntry.HopChain,
			ModelChain:        entry.LedgerEntry.ModelChain,
			PrevMerkleRoot:    prev,
			MerkleRoot:        root,
			Signature:         entry.ChainProof.Signature,
			PubkeyFingerprint: entry.ChainProof.PubkeyFingerprint,
		})
	}
	return out
}

type tamperingRangeLedger struct {
	Ledger
}

func (l *tamperingRangeLedger) ListByRange(ctx context.Context, tenantScopeRef string, from, to time.Time, limit int) ([]auditledger.LedgerEntry, error) {
	rows, err := l.Ledger.ListByRange(ctx, tenantScopeRef, from, to, limit)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	rows[0].LedgerID = rows[0].LedgerID + "-tampered"
	return rows, nil
}

func (l *tamperingRangeLedger) ListByRequestIDs(ctx context.Context, tenantScopeRef string, requestIDs []string, limit int) ([]auditledger.LedgerEntry, error) {
	rows, err := l.Ledger.ListByRequestIDs(ctx, tenantScopeRef, requestIDs, limit)
	if err != nil || len(rows) == 0 {
		return rows, err
	}
	rows[0].LedgerID = rows[0].LedgerID + "-tampered"
	return rows, nil
}

func ledgerRequestIDs(entries []auditledger.LedgerEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.RequestID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
