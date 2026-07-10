//go:build integration_pg

package credentialstore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"
)

// TestVertexSABootstrapSchedulesImmediateRefresh 咬住 M1 bootstrap:创建一个只有 SA 私钥
// 材料、无 access_token 的 vertex_sa 凭据时,refresh_before_at 必须被排入(非 NULL),否则该
// 凭据永不进刷新扫描→refresher 永不铸首个 token→无法物化 fail-closed。
// 变异:把 prepareEnvelope 里"accessExp 为零则 refreshBefore=now"去掉 → refresh_before_at
// 为 NULL,本测试红。
func TestVertexSABootstrapSchedulesImmediateRefresh(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)

	f := seedCredentialAuditTxFixture(t, ctx, pool, "vertexsa-bootstrap")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, f)

	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}))
	payload, _ := json.Marshal(map[string]any{
		"client_email": "svc@proj.iam.gserviceaccount.com",
		"private_key":  pemKey,
		"project_id":   "my-proj",
	})

	store := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry())
	meta, err := store.Create(ctx, CreateCredentialInput{
		TenantID: f.tenantID, ProviderAccountID: f.providerAccountID,
		Vendor: VendorGemini, AuthMode: AuthModeVertexSA, Payload: payload, ActorID: "owner",
	})
	if err != nil {
		t.Fatalf("创建 vertex_sa 凭据: %v", err)
	}

	var refreshBeforeAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT refresh_before_at FROM account_credentials WHERE id=$1`, meta.ID,
	).Scan(&refreshBeforeAt); err != nil {
		t.Fatalf("读 refresh_before_at: %v", err)
	}
	if refreshBeforeAt == nil {
		t.Fatal("无 access_token 的 vertex_sa 凭据 refresh_before_at 为 NULL——永不进刷新扫描,首个 token 永不铸造(M1 bootstrap 缺失)")
	}
}
