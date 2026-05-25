// Package obs 承载 F-OBS-001 的内部观测数据管线：outbox、replica 与
// reconciliation。
//
// 职责边界：obs 负责后台持久化和一致性修复；observability 包负责
// F-OBS-003 admin 看板与查询 handler。
package obs
