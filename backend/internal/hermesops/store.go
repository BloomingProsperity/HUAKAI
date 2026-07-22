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
	TenantID    int64
	ActorSource string
	ActorID     int64
	ActorRole   string
	ToolName    string
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
	// DryRun 区分改动预览与真实执行；只读工具始终为 false。
	DryRun bool
	// LogCategory 使用全局日志分类；空值按调用结果自动归类。
	LogCategory string
}

// RecordToolCall 脱敏参数和结果摘要，并追加一条 hermes_tool_calls 行。成功、错误和
// 拒绝共用这条持久化路径；敏感键在写库前再次打码。
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
		ActorSource:   rec.ActorSource,
		ActorID:       rec.ActorID,
		ActorRole:     rec.ActorRole,
		ToolName:      rec.ToolName,
		RequestedArgs: argsJSON,
		ResultStatus:  string(rec.Status),
		ResultSummary: summaryJSON,
		CorrelationID: nilIfEmpty(rec.CorrelationID),
		RequestID:     nilIfEmpty(rec.RequestID),
		ErrorClass:    nilIfEmpty(rec.ErrorClass),
		CalledAt:      pgtype.Timestamptz{Time: called.UTC(), Valid: true},
		DryRun:        rec.DryRun,
		LogCategory:   toolLogCategory(rec.Status, rec.LogCategory),
	}
	if !rec.ReturnedAt.IsZero() {
		params.ReturnedAt = pgtype.Timestamptz{Time: rec.ReturnedAt.UTC(), Valid: true}
	}

	_, err = inserter.InsertHermesToolCall(ctx, params)
	return err
}

func toolLogCategory(status ResultStatus, explicit string) string {
	if explicit != "" {
		return explicit
	}
	switch status {
	case ResultDenied:
		return "security"
	case ResultError:
		return "error"
	default:
		return "operation"
	}
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
