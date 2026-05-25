// Package proto 保存 HUAKAI vendor-neutral 协议契约。
//
// 根包只承载 HCSF 类型、adapter 接口、能力元数据、loss 记录、trust-chain、
// cache metrics payload 和通用 passthrough helper。vendor-specific stream /
// event adapter 放在 proto/anthropic、proto/openai、proto/gemini、
// proto/bedrock 等子包。
package proto
