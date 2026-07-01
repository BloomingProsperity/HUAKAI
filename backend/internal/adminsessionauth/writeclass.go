package adminsessionauth

import (
	"context"
	"net/http"
)

// AdminWriteClass 是 session 通道对写端点的放行等级。它是 opt-in 标注:
// 未标注的写端点 = 默认拒绝 session 写(fail-closed = token-only)。这是把爆炸半径关在
// 默认态的关键不变量——只有显式挂了本标注的路由,session-admin 才够得到写。
// token 通道与只读方法不受本机制影响。
type AdminWriteClass int

const (
	// writeClassNone 是零值:未标注 → session 写一律拒。不导出(缺省即此,fail-closed)。
	writeClassNone AdminWriteClass = iota
	// SessionSafe:低危配置类,session-admin 可直接写,无需 step-up。
	SessionSafe
	// SessionStepUp:中危,session-admin 需带新鲜 step-up 证明(header)方可写。
	SessionStepUp
)

// step-up 证明的 HTTP header 载体。走 header 而非 body:避开 danger 端点解码器的
// DisallowUnknownFields(请求结构体无 step_up 字段 → body 载必 400),且解析器不碰 r.Body
//(否则耗尽致下游 handler 读到 EOF)。二者任一即可(密码优先,见既有 verifier 语义)。
const (
	StepUpPasswordHeader  = "X-Admin-Step-Up-Password"
	StepUpTwoFactorHeader = "X-Admin-Step-Up-2FA"
)

type writeClassCtxKey struct{}

// AllowSessionWrite 是 per-route 中间件:把该路由的写放行等级塞进 request context,
// 供解析器在 session 写分支读取。挂在路由注册处(端点风险已知),只给要放开的路由挂。
func AllowSessionWrite(class AdminWriteClass) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), writeClassCtxKey{}, class)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeClassFromContext 读取放行等级。未设置(类型断言失败)→ 零值 writeClassNone,fail-closed。
func writeClassFromContext(ctx context.Context) AdminWriteClass {
	v, _ := ctx.Value(writeClassCtxKey{}).(AdminWriteClass)
	return v
}

// StepUpVerifier 校验 session 通道 SessionStepUp 写端点的 step-up 证明。生产实现为
// adminstepup.Verifier(结构上满足;本包不 import 之,避免解析器→http 层级耦合)。
// 返回 nil=通过,或 admin.ErrAdminStepUp{Required,Invalid,Locked} / admin.ErrAdminBackend。
type StepUpVerifier interface {
	VerifyStepUp(ctx context.Context, tenantID, userID int64, password, twoFactorCode string) error
}
