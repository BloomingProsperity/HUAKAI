package adminsessionauth

import (
	"context"
	"net/http"
)

// AdminWriteClass 是 session 通道对写端点的放行等级。它是 opt-in 标注:
// 未标注的写端点 = 默认拒绝 session 写(fail-closed = token-only)。这是把爆炸半径关在
// 默认态的关键不变量——只有显式挂了本标注的路由,session-admin 才够得到写。
// token 通道与只读方法不受本机制影响。
//
// role 制单登录 P3 的 Owner 终审把写分级收敛成二元：token-only(默认)
// vs SessionSafe(session 可直接写)。危险/不可逆操作靠前端确认弹窗防误操作,后端不做二次密码/2FA。
type AdminWriteClass int

const (
	// writeClassNone 是零值:未标注 → session 写一律拒。不导出(缺省即此,fail-closed)。
	writeClassNone AdminWriteClass = iota
	// SessionSafe:session-admin 可直接写(登录 admin 是受信运营者;危险操作前端弹确认框防手滑)。
	SessionSafe
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
