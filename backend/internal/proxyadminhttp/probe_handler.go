package proxyadminhttp

import (
	"net/http"
	"time"
)

// testResponse 是 POST /{id}/test 的结果 DTO。**绝不含代理 URL、凭据或原始错误**——
// 只回连通性、延迟与粗粒度错误分类。
type testResponse struct {
	Object     string `json:"object"`
	OK         bool   `json:"ok"`
	LatencyMS  int64  `json:"latency_ms"`
	ErrorClass string `json:"error_class,omitempty"`
	ProbedAt   string `json:"probed_at"`
}

// newTestHandler 处理 POST /admin/v1/proxies/{id}/test:经该 stored 代理建隧道到服务端固定 canary,
// 测真实出站连通性 + 延迟。鉴权/租户隔离复用 resolveTenant(租户运营者越权他人 tenant → 403)。
// 探测目标是服务端常量(在 Prober 实现里),请求体不参与目标决策——杜绝双跳 SSRF。
func newTestHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenant(w, r, d)
		if !ok {
			return
		}
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if d.Prober == nil {
			writeError(w, http.StatusServiceUnavailable, "prober_unavailable", "proxy probe not configured")
			return
		}
		out, err := d.Prober.Probe(r.Context(), tenantID, id)
		if err != nil {
			// ErrNotFound→404 / ErrInvalidInput→400 / 其它→500,均不泄露原始错误细节。
			writeServiceError(w, err, "proxy probe failed")
			return
		}
		writeJSON(w, http.StatusOK, testResponse{
			Object:     "proxy_probe",
			OK:         out.OK,
			LatencyMS:  out.LatencyMS,
			ErrorClass: out.ErrorClass,
			ProbedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}
}
