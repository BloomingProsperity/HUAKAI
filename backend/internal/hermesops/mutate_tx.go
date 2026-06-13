package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	hermestoolsdb "github.com/BloomingProsperity/HUAKAI/internal/db/hermestoolsdb"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

// txBeginner is the narrow pool surface the mutating orchestrator needs to open
// its own transaction. *pgxpool.Pool satisfies it; the interface keeps the
// orchestrator unit-testable with a fake begin/commit/rollback recorder.
type txBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// MutationAuditRecord is the atomic audit footprint of one confirmed mutation:
// the hermes_tool_calls row AND the admin_audit_events mirror, written inside
// the orchestrator transaction so neither can exist without the other (and,
// per the ordering below, without the mutation).
type MutationAuditRecord struct {
	// Tool-call ledger fields (hermes_tool_calls).
	TenantID          int64
	ActorUserID       int64
	AdminActorTokenID int64
	ToolName          string
	Args              map[string]any
	ResultSummary     map[string]any
	Status            ResultStatus
	ErrorClass        string
	CorrelationID     string
	RequestID         string
	CalledAt          time.Time
	ReturnedAt        time.Time
	DryRun            bool

	// admin_audit_events mirror fields.
	AdminAction string // e.g. hermes.tool.account_pause
	AdminRole   string
	TargetType  string
	TargetID    int64
	// AuditPayload is the sanitized previous->next state payload. Sanitized again
	// before insert as defense in depth.
	AuditPayload map[string]any
}

// MutateOrchestrator runs a confirmed mutating tool under L3 (atomic audit) +
// L4 (advisory lock). It owns a single transaction that:
//
//  1. acquires a per-target pg advisory xact lock (serializing concurrent
//     mutations on the SAME target across operators/replicas),
//  2. inserts the hermes_tool_calls row + the admin_audit_events mirror and
//     VERIFIES both inserts succeed,
//  3. only THEN executes the mutation,
//  4. commits iff the mutation succeeded; rolls back otherwise.
//
// Because the audit inserts are validated BEFORE the mutation runs, a broken
// audit path aborts the request with the target UNCHANGED (fail-closed) — a
// mutation can never be applied without a durable audit row first being
// accepted by the database. The residual window (mutation succeeds, then the
// final COMMIT fails on a transport fault) is documented as a known risk; the
// audit rows are already DB-accepted at that point, so it is a connection
// fault, not a silent missing-audit.
type MutateOrchestrator struct {
	begin txBeginner
}

// NewMutateOrchestrator builds the orchestrator over a transaction beginner
// (the pgx pool). A nil beginner makes Execute fail closed.
func NewMutateOrchestrator(begin txBeginner) *MutateOrchestrator {
	return &MutateOrchestrator{begin: begin}
}

// errAuditUnavailable is returned when the orchestrator has no transaction
// beginner wired — a mutation must never proceed without the atomic audit path.
var errAuditUnavailable = errors.New("hermesops: mutation audit transaction unavailable")

// Execute runs mutate() inside the atomic-audit + advisory-lock transaction.
// mutate receives the request and the resolved plan and returns the final
// post-mutation summary; it is invoked exactly once, only after the audit rows
// are accepted, and only while the advisory lock is held. lockKey discriminates
// the advisory lock (L4).
//
// On any error before/at the mutation, the whole transaction rolls back so no
// audit row (and no lock side effect) persists for a mutation that did not
// happen. The returned summary is the mutate() summary on success.
func (o *MutateOrchestrator) Execute(
	ctx context.Context,
	lockKey string,
	rec MutationAuditRecord,
	mutate func(ctx context.Context, tx pgx.Tx) (ToolResult, error),
) (ToolResult, error) {
	if o == nil || o.begin == nil {
		return ToolResult{}, errAuditUnavailable
	}
	tx, err := o.begin.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: begin mutation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	// L4: serialize concurrent mutations on the SAME target. The lock is held for
	// the lifetime of this tx (released on commit/rollback), so a second operator
	// or replica blocks until this mutation + audit commit or abort.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: acquire advisory lock: %w", err)
	}

	// L3: write + VERIFY the audit rows BEFORE the mutation. If either insert
	// fails, we return here with the tx rolled back and the mutation never run.
	if err := insertToolCallRow(ctx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: tool-call audit insert failed: %w", err)
	}
	if err := insertAdminAuditRow(ctx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: admin audit insert failed: %w", err)
	}

	// Mutation runs only after the audit rows are accepted by the database. The
	// tx is threaded via context so a tool's Mutate can run tx-bound writes
	// (e.g. provider_accounts.enabled flip) atomically with the audit rows.
	result, mErr := mutate(withMutationTx(ctx, tx), tx)
	if mErr != nil {
		return ToolResult{}, mErr
	}

	if err := tx.Commit(ctx); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w", err)
	}
	committed = true
	return result, nil
}

// insertToolCallRow appends the sanitized hermes_tool_calls row on the tx.
func insertToolCallRow(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) error {
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
	_, err = hermestoolsdb.New(tx).InsertHermesToolCall(ctx, params)
	return err
}

// insertAdminAuditRow mirrors the mutation into admin_audit_events on the same
// tx. The action MUST be in the migration whitelist; the payload is sanitized
// (previous->next state, enums/ids only — never secrets).
func insertAdminAuditRow(ctx context.Context, tx pgx.Tx, rec MutationAuditRecord) error {
	payload := hermes.SanitizeArgs(rec.AuditPayload)
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("hermesops: admin audit payload not json encodable: %w", err)
	}
	tenant := rec.TenantID
	actorID := fmt.Sprintf("%d", rec.AdminActorTokenID)
	reqID := nilIfEmpty(rec.RequestID)
	targetID := rec.TargetID
	_, err = admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
		TenantID:   &tenant,
		ActorID:    actorID,
		ActorRole:  rec.AdminRole,
		Action:     rec.AdminAction,
		TargetType: rec.TargetType,
		TargetID:   &targetID,
		RequestID:  reqID,
		Reason:     nil,
		Payload:    raw,
	})
	return err
}
