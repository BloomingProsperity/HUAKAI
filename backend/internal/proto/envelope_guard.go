package proto

import "fmt"

// ValidateEnvelopeVersionGuard 是 ValidateEnvelope 的轻量同伴；
// 仅在 HCSF adapter 边界做最小一致性检查，确保 envelope 不为 nil 且
// Version 字段锁定为 HCSFVersion。
//
// 设计目标（M4 architecture，per P-0c synthesis）：
//
//   - 适用场景：forwarder / dispatcher / SSE adapter 等 hot path 在每次
//     穿越 envelope 边界时调用，**避免**在请求处理路径上跑完整
//     ValidateEnvelope 的完整检查（图遍历 + projection
//     枚举 + tagged-union nil 扫描成本不可忽略）。
//   - 性能预期：单次调用 ~ 1-2 次指针解引 + 1 次字符串等值比较，
//     纳秒级（无 map / slice / 反射），可放在 per-request 关键路径。
//   - 与 ValidateEnvelope 的关系：本函数是 ValidateEnvelope 的**真子集**——
//     凡能通过 ValidateEnvelope 的 envelope 必能通过本函数；反之不然。
//     完整结构性 / 语义性校验仍由 ValidateEnvelope（debug build / 测试 /
//     fixture 入口）承担；本函数仅守 alias sunset 后最容易出错的点：
//     "Version 字段未被显式赋值导致零值穿过边界"。
//
// 返回值：
//
//   - env == nil → 校验错误
//   - env.Version != HCSFVersion → 校验错误
//   - 其它一切违规 → 不在本函数职责内（调用方若需要全量校验请直接用
//     ValidateEnvelope，或在 debug build 下走 ValidateEnvelopeDebug）
//
// 调用示例：
//
//	if err := proto.ValidateEnvelopeVersionGuard(env); err != nil {
//	    // log & 拒绝；不要 panic（hot path）
//	    return err
//	}
func ValidateEnvelopeVersionGuard(env *HCSFEnvelope) error {
	if env == nil {
		return &ValidationError{Inv: "INV-0", Message: "envelope is nil"}
	}
	if env.Version != HCSFVersion {
		return &ValidationError{
			Inv:     "INV-4",
			Message: fmt.Sprintf("Version must be %q, got %q", HCSFVersion, env.Version),
		}
	}
	return nil
}
