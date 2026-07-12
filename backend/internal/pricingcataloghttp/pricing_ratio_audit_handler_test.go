package pricingcataloghttp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/pricingcatalog"
)

// TestPricingRatioAuditVerify_OKWhenChainIntact 断言一条有效的链会返回
// ok=true 且没有出问题的记录 id。所防回归：处理器是否把 Store 的"干净"
// 结论原样转给运维人员。
func TestPricingRatioAuditVerify_OKWhenChainIntact(t *testing.T) {
	store := &fakeRatioStore{verifyResult: pricingcatalog.VerifyChainResult{OK: true}}
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: store,
	}, http.MethodGet, "/audit/verify?tenant_id=7", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if !store.verifyCalled {
		t.Fatalf("handler did not call Store.VerifyChain")
	}
	body := decodeAuditVerify(t, rec.Body.Bytes())
	if !body.OK {
		t.Fatalf("ok=%v want true body=%s", body.OK, rec.Body.String())
	}
	if body.RowID != 0 || body.Reason != "" {
		t.Fatalf("clean chain leaked row_id=%d reason=%q", body.RowID, body.Reason)
	}
}

// TestPricingRatioAuditVerify_ReportsTamperedRow 是具备判别力的用例：
// 当 Store 报告链已损坏时，处理器必须返回 ok=false 以及出问题的记录 id。
// 一个把 ok 硬编码为 true 的处理器（即变异）会让本测试失败。
// 所防回归：处理器是否丢弃/忽略了 Store 的"被篡改"结论。
func TestPricingRatioAuditVerify_ReportsTamperedRow(t *testing.T) {
	store := &fakeRatioStore{verifyResult: pricingcatalog.VerifyChainResult{
		OK:     false,
		RowID:  42,
		Reason: "entry_hash mismatch",
	}}
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: store,
	}, http.MethodGet, "/audit/verify?tenant_id=7", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	body := decodeAuditVerify(t, rec.Body.Bytes())
	if body.OK {
		t.Fatalf("tampered chain reported ok=true body=%s", rec.Body.String())
	}
	if body.RowID != 42 {
		t.Fatalf("row_id=%d want 42 (offending row) body=%s", body.RowID, rec.Body.String())
	}
	if body.Reason != "entry_hash mismatch" {
		t.Fatalf("reason=%q want entry_hash mismatch", body.Reason)
	}
}

// TestPricingRatioAuditVerify_NonAdminIs403 确认该证明接口复用了平台管理员门槛；
// 租户运维人员不得运行链校验。
func TestPricingRatioAuditVerify_NonAdminIs403(t *testing.T) {
	store := &fakeRatioStore{verifyResult: pricingcatalog.VerifyChainResult{OK: true}}
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RoleTenantOperator, ScopeTenantID: 7}},
		Store: store,
	}, http.MethodGet, "/audit/verify?tenant_id=7", "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
	if store.verifyCalled {
		t.Fatalf("tenant_operator reached Store.VerifyChain")
	}
	assertErrorCode(t, rec, "admin_forbidden")
}

// TestPricingRatioAuditVerify_ResponseMasksSecrets 证明证明响应体只暴露安全的
// 几个字段（object/ok/row_id/reason），绝不泄露签名、哈希、密钥材料等审计机密。
func TestPricingRatioAuditVerify_ResponseMasksSecrets(t *testing.T) {
	store := &fakeRatioStore{verifyResult: pricingcatalog.VerifyChainResult{
		OK:     false,
		RowID:  9,
		Reason: "signature mismatch",
	}}
	rec := doPricingRatioRequest(t, AdminPricingRatioDeps{
		Auth:  fakeAdminAuth{ident: admin.AdminIdentity{TokenID: 1, Role: admin.RolePlatformAdmin}},
		Store: store,
	}, http.MethodGet, "/audit/verify?tenant_id=7", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// 最强的脱敏保证：响应体只携带这几个安全字段。多出任何一个键都意味着
	// 有 hash/签名/key-id 字段泄露出来了。
	//（reason 是类似 "signature mismatch" 的简短诊断标签——它指明的是失败
	// 类别，绝不会带出底层字节。）
	allowed := map[string]struct{}{"object": {}, "ok": {}, "row_id": {}, "reason": {}}
	for k := range raw {
		if _, ok := allowed[k]; !ok {
			t.Fatalf("unexpected field %q in attestation body=%s", k, rec.Body.String())
		}
	}
	// reason 必须保持为简短标签，而不能是内嵌的 hash/签名转储。
	if reason, ok := raw["reason"].(string); ok && len(reason) > 64 {
		t.Fatalf("reason field suspiciously long (%d chars); may embed secret bytes: %q", len(reason), reason)
	}
}

func decodeAuditVerify(t *testing.T, raw []byte) ratioAuditVerifyResponse {
	t.Helper()
	var body ratioAuditVerifyResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode attestation: %v", err)
	}
	return body
}
