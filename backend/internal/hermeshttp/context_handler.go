package hermeshttp

import (
	"log"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

// getModuleContext 服务 GET /v1/hermes/context。它返回跨类别合并的模块身份、
// 能力、激活快照、实时探针状态和静态目录。路由层负责角色授权，本处理器仍要求
// 上下文中存在已解析身份。响应只含模块身份、枚举状态和简短详情，不含机密或用户数据。
func (h handler) getModuleContext(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireIdentity(w, r); !ok {
		return
	}
	if h.contextSource == nil {
		writeError(w, http.StatusServiceUnavailable, "hermes_context_unavailable", "hermes module context source unset")
		return
	}
	views := modulehttp.ContextSummary(r.Context(), h.contextSource)
	writeJSON(w, http.StatusOK, map[string]any{"modules": views})
}

func logToolCallWriteFailure(toolName string, err error) {
	log.Printf("hermes tool-call audit insert failed (tool=%s): %v", toolName, err)
}

func logToolAuditMirrorFailure(toolName string, err error) {
	log.Printf("hermes tool audit mirror failed (tool=%s): %v", toolName, err)
}
