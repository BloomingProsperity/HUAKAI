// Package backuphttp 暴露一个**只读元数据**端点 GET /v1/admin/backup/manifest:返回"可备份清单"
// (表名 + 行数估算 + schema/迁移版本 + 脱敏策略声明)。**不导出任何业务数据、零写入、零凭据外泄**——
// 该端点只负责数据库级备份元数据；账号级加密迁移由 accountbundle 独立合同负责。
// platform_admin only(由 cmd/gateway 的 adminGate 强制),全库元数据属平台级信息。
package backuphttp

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TableInfo 是单张表的元数据(只读;行数为估算值,非精确 COUNT)。
type TableInfo struct {
	Name          string `json:"name"`
	EstimatedRows int64  `json:"estimated_rows"`
}

// ManifestData 是后端聚合的可备份元数据(不含任何业务行数据)。
type ManifestData struct {
	SchemaVersion int64
	SchemaDirty   bool
	Tables        []TableInfo
}

// Store 抽象只读元数据查询,便于 handler 单测注入 fake。
type Store interface {
	Manifest(ctx context.Context) (ManifestData, error)
}

// PostgresStore 只读 pg_catalog + schema_migrations。绝不读业务表数据、绝不写任何状态。
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Manifest 取迁移版本 + public schema 下所有基表的行数估算。行数用 pg_class.reltuples(上次
// ANALYZE/VACUUM 的估算值,GREATEST 兜 -1=从未分析),避免对大表跑昂贵的精确 COUNT(*)。
func (s *PostgresStore) Manifest(ctx context.Context) (ManifestData, error) {
	var out ManifestData

	// 迁移版本(golang-migrate 的 schema_migrations 单行 version/dirty;空表=尚未迁移→version 0)。
	err := s.pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).
		Scan(&out.SchemaVersion, &out.SchemaDirty)
	if err != nil && err != pgx.ErrNoRows {
		return ManifestData{}, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.relname, GREATEST(c.reltuples, 0)::bigint AS est_rows
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relkind = 'r'
		  AND c.relname NOT LIKE 'pg_%'
		ORDER BY c.relname`)
	if err != nil {
		return ManifestData{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var t TableInfo
		if err := rows.Scan(&t.Name, &t.EstimatedRows); err != nil {
			return ManifestData{}, err
		}
		out.Tables = append(out.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return ManifestData{}, err
	}
	return out, nil
}
