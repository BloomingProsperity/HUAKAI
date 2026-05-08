// middleware.go — U6-B atomic：HTTP middleware 把 client identity 写到 ctx
// 让下游 handler / quota / metrics 通过 IdentityFromContext 读取，避免每层
// 重复识别。
//
// 用法（cmd/gateway/main.go chi router）:
//
//   router.Use(middleware.RequestID)
//   router.Use(middleware.RealIP)
//   router.Use(middleware.Recoverer)
//   router.Use(middleware.Timeout(60 * time.Second))
//   router.Use(clientid.Middleware())  // <-- 在 Recoverer 之后即可
//
// 设计:
//   - **不**因检测失败而拒绝请求（execution_boundary_c rule + IdentityUnknown
//     fallback 是正常路径）
//   - **不**对 hot path 加锁；Detect 是 stateless / Signal 是 per-request 局部
//   - 错误隔离: panic 不会上抛（即便 Detect 实现异常也不影响 forwarder）
//     —— 复用 chi middleware.Recoverer 责任链；本 middleware 自己不再加 recover
//   - **不**改 request body（不需 sniff body，避免读完 body 后下游不能读）
package clientid

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// Middleware 返回 chi 兼容的 HTTP middleware：抽 Signal → Detect → 写 ctx。
//
// logger 可为 nil（静默模式）；非 nil 时按 DEBUG 级别 emit 一行结构化日志
// 含 request_id + identity + confidence，便于运维审视误判（sonnet F5 SHOULD_FIX）。
//
// 返回 chi.Router.Use 接受的 func(http.Handler) http.Handler 形态。
func Middleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			signal := SignalFromRequest(r)
			id, conf := Detect(signal)
			ctx := WithIdentity(r.Context(), id, conf)
			if logger != nil {
				logger.Debug("client identity detected",
					zap.String("request_id", middleware.GetReqID(ctx)),
					zap.String("identity", string(id)),
					zap.Float64("confidence", conf),
					zap.String("path", r.URL.Path),
				)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
