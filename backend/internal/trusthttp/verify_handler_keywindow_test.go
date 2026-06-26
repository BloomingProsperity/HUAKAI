package trusthttp

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
)

// receipt 校验必须拒绝 occurred_at 落在签名 key 的 [EffectiveFrom, EffectiveTo] 窗口之外
// 的 receipt——即便 ed25519 签名在密码学上有效。这就是泄漏的轮换 key 攻击:一把仅在
// 2026 年 1–3 月有效的旧 key,去签一份日期为 2026 年 5 月的新 receipt。
//
// 变异自检:删掉 verify() 里的 SignatureOutsideKeyWindow 分支,"outside" 子断言会翻成
// valid=true status="signed-only" → 红。"inside" 对照证明落在窗口内的 receipt 仍然通过,
// 因此该检查具有判别性(不是一刀切拒绝)。
func TestVerifyHandlerRejectsReceiptSignedOutsideKeyWindow(t *testing.T) {
	signer := mustTestSigner(t)
	key := mustPubkeyFromSigner(t, signer, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	effectiveTo := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	key.EffectiveTo = &effectiveTo // 已轮换:仅 2026-01-01 .. 2026-03-01 有效
	handler := NewVerifyHandler(VerifyDeps{Registry: auditledger.NewMemoryPubkeyRegistry(key)})

	// 攻击:同一把(已轮换的)key 去签一份日期晚于其 EffectiveTo 的 receipt。
	outside := sampleTrustReceipt()
	outside.OccurredAt = time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	canonOut := mustCanonicalReceipt(t, outside)
	reqOut := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonOut),
		base64.StdEncoding.EncodeToString(signer.Sign(canonOut)),
		signer.Fingerprint(),
	)
	gotOut := decodeVerifyResponse(t, doTrustVerify(t, handler, reqOut))
	if !gotOut.SignatureValid {
		t.Fatalf("precondition: ed25519 signature must verify over canonical bytes, got %+v", gotOut)
	}
	if gotOut.Valid || gotOut.Reason != "signature_outside_key_window" {
		t.Fatalf("receipt signed outside key window must be rejected, got %+v", gotOut)
	}

	// 对照:日期落在窗口之内的 receipt 仍然通过。
	inside := sampleTrustReceipt()
	inside.OccurredAt = time.Date(2026, 2, 15, 12, 0, 0, 0, time.UTC)
	canonIn := mustCanonicalReceipt(t, inside)
	reqIn := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonIn),
		base64.StdEncoding.EncodeToString(signer.Sign(canonIn)),
		signer.Fingerprint(),
	)
	gotIn := decodeVerifyResponse(t, doTrustVerify(t, handler, reqIn))
	if !gotIn.Valid || gotIn.Status != "signed-only" {
		t.Fatalf("in-window receipt must still verify, got %+v", gotIn)
	}
}

// TestVerifyHandlerSignerOnlyDoesNotWindowRejectHistoricalReceipt 守护 S1-032a:
// 在仅 signer 模式(无 registry)下,lookupKey 会捏造一把 EffectiveFrom = 校验时刻的 key,
// 因此幼稚的窗口检查会拒绝每一份历史 receipt(occurred_at < now)。修复方案只在存在真实
// registry 时才强制窗口。这里一份日期为 2026-05-27 的 receipt 在 "now"=2026-06-01 时
// 经由仅 Signer 的 deps 校验,必须仍然通过。
//
// 变异自检:去掉窗口检查上的 `h.deps.Registry != nil` 守卫,这份 receipt 会翻成
// valid=false reason="signature_outside_key_window" → 红。
func TestVerifyHandlerSignerOnlyDoesNotWindowRejectHistoricalReceipt(t *testing.T) {
	signer := mustTestSigner(t)
	handler := NewVerifyHandler(VerifyDeps{
		Signer: signer,
		Now:    func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) },
	})
	receipt := sampleTrustReceipt() // OccurredAt 2026-05-27,早于被捏造的 EffectiveFrom
	canonical := mustCanonicalReceipt(t, receipt)
	req := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonical),
		base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		signer.Fingerprint(),
	)
	got := decodeVerifyResponse(t, doTrustVerify(t, handler, req))
	if !got.Valid || got.Status != "signed-only" {
		t.Fatalf("signer-only historical receipt must still verify (no fabricated-window reject): %+v", got)
	}
}

// TestVerifyHandlerRevocationTakesPrecedenceOverKeyWindow 守护 P2 修复:当一把 key 既被
// CRL 撤销、又签了一份落在其有效窗口之外的 receipt 时,响应必须报告撤销
//(key_revoked / key_status=revoked),而不是把它掩盖成 signature_outside_key_window——
// 运维必须仍能看到一把被攻陷的 key 已被撤销。
//
// 变异自检:把窗口检查移到 revoked 分支之前(修复前的顺序),本测试断言 key_revoked
// 却得到 signature_outside_key_window → 红。
func TestVerifyHandlerRevocationTakesPrecedenceOverKeyWindow(t *testing.T) {
	signer := mustTestSigner(t)
	key := mustPubkeyFromSigner(t, signer, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	effectiveTo := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	key.EffectiveTo = &effectiveTo // 样例 receipt(2026-05-27)落在此窗口之外
	handler := NewVerifyHandler(VerifyDeps{
		Registry: auditledger.NewMemoryPubkeyRegistry(key),
		Revocations: Revocations{
			signer.Fingerprint(): {Fingerprint: signer.Fingerprint(), RevokedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), ReasonClass: "key_compromise"},
		},
	})
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonical),
		base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		signer.Fingerprint(),
	)
	got := decodeVerifyResponse(t, doTrustVerify(t, handler, req))
	if got.Valid {
		t.Fatalf("revoked+out-of-window must not be valid: %+v", got)
	}
	if got.Reason != "key_revoked" || got.KeyStatus != "revoked" {
		t.Fatalf("revocation must take precedence over key-window (compromise must stay visible): %+v", got)
	}
}
