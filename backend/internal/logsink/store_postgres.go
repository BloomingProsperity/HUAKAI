package logsink

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// PostgresStore ops_runtime_logs 的手写存取层(表在迁移 0180)。
// 刻意不走 sqlc:运行日志独立于计费域,手写避免全量重生成扰动。

type PostgresStore struct {
	db db.DBTX
}

func NewPostgresStore(database db.DBTX) *PostgresStore {
	return &PostgresStore{db: database}
}

// InsertRuntimeLogs 批量插入。attrs 序列化失败的条目降级为空对象,不整批失败。
func (s *PostgresStore) InsertRuntimeLogs(ctx context.Context, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO ops_runtime_logs (created_at, level, component, message, request_id, attrs) VALUES `)
	args := make([]any, 0, len(entries)*6)
	for i, e := range entries {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 6
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, $%d)", base+1, base+2, base+3, base+4, base+5, base+6)
		attrs, err := json.Marshal(e.Attrs)
		if err != nil || len(e.Attrs) == 0 {
			attrs = []byte(`{}`)
		}
		created := e.Time
		if created.IsZero() {
			created = time.Now().UTC()
		}
		var requestID *string
		if strings.TrimSpace(e.RequestID) != "" {
			requestID = &e.RequestID
		}
		args = append(args, created, e.Level, e.Component, e.Message, requestID, attrs)
	}
	_, err := s.db.Exec(ctx, b.String(), args...)
	return err
}

// RuntimeLogRow 查询返回行。
type RuntimeLogRow struct {
	ID        int64           `json:"id"`
	CreatedAt time.Time       `json:"created_at"`
	Level     string          `json:"level"`
	Component string          `json:"component"`
	Message   string          `json:"message"`
	RequestID *string         `json:"request_id"`
	Attrs     json.RawMessage `json:"attrs"`
}

// ListParams 键集分页(新→旧):BeforeID>0 时取 id < BeforeID 的更旧页。
type ListParams struct {
	Level     string
	Component string
	RequestID string
	BeforeID  int64
	Limit     int32
}

// ListRuntimeLogs 按过滤条件取最新一页(id 降序)。
func (s *PostgresStore) ListRuntimeLogs(ctx context.Context, p ListParams) ([]RuntimeLogRow, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var b strings.Builder
	b.WriteString(`SELECT id, created_at, level, component, message, request_id, attrs FROM ops_runtime_logs WHERE 1=1`)
	args := make([]any, 0, 5)
	add := func(clause string, v any) {
		args = append(args, v)
		fmt.Fprintf(&b, clause, len(args))
	}
	if p.Level != "" {
		add(" AND level = $%d", p.Level)
	}
	if p.Component != "" {
		add(" AND component = $%d", p.Component)
	}
	if strings.TrimSpace(p.RequestID) != "" {
		add(" AND request_id = $%d", strings.TrimSpace(p.RequestID))
	}
	if p.BeforeID > 0 {
		add(" AND id < $%d", p.BeforeID)
	}
	add(" ORDER BY id DESC LIMIT $%d", limit)
	rows, err := s.db.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuntimeLogRow
	for rows.Next() {
		var r RuntimeLogRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.Level, &r.Component, &r.Message, &r.RequestID, &r.Attrs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CleanupRuntimeLogs 删除 before 之前的日志,返回删除行数(保留策略由运营者掌控)。
func (s *PostgresStore) CleanupRuntimeLogs(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM ops_runtime_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
