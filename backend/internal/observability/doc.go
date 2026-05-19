// Package observability 实现 admin observability handler，用于 F-OBS-003
// 的看板查询与运维视图。
//
// 职责边界：observability 不承载内部 outbox、replica、DLQ 工作流；这些分别
// 位于 obs 与 dlq 包。
package observability
