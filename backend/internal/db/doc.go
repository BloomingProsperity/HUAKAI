// Package db 只保留跨 sqlc 子包共享的连接与事务接口。
//
// 业务表查询由 internal/db/admin、billing、auth、audit、registry 子包承载。
package db
