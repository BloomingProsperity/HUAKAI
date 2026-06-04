//go:build debug

package proto

// ValidateEnvelopeDebug 在 debug build (-tags debug) 下转发到完整
// ValidateEnvelope，触发校验错误 全量结构性/语义性校验。
//
// 用法：开发或 CI 环境用 `go test -tags debug ./...` 或
// `go build -tags debug ./...` 跑出来 envelope 任何 INV 违规。
// release build 下走 envelope_validate_release.go 的 noop stub，
// 不引入运行时开销。
//
// 与 ValidateEnvelopeVersionGuard 的分工：
//
//   - VersionGuard：始终启用，仅校 nil + Version；hot path 边界
//   - ValidateEnvelopeDebug：debug build 启用 / release noop；
//     适合在 SSE adapter 入口、forwarder envelope 进出点放
//     "deep-check" 断言，捕捉 INV 违规但不影响 prod 性能
func ValidateEnvelopeDebug(env *HCSFEnvelope) error {
	return ValidateEnvelope(env)
}
