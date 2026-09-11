package credentialstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

func TestRotateKeepsExistingRefreshWhenIncomingOmitsIt(t *testing.T) {
	const oldRefresh = "codex-refresh-keep"
	const newAccess = "codex-access-after-update"
	db := newCredentialAuditTxFakeDB()
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	created, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"codex-access-before","session_token":"codex-access-before","refresh_token":"` + oldRefresh + `"}`),
		ActorID: "owner",
	})
	if err != nil {
		t.Fatalf("Create 失败：%v", err)
	}

	rotated, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"` + newAccess + `","session_token":"` + newAccess + `"}`),
		ActorID: "owner",
	})
	if err != nil {
		t.Fatalf("仅访问令牌轮换失败：%v", err)
	}
	if rotated.Version != created.Version+1 {
		t.Fatalf("版本=%d，期望 %d", rotated.Version, created.Version+1)
	}

	resolved, err := store.ResolveActive(context.Background(), db.tenantID, db.providerAccountID)
	if err != nil {
		t.Fatalf("读取轮换后凭据失败：%v", err)
	}
	defer privacy.Zeroize(resolved.PlaintextPayload)
	var fields map[string]string
	if err := json.Unmarshal(resolved.PlaintextPayload, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["access_token"] != newAccess {
		t.Fatalf("访问令牌=%q，期望 %q；未改访问令牌则本测试失去判别力", fields["access_token"], newAccess)
	}
	if fields["refresh_token"] != oldRefresh {
		t.Fatalf("续期材料=%q，期望保留 %q；删掉轮换前合并后本断言变红", fields["refresh_token"], oldRefresh)
	}
	if strings.Contains(rotated.Vendor, oldRefresh) {
		t.Fatal("轮换元数据泄漏了续期材料")
	}
}

func TestRotateReplacesRefreshWhenIncomingProvidesIt(t *testing.T) {
	const newRefresh = "codex-refresh-replacement"
	db := newCredentialAuditTxFakeDB()
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	created, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"old","session_token":"old","refresh_token":"codex-refresh-old"}`),
	})
	if err != nil {
		t.Fatalf("Create 失败：%v", err)
	}
	if _, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"new","session_token":"new","refresh_token":"` + newRefresh + `"}`),
	}); err != nil {
		t.Fatalf("带新续期材料的轮换失败：%v", err)
	}
	resolved, err := store.ResolveActive(context.Background(), db.tenantID, db.providerAccountID)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	defer privacy.Zeroize(resolved.PlaintextPayload)
	if !strings.Contains(string(resolved.PlaintextPayload), newRefresh) ||
		strings.Contains(string(resolved.PlaintextPayload), "codex-refresh-old") {
		t.Fatalf("入站已带续期材料时必须覆盖旧值：%s", resolved.PlaintextPayload)
	}
}

func TestRotateDoesNotInventRefreshForAPIKey(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	created, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey,
		Payload: []byte(`{"api_key":"sk-before"}`),
	})
	if err != nil {
		t.Fatalf("Create 失败：%v", err)
	}
	if _, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"api_key":"sk-after"}`),
	}); err != nil {
		t.Fatalf("官方 Key 轮换失败：%v", err)
	}
	resolved, err := store.ResolveActive(context.Background(), db.tenantID, db.providerAccountID)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	defer privacy.Zeroize(resolved.PlaintextPayload)
	if strings.Contains(string(resolved.PlaintextPayload), refreshMaterialField) {
		t.Fatalf("官方 Key 轮换不得写入续期字段：%s", resolved.PlaintextPayload)
	}
}
