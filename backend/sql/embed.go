// Package sqlmigrations 用 go:embed 把 sql/migrations 下的迁移文件打进二进制,供进程内
// 自迁移(HUAKAI_AUTO_MIGRATE)在裸二进制单实例部署时无需外挂迁移目录即可建表。
//
// go:embed 不能跨目录向上引用,故内嵌点放在 sql/ 包内、由 dbmigrate 消费,而非放在 internal/。
package sqlmigrations

import "embed"

// Files 内嵌全部迁移 SQL(migrations/*.up.sql 与 *.down.sql)。
// README.md 等非 .sql 文件不在内嵌范围,iofs 源亦只识别 {version}_{name}.{up,down}.sql。
//
//go:embed migrations/*.sql
var Files embed.FS
