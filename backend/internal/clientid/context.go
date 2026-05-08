// context.go — clientid 与 context 集成 helper。
//
// 用途: middleware 把 Detect 结果挂 context；下游 handler / quota / metrics
// 通过 IdentityFromContext 读取，避免每层重复检测。
package clientid

import "context"

// ctxKey 是 unexported type 防止外部包 key 冲突。
type ctxKey struct{}

var identityKey = ctxKey{}

// detected 是挂在 context 的载荷（identity + 检测置信度）。
type detected struct {
	identity   Identity
	confidence float64
}

// WithIdentity 把 (identity, confidence) 挂到 ctx，返回新 ctx。
// middleware 在请求入口调用一次。
func WithIdentity(ctx context.Context, id Identity, confidence float64) context.Context {
	return context.WithValue(ctx, identityKey, detected{identity: id, confidence: confidence})
}

// IdentityFromContext 从 ctx 取 identity。
// 未挂时返回 (IdentityUnknown, 0)，方便下游一律 switch 处理。
func IdentityFromContext(ctx context.Context) (Identity, float64) {
	if ctx == nil {
		return IdentityUnknown, 0
	}
	v := ctx.Value(identityKey)
	if v == nil {
		return IdentityUnknown, 0
	}
	d, ok := v.(detected)
	if !ok {
		return IdentityUnknown, 0
	}
	return d.identity, d.confidence
}
