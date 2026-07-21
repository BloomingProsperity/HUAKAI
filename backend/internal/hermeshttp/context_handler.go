package hermeshttp

import (
	"log"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/modulehttp"
)

// getModuleContext 服务 GET /v1/hermes/context。它返回跨所有类别合并后的
// module-knowledge 视图(module 身份 + capabilities + 激活快照 + 实时探针状态 +
// 静态 catalog 叠加)——这是 H2 module 主干的首个消费者。由 H1 中间件挂载做 admin 门控;这里只
// 要求一个已解析的身份。隐私:仅含 module 身份 + 枚举状态 + 简短 detail 字符串,
// 绝不含机密或用户数据。
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
