//go:build !debug

package proto

// ValidateEnvelopeDebug 在 release build（默认）是 noop，永远返回 nil。
// 见 envelope_validate_debug.go 注释。
//
// 这样 hot path 可以无条件调 ValidateEnvelopeDebug 且不付出 runtime 代价；
// debug build 时编译器选 envelope_validate_debug.go 那份实现以触发完整校验。
func ValidateEnvelopeDebug(env *HCSFEnvelope) error {
	_ = env
	return nil
}
