package trusthttp

import (
	"encoding/base64"
	"net/http"
	"testing"
)

// /v1/trust/verify 绝不能仅仅因为某些字节碰巧被共享的 audit 签名 key 签过,就把它们
// 认证为「有效 trust receipt」。audit ledger 用同一把 key 家族签 entry_hash 字节(以及
// 其它 trust.ledger.v1 载荷),因此缺少域分离时,攻击者可以把这样一段被签的载荷做 base64,
// 从而拿到 valid=true / status="signed-only"。
//
// 下面两个用例都由 handler 所接受的同一把可信 signer 签名,因此 ed25519 检查会通过——
// 唯一能拦住它们的,就是域检查。
//
// 变异自检:移除 verify() 里的 requireCanonicalTrustReceipt 调用(或让它始终返回 nil),
// 两个子用例都会翻成 valid=true status="signed-only" → 红。
func TestVerifyHandlerRejectsNonReceiptSignedPayload(t *testing.T) {
	signer := mustTestSigner(t)
	reg := mustRegistryForSigner(t, signer)

	cases := map[string][]byte{
		// audit-ledger 域的 JSON(不同的 schema_version,同一把 key)
		"ledger_domain_json": []byte(`{"schema_version":"trust.ledger.v1","entry_hash":"deadbeef","sequence":7}`),
		// 形如 entry_hash 的原始字节,根本不是 JSON
		"raw_entry_hash_bytes": {0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}
	for name, payload := range cases {
		payload := payload
		t.Run(name, func(t *testing.T) {
			req := verifyRequestBody(t,
				base64.StdEncoding.EncodeToString(payload),
				base64.StdEncoding.EncodeToString(signer.Sign(payload)),
				signer.Fingerprint(),
			)
			rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: reg}), req)
			got := decodeVerifyResponse(t, rec)

			if rec.Code != http.StatusOK {
				t.Fatalf("unexpected http status %d body=%s", rec.Code, rec.Body.String())
			}
			// 签名对这些字节确实有效——这正是陷阱所在。
			if !got.SignatureValid {
				t.Fatalf("precondition: signature should verify over the signed bytes, got %+v", got)
			}
			// 但它绝不能被认证为一份 trust receipt。
			if got.Valid || got.Status == "signed-only" {
				t.Fatalf("non-receipt signed payload certified as receipt: status=%q valid=%v (%+v)", got.Status, got.Valid, got)
			}
			// reason 复用契约内的 payload_invalid(见 OpenAPI TrustVerifyResponse.reason 枚举);
			// status=unverified + signature_valid=true 区分「签名有效但非 receipt 域」与篡改(mismatch)。
			if got.Reason != "payload_invalid" {
				t.Fatalf("want reason=payload_invalid for non-receipt domain, got %+v", got)
			}
			if got.Status != "unverified" {
				t.Fatalf("want status=unverified for signed-but-not-receipt, got %+v", got)
			}
		})
	}
}

// TestVerifyHandlerStillAcceptsRealReceiptBase64 证明域检查没有破坏合法的 base64 路径:
// 一份由该 key 签名的真实 canonical trust.receipt.v1 仍必须返回 valid=true status="signed-only"。
func TestVerifyHandlerStillAcceptsRealReceiptBase64(t *testing.T) {
	signer := mustTestSigner(t)
	canonical := mustCanonicalReceipt(t, sampleTrustReceipt())
	req := verifyRequestBody(t,
		base64.StdEncoding.EncodeToString(canonical),
		base64.StdEncoding.EncodeToString(signer.Sign(canonical)),
		signer.Fingerprint(),
	)
	rec := doTrustVerify(t, NewVerifyHandler(VerifyDeps{Registry: mustRegistryForSigner(t, signer)}), req)
	got := decodeVerifyResponse(t, rec)
	if !got.Valid || got.Status != "signed-only" {
		t.Fatalf("genuine receipt base64 must still verify, got %+v", got)
	}
}
