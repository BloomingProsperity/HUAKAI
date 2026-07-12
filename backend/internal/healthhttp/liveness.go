package healthhttp

import (
	"encoding/json"
	"net/http"
)

// NewLivenessHandler 返回一个仅进程级的存活性(liveness)响应。它刻意
// 不触碰 DB、上游 provider、凭证或 admin 鉴权。
func NewLivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}
