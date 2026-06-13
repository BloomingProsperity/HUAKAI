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

// ErrAuditStoreUnavailable is returned when the tool-call audit store is unwired.
var ErrAuditStoreUnavailable = errors.New("hermesops: tool-call audit store unavailable")

// ToolCallInserter is the narrow write surface for the hermes_tool_calls ledger.
// *hermestoolsdb.Queries satisfies it; the interface keeps the recorder
// unit-testable with a fake and lets the HTTP layer pass either the pool-backed
// Queries or a tx-bound one.
type ToolCallInserter interface {
	InsertHermesToolCall(ctx context.Context, arg hermestoolsdb.InsertHermesToolCallParams) (hermestoolsdb.InsertHermesToolCallRow, error)
}

// ToolCallAudit is the sanitized record of one tool invocation (or denial).
type ToolCallAudit struct {
	TenantID          int64
	ActorUserID       int64
	AdminActorTokenID int64 // 0 => not an admin-mode actor (recorded NULL)
	ToolName          string
	// Args is the RAW request arg map; the recorder sanitizes it before insert.
	Args map[string]any
	// ResultSummary is the tool's structured summary; sanitized before insert.
	ResultSummary map[string]any
	Status        ResultStatus
	ErrorClass    string
	CorrelationID string
	RequestID     string
	CalledAt      time.Time
	ReturnedAt    time.Time
}

// RecordToolCall sanitizes the args + summary and appends one hermes_tool_calls
// row. It is the single persistence path: success, error, AND denial all flow
// through here so every invocation is recorded. Sanitization (hermes.SanitizeArgs)
// is applied as defense-in-depth even though tools already emit diagnostic-only
// summaries — a tool that accidentally surfaced a "token"/"secret"/"password"
// key would still be redacted before it touches the row.
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

// sanitizedJSON applies the hermes sensitive-key sanitizer then JSON-encodes.
// A nil/empty map yields nil bytes (persisted as SQL NULL).
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
