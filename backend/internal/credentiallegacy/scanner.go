// Package credentiallegacy 负责发现仍需迁出旧内联字段的账号，不读取或返回凭据内容。
package credentiallegacy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type RowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Count 返回未删除账号中仍保存非空内联凭据的数量。查询只计算行数，避免凭据
// 内容进入进程日志、诊断响应或错误链。
func Count(ctx context.Context, db RowQuerier) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("credentiallegacy: database is unset")
	}
	var count int64
	err := db.QueryRow(ctx, `
SELECT count(*)::bigint
FROM provider_accounts
WHERE deleted_at IS NULL
  AND credentials IS NOT NULL
  AND credentials <> '{}'::jsonb
`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("credentiallegacy: count inline credentials: %w", err)
	}
	return count, nil
}
