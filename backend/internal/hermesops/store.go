package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

// ErrAuditStoreUnavailable 在 tool-call 审计 store 未接线时返回。
var ErrAuditStoreUnavailable = errors.New("hermesops: tool-call audit store unavailable")

// ToolCallInserter 是 hermes_tool_calls 流水的窄写表面。*hermestoolsdb.Queries 满足它;
// 该接口让 recorder 可用一个 fake 做单元测试,并让 HTTP 层既能传入由连接池支撑的 Queries、
// 也能传入受事务约束的那个。
type ToolCallInserter interface {
	InsertHermesToolCall(ctx context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error)
}

// ToolCallAudit 是一次工具调用(或拒绝)的已脱敏记录。
type ToolCallAudit struct {
	TenantID          int64
	ActorUserID       int64
	AdminActorTokenID int64 // 0 => 非 admin 模式 actor(记录为 NULL)
	ToolName          string
	// Args 是 RAW(原始)的请求参数 map;recorder 在 insert 前脱敏它。
	Args map[string]any
	// ResultSummary 是工具的结构化 summary;insert 前脱敏。
	ResultSummary map[string]any
	Status        ResultStatus
	ErrorClass    string
	CorrelationID string
	RequestID     string
	CalledAt      time.Time
	ReturnedAt    time.Time
	// DryRun 标记一条 WAVE H4 mutating 工具的 PREVIEW(预览)行(true),区别于一条真正执行 /
	// 只读的行(false)。只读工具始终把它留为 false。
	DryRun bool
}

// RecordToolCall 脱敏 args + summary,并追加一条 hermes_tool_calls 行。它是唯一的持久化路径:
// 成功、出错 AND(以及)拒绝都流经此处,这样每一次调用都被记录。脱敏(hermes.SanitizeArgs)作为
// 纵深防御施加,即便工具已经只输出诊断性的 summary——某个工具若意外暴露了 "token"/"secret"/
// "password" 键,在它触及该行之前仍会被打码。
func RecordToolCall(ctx context.Context, inserter ToolCallInserter, rec ToolCallAudit) error {
	if inserter == nil {
		return ErrAuditStoreUnavailable
	}

	argsJSON, err := sanitizedJSON(rec.Args)
	if err != nil {
		return err
	}
	summaryJSON, err := sanitizedJSON(rec.ResultSummary)
	if err != nil {
		return err
	}

	called := rec.CalledAt
	if called.IsZero() {
		called = time.Now()
	}

	params := hermestoolsdb.InsertHermesToolCallParams{
		TenantID:      rec.TenantID,
		ActorUserID:   rec.ActorUserID,
		ToolName:      rec.ToolName,
		RequestedArgs: argsJSON,
		ResultStatus:  string(rec.Status),
		ResultSummary: summaryJSON,
		CorrelationID: nilIfEmpty(rec.CorrelationID),
		RequestID:     nilIfEmpty(rec.RequestID),
		ErrorClass:    nilIfEmpty(rec.ErrorClass),
		CalledAt:      pgtype.Timestamptz{Time: called.UTC(), Valid: true},
		DryRun:        rec.DryRun,
	}
	if rec.AdminActorTokenID > 0 {
		id := rec.AdminActorTokenID
		params.AdminActorTokenID = &id
	}
	if !rec.ReturnedAt.IsZero() {
		params.ReturnedAt = pgtype.Timestamptz{Time: rec.ReturnedAt.UTC(), Valid: true}
	}

	_, err = inserter.InsertHermesToolCall(ctx, params)
	return err
}

// sanitizedJSON 先施加 hermes 的敏感键 sanitizer,再做 JSON 编码。nil/空 map 产出 nil 字节
// (持久化为 SQL NULL)。
func sanitizedJSON(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	clean := hermes.SanitizeArgs(m)
	raw, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
