// HUAKAI · iKun

package paymenthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

type fakeWebhookService struct {
	called      bool
	gotProvider payment.ProviderKind
	gotBody     []byte
	gotSig      string
	res         payment.FulfillResult
	err         error
}

func (f *fakeWebhookService) ConfirmPaidByCallback(_ context.Context, kind payment.ProviderKind, rawBody []byte, signature string) (payment.FulfillResult, error) {
	f.called = true
	f.gotProvider = kind
	f.gotBody = append([]byte(nil), rawBody...)
	f.gotSig = signature
	return f.res, f.err
}

func mountWebhook(svc WebhookService) http.Handler {
	r := chi.NewRouter()
	MountPaymentWebhookRoutes(r, WebhookDeps{Service: svc})
	return r
}

func postWebhook(h http.Handler, provider string, body []byte, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/payments/webhooks/"+provider, bytes.NewReader(body))
	if sig != "" {
		req.Header.Set(paymentSignatureHeader, sig)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// raw body 与签名头必须原样转给 service (验签基于原始字节, handler 不得改写)。
func TestWebhookHandler_ForwardsRawBodyAndSignature(t *testing.T) {
	fake := &fakeWebhookService{res: payment.FulfillResult{Order: payment.Order{Status: payment.StatusCompleted}}}
	body := []byte(`{"tenant_id":1,"out_trade_no":"x","paid_amount_cents":100}`)
	rec := postWebhook(mountWebhook(fake), "test", body, "sig-abc")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !fake.called {
		t.Fatalf("service not called")
	}
	if fake.gotProvider != payment.ProviderTest {
		t.Fatalf("provider = %q, want test", fake.gotProvider)
	}
	// 判别: body / sig 必须逐字节一致 (mutation: handler 若 JSON 归一化或丢签名头 → 此处变红)。
	if !bytes.Equal(fake.gotBody, body) {
		t.Fatalf("forwarded body = %q, want %q", fake.gotBody, body)
	}
	if fake.gotSig != "sig-abc" {
		t.Fatalf("forwarded sig = %q, want sig-abc", fake.gotSig)
	}
}

// 错误 → 状态码映射 (验签失败通用 401, 业务拒 409, 订单不存在 404, provider 不支持 400, 未知 provider 404)。
func TestWebhookHandler_ErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"unverified", payment.ErrCallbackUnverified, http.StatusUnauthorized},
		{"rejected", payment.ErrCallbackRejected, http.StatusConflict},
		{"not_found", payment.ErrOrderNotFound, http.StatusNotFound},
		{"not_confirmable", payment.ErrOrderNotConfirmable, http.StatusConflict},
		{"not_fulfillable", payment.ErrOrderNotFulfillable, http.StatusConflict},
		{"no_callback", payment.ErrProviderNoCallback, http.StatusBadRequest},
		{"unknown_provider", payment.ErrProviderUnknown, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeWebhookService{err: c.err}
			rec := postWebhook(mountWebhook(fake), "test", []byte(`{}`), "sig")
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
		})
	}
}

// 验签失败响应不得泄露失败原因细节 (只给通用错误码, 不告知伪造者哪步失败)。
func TestWebhookHandler_UnverifiedResponseIsGeneric(t *testing.T) {
	fake := &fakeWebhookService{err: payment.ErrCallbackUnverified}
	rec := postWebhook(mountWebhook(fake), "test", []byte(`{}`), "bad")
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "callback_unverified" {
		t.Fatalf("error code = %q, want callback_unverified", body.Error.Code)
	}
}

func TestWebhookHandler_UnsignedCallbackRejected(t *testing.T) {
	fake := &fakeWebhookService{err: payment.ErrCallbackUnverified}
	rec := postWebhook(mountWebhook(fake), "test", []byte(`{}`), "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned canonical webhook status=%d body=%s want 401", rec.Code, rec.Body.String())
	}
	if !fake.called {
		t.Fatal("service must receive unsigned callback so provider verifier can reject it")
	}
	if fake.gotSig != "" {
		t.Fatalf("unsigned callback signature forwarded as %q, want empty string", fake.gotSig)
	}
}

func TestWebhookHandler_ProviderMismatchRejected(t *testing.T) {
	fake := &fakeWebhookService{err: payment.ErrCallbackRejected}
	rec := postWebhook(mountWebhook(fake), "test", []byte(`{"provider":"other"}`), "sig")

	if rec.Code != http.StatusConflict {
		t.Fatalf("provider mismatch status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode provider mismatch body: %v", err)
	}
	if body.Error.Code != "callback_rejected" {
		t.Fatalf("provider mismatch error code = %q, want callback_rejected", body.Error.Code)
	}
}

// body 超 1 MiB 上限 → 400, 且 service 绝不被调用 (mutation: 去掉 MaxBytesReader → service 被调 → 红)。
func TestWebhookHandler_BodyCapEnforced(t *testing.T) {
	fake := &fakeWebhookService{res: payment.FulfillResult{Order: payment.Order{Status: payment.StatusCompleted}}}
	huge := bytes.Repeat([]byte("a"), (1<<20)+1024) // 略超 1 MiB
	rec := postWebhook(mountWebhook(fake), "test", huge, "sig")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (oversized body must be rejected)", rec.Code)
	}
	if fake.called {
		t.Fatalf("service must not be called for oversized body")
	}
}

// service 未配置 → 503 (fail-closed, 不 panic)。
func TestWebhookHandler_NilServiceUnavailable(t *testing.T) {
	rec := postWebhook(mountWebhook(nil), "test", []byte(`{}`), "sig")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
