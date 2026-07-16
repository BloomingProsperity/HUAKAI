// Package bindingfallback 定义绑定级降级的稳定类别与纯决策契约。
// 本包不做 IO，也不依赖路由、网关、计费或认证包。
package bindingfallback

// Class 表示 model→pool binding 在路由计划中的职责类别。
type Class string

const (
	ClassNormal        Class = "normal"
	ClassContextWindow Class = "context_window"
	ClassSafety        Class = "safety"
	ClassQuota         Class = "quota"
	ClassManual        Class = "manual"
)

var fallbackClassOrder = [...]Class{
	ClassContextWindow,
	ClassSafety,
	ClassQuota,
	ClassManual,
}

// NormalizeClass 把历史空值解释为 normal；其它值保持原样，交给调用方的
// 已知值校验决定是否 fail-closed。
func NormalizeClass(raw string) Class {
	if raw == "" {
		return ClassNormal
	}
	return Class(raw)
}

// IsKnownClass 报告 class 是否属于管理契约允许的五个值。
func IsKnownClass(class Class) bool {
	switch class {
	case ClassNormal, ClassContextWindow, ClassSafety, ClassQuota, ClassManual:
		return true
	default:
		return false
	}
}

// FallbackClasses 返回 Router 编译目标 phase 时必须使用的固定顺序。
// 每次返回新切片，避免调用方修改全局契约。
func FallbackClasses() []Class {
	out := make([]Class, len(fallbackClassOrder))
	copy(out, fallbackClassOrder[:])
	return out
}
