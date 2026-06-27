//go:build integration_pg

package credentialstore

import (
	"context"
	"errors"
	"testing"
)

// TestPG_VerifyKeySelfCheck_FailsClosedOnWrongKey 钉死启动期凭证密钥自检:
//   - 正确 KEK 解得开既有 active 凭证 → 放行(nil);
//   - 轮换后密钥材料不符(同 key_id)→ ErrDecryptFailed → 包裹成 ErrKeySelfCheckFailed,fail-closed;
//   - 轮换到新 key_id 却未保留旧 key → ErrKeyUnavailable → 同样 fail-closed。
//
// 判别(§14):若把 VerifyKeySelfCheck 里的解密尝试拆掉、恒返回 nil(即"自检永远放行"),
// 下方两个错配分支立即不再转红——证明断言真正咬住"解不开必须拒绝启动"。
func TestPG_VerifyKeySelfCheck_FailsClosedOnWrongKey(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	fixture := seedCredentialAuditTxFixture(t, ctx, pool, "kek-selfcheck")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, fixture)

	// 隔离:自检按全局抽样一条 active 凭证。把库内其它历史 active 凭证降级,
	// 保证抽样命中本测试新建的这条(否则可能命中别的用例残留、用别的 key 加密,结果不确定)。
	if _, err := pool.Exec(ctx, "UPDATE account_credentials SET state='revoked' WHERE state='active'"); err != nil {
		t.Fatalf("neutralize stray active credentials: %v", err)
	}

	goodKeys := mustTestKeyProvider(t) // key_id="test-key"
	store := NewStore(pool, goodKeys, DefaultHandlerRegistry())
	if _, err := store.Create(ctx, CreateCredentialInput{
		TenantID: fixture.tenantID, ProviderAccountID: fixture.providerAccountID,
		Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey, Payload: []byte(`{"api_key":"sk-kek-selfcheck"}`),
		ActorID: "owner",
	}); err != nil {
		t.Fatalf("Create active credential: %v", err)
	}

	// 1) 正确 KEK:解得开 → 放行。
	if err := store.VerifyKeySelfCheck(ctx); err != nil {
		t.Fatalf("正确 KEK 自检应放行,实际 err=%v", err)
	}

	// 2) 同 key_id、密钥材料不符(模拟原地换料轮换)→ fail-closed。
	wrongMaterial, err := NewStaticKeyProvider("test-key", []byte("ffffffffffffffffffffffffffffffff"))
	if err != nil {
		t.Fatalf("build wrong-material key: %v", err)
	}
	if err := NewStore(pool, wrongMaterial, DefaultHandlerRegistry()).VerifyKeySelfCheck(ctx); !errors.Is(err, ErrKeySelfCheckFailed) {
		t.Fatalf("错料 KEK 应 fail-closed(ErrKeySelfCheckFailed),实际 err=%v", err)
	}

	// 3) 轮换到新 key_id、旧 key 未保留(模拟无多版本密钥环的轮换)→ fail-closed。
	rotatedAway, err := NewStaticKeyProvider("rotated-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("build rotated key: %v", err)
	}
	if err := NewStore(pool, rotatedAway, DefaultHandlerRegistry()).VerifyKeySelfCheck(ctx); !errors.Is(err, ErrKeySelfCheckFailed) {
		t.Fatalf("轮换走 key_id 应 fail-closed(ErrKeySelfCheckFailed),实际 err=%v", err)
	}
}

// TestPG_VerifyKeySelfCheck_NoActiveCredentialPasses 钉死全新部署放行分支:库里无 active 凭证时
// 自检无可验证,必须放行启动(返回 nil),不得误把"空库"当成 KEK 故障。
// 判别:若把"无样本→放行"改成"无样本→fail-closed",本用例转红。
func TestPG_VerifyKeySelfCheck_NoActiveCredentialPasses(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	if _, err := pool.Exec(ctx, "UPDATE account_credentials SET state='revoked' WHERE state='active'"); err != nil {
		t.Fatalf("neutralize stray active credentials: %v", err)
	}
	if err := NewStore(pool, mustTestKeyProvider(t), DefaultHandlerRegistry()).VerifyKeySelfCheck(ctx); err != nil {
		t.Fatalf("无 active 凭证应放行启动,实际 err=%v", err)
	}
}

// TestPG_VerifyKeySelfCheck_OneCorruptCredentialDoesNotBlock 钉死"个别凭证数据损坏不拖垮启动":
// 样本里只要有一条能解开就放行——KEK 对其余账号有效时,不因单条坏数据(密文被损坏 → ErrDecryptFailed)
// fail-closed,否则会出现"一条坏凭证拖垮全网关启动"的过度误杀。
// 判别(§14):若把放行逻辑从"任一条能解开即放行"改成"任一条解不开即 fail-closed",本用例转红。
func TestPG_VerifyKeySelfCheck_OneCorruptCredentialDoesNotBlock(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)
	// 先种 bad(provider_account_id 更小),使自检按 provider_account_id 升序抽样时先撞到这条解不开的,
	// 从而真正考验"遇到解密失败仍继续找下一条、有一条能解开即放行";否则若 good 先被抽中会直接放行、
	// 测不到失败→继续这条路径,§14 变异(遇首条失败即 fail-closed)就抓不住。
	bad := seedCredentialAuditTxFixture(t, ctx, pool, "kek-corrupt")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, bad)
	good := seedCredentialAuditTxFixture(t, ctx, pool, "kek-good")
	defer cleanupCredentialAuditTxFixture(t, context.Background(), pool, good)
	// 钉死判别性前提:bad 必须比 good 先被自检抽中(provider_account_id 升序),否则 good 先被抽中
	// 直接放行、测不到"遇解密失败仍继续"这条路径,§14 变异(遇首条失败即 fail-closed)会悄然失效。
	if bad.providerAccountID >= good.providerAccountID {
		t.Fatalf("前提失效:期望 bad(%d) < good(%d),否则本用例测不到目标路径", bad.providerAccountID, good.providerAccountID)
	}
	if _, err := pool.Exec(ctx, "UPDATE account_credentials SET state='revoked' WHERE state='active'"); err != nil {
		t.Fatalf("neutralize stray active credentials: %v", err)
	}

	keys := mustTestKeyProvider(t)
	store := NewStore(pool, keys, DefaultHandlerRegistry())
	for _, f := range []credentialAuditTxFixture{good, bad} {
		if _, err := store.Create(ctx, CreateCredentialInput{
			TenantID: f.tenantID, ProviderAccountID: f.providerAccountID,
			Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey, Payload: []byte(`{"api_key":"sk-kek"}`),
			ActorID: "owner",
		}); err != nil {
			t.Fatalf("Create active credential: %v", err)
		}
	}
	// 把 bad 账号的密文损坏:用同 KEK 也解不开(GCM 认证失败 → ErrDecryptFailed)。
	if _, err := pool.Exec(ctx,
		`UPDATE account_credentials SET encrypted_payload = decode('deadbeef', 'hex')
		 WHERE provider_account_id=$1 AND state='active'`, bad.providerAccountID); err != nil {
		t.Fatalf("corrupt bad credential: %v", err)
	}

	// good 账号仍可解开 → 自检放行,不被 bad 那条坏数据拖垮。
	if err := store.VerifyKeySelfCheck(ctx); err != nil {
		t.Fatalf("一条坏凭证不应拖垮启动(good 账号可解),实际 err=%v", err)
	}
}
