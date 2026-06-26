package modulehttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ModulesResponse 是 GET /admin/v1/modules 的 JSON body。
type ModulesResponse struct {
	Modules []ModuleView `json:"modules"`
}

// NewModulesHandler 返回只读的 modules handler。它刻意置于 adminGate 之后
//(由调用方负责,与 system-health 路由对称)—— handler 自身不做任何鉴权,
// 因此其测试可专注于 merge/filter 行为,而门控则在路由层断言。
//
// 查询参数:
//
//	?category=<cat>  过滤到单个 category(例如 money-path);为空 = 全部。
func NewModulesHandler(src Source) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			writeJSON(w, http.StatusServiceUnavailable, ModulesResponse{Modules: []ModuleView{}})
			return
		}
		category := r.URL.Query().Get("category")
		views := Merge(r.Context(), src, category)
		if views == nil {
			views = []ModuleView{}
		}
		writeJSON(w, http.StatusOK, ModulesResponse{Modules: views})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("modulehttp json encode failed", slog.String("error", err.Error()))
	}
}
