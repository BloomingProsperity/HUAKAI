package proxyadminhttp

import (
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyquality"
)

// testResponse 是 POST /{id}/test 的结果 DTO。**绝不含代理 URL、凭据或原始错误**——
// 只回连通性、延迟、粗粒度错误分类与回写后的质量档。
type testResponse struct {
	Object         string `json:"object"`
	OK             bool   `json:"ok"`
	LatencyMS      int64  `json:"latency_ms"`
	ErrorClass     string `json:"error_class,omitempty"`
	Grade          string `json:"grade"`
	Source         string `json:"source"`
	Fresh          bool   `json:"fresh"`
	EffectiveGrade string `json:"effective_grade"`
	ProbedAt       string `json:"probed_at"`
}

// newTestHandler 处理 POST /admin/v1/proxies/{id}/test:经该 stored 代理建隧道到服务端固定 canary,
// 测真实出站连通性 + 延迟，并回写权威质量快照。鉴权/租户隔离复用 resolveTenant。
// 探测目标是服务端常量,请求体不参与目标决策——杜绝双跳 SSRF。
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
		if d.Service == nil {
			writeError(w, http.StatusServiceUnavailable, "quality_store_unavailable", "proxy quality store not configured")
			return
		}
		out, err := d.Prober.Probe(r.Context(), tenantID, id)
		if err != nil {
			writeServiceError(w, err, "proxy probe failed")
			return
		}
		recorded, err := d.Service.RecordQuality(r.Context(), tenantID, id, proxyadmin.QualityWrite{
			OK:         out.OK,
			LatencyMS:  out.LatencyMS,
			ErrorClass: out.ErrorClass,
			Source:     proxyquality.SourceManual,
		})
		if err != nil {
			writeServiceError(w, err, "proxy quality persist failed")
			return
		}
		probedAt := time.Now().UTC().Format(time.RFC3339)
		grade := proxyquality.Grade(out.OK, out.LatencyMS)
		fresh := true
		effective := grade
		if recorded.Quality.HasSnapshot {
			if recorded.Quality.ProbedAt != nil {
				probedAt = recorded.Quality.ProbedAt.UTC().Format(time.RFC3339)
			}
			grade = recorded.Quality.Grade
			fresh = recorded.Quality.Fresh
			effective = recorded.Quality.EffectiveGrade
		}
		writeJSON(w, http.StatusOK, testResponse{
			Object:         "proxy_probe",
			OK:             out.OK,
			LatencyMS:      out.LatencyMS,
			ErrorClass:     out.ErrorClass,
			Grade:          grade,
			Source:         proxyquality.SourceManual,
			Fresh:          fresh,
			EffectiveGrade: effective,
			ProbedAt:       probedAt,
		})
	}
}
