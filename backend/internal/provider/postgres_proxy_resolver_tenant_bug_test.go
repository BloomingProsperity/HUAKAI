package provider

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
)

// TestDecryptRowAuthSecret_TenantScoped_S1 证实审计 S1.2 的底层机制并守护其修复。
//
// 背景:代理 auth_secret 的加/解密以 tenantID 为上下文。Resolve 曾在构造 proxyRow 时
// 漏拷 tenantID(结构体默认 0),于是 decryptRowAuthSecret 用 tenant 0 解密一个在真实
// 租户下加密的 secret → ErrDecryptFailed → 所有带认证的账号级/组代理出站 fail-closed。
// 修复:两处 proxyRow{...} 构造补 `tenantID: row.tenantID`。
//
// 本测试证实机制:用正确 tenantID(=修复后 Resolve 所拷)能解密;用 0(=漏拷时的默认)
// 必失败。若"用 0 也能解密",则 tenant 未真正参与解密、S1.2 结论不成立 → 本测试 RED 报警。
// （通过 Resolve 端到端触发本 bug 的判别测试需真 postgres,见 integration 说明。）
func TestDecryptRowAuthSecret_TenantScoped_S1(t *testing.T) {
	ctx := context.Background()
	keys, err := credentialstore.NewStaticKeyProvider("test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	const tenantID = int64(42)
	const secret = "proxy-super-secret"

	r := &PostgresProxyResolver{keys: keys}

	// 正确 tenantID(修复后 Resolve 拷贝的值)→ 解密成功、明文正确。
	enc1, err := proxysecret.Encode(ctx, keys, tenantID, secret)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	good := proxyRow{tenantID: tenantID, secret: &enc1}
	if err := r.decryptRowAuthSecret(ctx, &good); err != nil {
		t.Fatalf("正确 tenantID 应解密成功, 实得 err=%v", err)
	}
	if good.secret == nil || *good.secret != secret {
		t.Fatalf("解密明文不符: got=%v want=%q", good.secret, secret)
	}

	// bug 场景:tenantID=0(Resolve 漏拷时的默认值)→ 解密必失败,证实漏拷会瘫痪带认证代理。
	enc2, err := proxysecret.Encode(ctx, keys, tenantID, secret)
	if err != nil {
		t.Fatalf("encode2: %v", err)
	}
	bug := proxyRow{tenantID: 0, secret: &enc2}
	if err := r.decryptRowAuthSecret(ctx, &bug); err == nil {
		t.Fatalf("tenantID=0 解密一个 tenant=%d 加密的 secret 竟成功 —— tenant 未参与解密上下文, 与 S1.2 结论不符", tenantID)
	}
}
