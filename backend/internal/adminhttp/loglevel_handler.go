package adminhttp

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/adminsessionauth"
	"github.com/BloomingProsperity/HUAKAI/internal/loglevel"
)

// LogLevelDeps 持有 admin log-level 端点所需的鉴权依赖。
type LogLevelDeps struct {
	Auth logLevelAuth
}

type logLevelAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// MountLogLevelRoutes 注册 GET + PUT /loglevel。GET 返回当前日志级别;
// PUT {"level":"debug"} 在运行时设置级别(委托给 zap 的
// AtomicLevel.ServeHTTP)。挂载在 /admin/v1 和 /v1/admin 之下。
func MountLogLevelRoutes(r chi.Router, d LogLevelDeps) {
	// SessionSafe:运行时日志级别切换(zap 进程内原子变量,即时可逆),登录 admin(session)可直接写。
	h := newLogLevelHandler(d)
	r.Get("/loglevel", h)
	r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe)).Put("/loglevel", h)
}

func newLogLevelHandler(d LogLevelDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "loglevel dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		// 仅限 platform_admin:修改全局日志详细程度属于平台级操作。
		if ident.Role != admin.RolePlatformAdmin {
			writeError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
			return
		}
		// zap 的 AtomicLevel 同时处理 GET(返回当前级别)和 PUT({"level":"debug"})。
		loglevel.Level.ServeHTTP(w, r)
	}
}
