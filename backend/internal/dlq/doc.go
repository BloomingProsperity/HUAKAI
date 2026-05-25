// Package dlq 提供 F-OBS-005 的异步死信队列能力，用于失败事件的记录、重试与
// 运维处理。
//
// 职责边界：dlq 是独立内部包，不是 obs 的子级；obs 负责 outbox、replica 与
// reconciliation。
package dlq
