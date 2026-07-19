package logsink

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// PostgresStore 是 ops_runtime_logs 的手写存取层，结构化合同由迁移 0195 固化。
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
	normalized := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if value, ok := normalizeEntry(entry); ok {
			normalized = append(normalized, value)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString(`INSERT INTO ops_runtime_logs (
        created_at, level, log_category, event_type, result, error_class, error_code, retryable,
        component, message, actor_kind, actor_ref, tenant_id, target_type, target_ref,
        request_id, trace_id, upstream_request_id, idempotency_key, recovery_state, attrs
    ) VALUES `)
	const columnCount = 21
	args := make([]any, 0, len(normalized)*columnCount)
	for i, e := range normalized {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * columnCount
		b.WriteByte('(')
		for column := 1; column <= columnCount; column++ {
			if column > 1 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", base+column)
		}
		b.WriteByte(')')
		attrs, err := json.Marshal(e.Attrs)
		if err != nil || len(e.Attrs) == 0 {
			attrs = []byte(`{}`)
		}
		created := e.Time
		if created.IsZero() {
			created = time.Now().UTC()
		}
		args = append(args,
			created, e.Level, e.Category, e.EventType, e.Result, e.ErrorClass, e.ErrorCode, e.Retryable,
			e.Component, e.Message, e.ActorKind, optionalString(e.ActorRef), e.TenantID,
			optionalString(e.TargetType), optionalString(e.TargetRef), optionalString(e.RequestID),
			optionalString(e.TraceID), optionalString(e.UpstreamRequestID), optionalString(e.IdempotencyKey),
			e.RecoveryState, attrs,
		)
	}
	_, err := s.db.Exec(ctx, b.String(), args...)
	return err
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

// RuntimeLogRow 查询返回行。
type RuntimeLogRow struct {
	ID                int64           `json:"id"`
	CreatedAt         time.Time       `json:"created_at"`
	IngestedAt        time.Time       `json:"ingested_at"`
	Level             string          `json:"level"`
	Category          string          `json:"category"`
	EventType         string          `json:"event_type"`
	Result            string          `json:"result"`
	ErrorClass        string          `json:"error_class"`
	ErrorCode         string          `json:"error_code"`
	Retryable         bool            `json:"retryable"`
	Component         string          `json:"component"`
	Message           string          `json:"message"`
	ActorKind         string          `json:"actor_kind"`
	ActorRef          *string         `json:"actor_ref"`
	TenantID          *int64          `json:"tenant_id"`
	TargetType        *string         `json:"target_type"`
	TargetRef         *string         `json:"target_ref"`
	RequestID         *string         `json:"request_id"`
	TraceID           *string         `json:"trace_id"`
	UpstreamRequestID *string         `json:"upstream_request_id"`
	IdempotencyKey    *string         `json:"idempotency_key"`
	RecoveryState     string          `json:"recovery_state"`
	Attrs             json.RawMessage `json:"attrs"`
}

// ListParams 键集分页(新→旧):BeforeID>0 时取 id < BeforeID 的更旧页。
type ListParams struct {
	Level             string
	Category          string
	EventType         string
	Result            string
	ErrorClass        string
	ErrorCode         string
	Component         string
	ActorKind         string
	TenantID          int64
	RequestID         string
	TraceID           string
	UpstreamRequestID string
	IdempotencyKey    string
	RecoveryState     string
	BeforeID          int64
	Limit             int32
}

// ListRuntimeLogs 按过滤条件取最新一页(id 降序)。
func (s *PostgresStore) ListRuntimeLogs(ctx context.Context, p ListParams) ([]RuntimeLogRow, error) {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var b strings.Builder
	b.WriteString(`SELECT id, created_at, ingested_at, level, log_category, event_type, result,
        error_class, error_code, retryable, component, message, actor_kind, actor_ref, tenant_id,
        target_type, target_ref, request_id, trace_id, upstream_request_id, idempotency_key,
        recovery_state, attrs
        FROM ops_runtime_logs WHERE 1=1`)
	args := make([]any, 0, 16)
	add := func(clause string, v any) {
		args = append(args, v)
		fmt.Fprintf(&b, clause, len(args))
	}
	if p.Level != "" {
		add(" AND level = $%d", p.Level)
	}
	if p.Category != "" {
		add(" AND log_category = $%d", p.Category)
	}
	if p.EventType != "" {
		add(" AND event_type = $%d", p.EventType)
	}
	if p.Result != "" {
		add(" AND result = $%d", p.Result)
	}
	if p.ErrorClass != "" {
		add(" AND error_class = $%d", p.ErrorClass)
	}
	if p.ErrorCode != "" {
		add(" AND error_code = $%d", p.ErrorCode)
	}
	if p.Component != "" {
		add(" AND component = $%d", p.Component)
	}
	if p.ActorKind != "" {
		add(" AND actor_kind = $%d", p.ActorKind)
	}
	if p.TenantID > 0 {
		add(" AND tenant_id = $%d", p.TenantID)
	}
	if strings.TrimSpace(p.RequestID) != "" {
		add(" AND request_id = $%d", strings.TrimSpace(p.RequestID))
	}
	if strings.TrimSpace(p.TraceID) != "" {
		add(" AND trace_id = $%d", strings.TrimSpace(p.TraceID))
	}
	if strings.TrimSpace(p.UpstreamRequestID) != "" {
		add(" AND upstream_request_id = $%d", strings.TrimSpace(p.UpstreamRequestID))
	}
	if strings.TrimSpace(p.IdempotencyKey) != "" {
		add(" AND idempotency_key = $%d", strings.TrimSpace(p.IdempotencyKey))
	}
	if p.RecoveryState != "" {
		add(" AND recovery_state = $%d", p.RecoveryState)
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
		if err := rows.Scan(
			&r.ID, &r.CreatedAt, &r.IngestedAt, &r.Level, &r.Category, &r.EventType, &r.Result,
			&r.ErrorClass, &r.ErrorCode, &r.Retryable, &r.Component, &r.Message, &r.ActorKind,
			&r.ActorRef, &r.TenantID, &r.TargetType, &r.TargetRef, &r.RequestID, &r.TraceID,
			&r.UpstreamRequestID, &r.IdempotencyKey, &r.RecoveryState, &r.Attrs,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
