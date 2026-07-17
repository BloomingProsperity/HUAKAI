// Package quota 承载 HUAKAI 配额子系统的中性领域契约。
//
// Slice A 只提供 schema/query 骨架和可编译的 Go 抽象:
// reserve/settle/release、窗口计数、并发槽、审计和补偿队列的实现留到
// Slice B。包内命名和类型均为 HUAKAI 自有的中性领域抽象。
package quota
