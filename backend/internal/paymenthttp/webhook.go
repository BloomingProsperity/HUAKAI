// HUAKAI · iKun

package paymenthttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// paymentSignatureHeader 是 HUAKAI 回调签名头约定 (P2a test provider)。
// 真实渠道各自签名头不同, 留 P-RealMoney 按 provider 取头。
const paymentSignatureHeader = "X-Payment-Signature"

// WebhookService 是公开回调端点依赖的支付能力子集 (由 *payment.Service 实现)。
type WebhookService interface {
	ConfirmPaidByCallback(ctx context.Context, providerKind payment.ProviderKind, rawBody []byte, signature string) (payment.FulfillResult, error)
}

// WebhookDeps 公开回调路由依赖。
type WebhookDeps struct {
	Service WebhookService
}

// MountPaymentWebhookRoutes 挂载公开支付回调端点。
// 公开无 session/admin 中间件 — provider 无法持有 HUAKAI 会话, 信任完全来自验签;
// 生产未注册真实 provider 时端点 fail-closed (一切回调被拒)。
func MountPaymentWebhookRoutes(r chi.Router, d WebhookDeps) {
	r.Post("/v1/payments/webhooks/{provider}", newWebhookHandler(d))
}

func newWebhookHandler(d WebhookDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment dependency unset")
			return
		}
		providerKind := strings.TrimSpace(chi.URLParam(r, "provider"))
		if providerKind == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid_provider", "provider path segment is required")
			return
		}
		// 读 raw body (验签基于原始字节, 不可先 JSON 归一化); 1 MiB 上限防滥用。
		limited := http.MaxBytesReader(w, r.Body, maxBodyBytes)
		raw, err := io.ReadAll(limited)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid_webhook_body", "callback body too large or unreadable")
			return
		}
		signature := r.Header.Get(paymentSignatureHeader)
		res, err := d.Service.ConfirmPaidByCallback(r.Context(), payment.ProviderKind(providerKind), raw, signature)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     string(res.Order.Status),
			"idempotent": res.Idempotent,
		})
	}
}

// writeWebhookError 把回调错误映射为状态码。验签失败一律通用化 401, 不告知伪造者哪一步失败。
func writeWebhookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrCallbackUnverified):
		writeJSONError(w, http.StatusUnauthorized, "callback_unverified", "callback verification failed")
	case errors.Is(err, payment.ErrCallbackRejected):
		writeJSONError(w, http.StatusConflict, "callback_rejected", "callback rejected by business validation")
	case errors.Is(err, payment.ErrOrderNotFound):
		writeJSONError(w, http.StatusNotFound, "order_not_found", "payment order not found")
	case errors.Is(err, payment.ErrOrderNotConfirmable):
		// 已过期/取消/失败等终态订单收到回调 = 业务冲突 (别让 provider 当瞬时错误重试)。
		writeJSONError(w, http.StatusConflict, "order_not_confirmable", "order is not in a confirmable state")
	case errors.Is(err, payment.ErrOrderNotFulfillable):
		writeJSONError(w, http.StatusConflict, "order_not_fulfillable", "order is not in a fulfillable state")
	case errors.Is(err, payment.ErrProviderNoCallback):
		writeJSONError(w, http.StatusBadRequest, "provider_no_callback", "provider does not support callbacks")
	case errors.Is(err, payment.ErrProviderUnknown):
		writeJSONError(w, http.StatusNotFound, "unknown_provider", "provider not found")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "payment_backend_error", "payment service unavailable")
	}
}
