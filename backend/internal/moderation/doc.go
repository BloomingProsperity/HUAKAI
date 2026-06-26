// Package moderation 负责内容审核 screener 及其存储。
//
// 不变量:
//   - 聊天分发可在 billing reserve 之前调用 screener；nil 接线
//     默认保持直通放行。
//   - screener 可检查内存中的请求体,但审计事件只存储
//     payload hash 和命中元数据。
//   - fail-closed 行为由每个租户的配置显式决定。
//   - 上游平台策略分类仍由 internal/gateway 负责。
package moderation
