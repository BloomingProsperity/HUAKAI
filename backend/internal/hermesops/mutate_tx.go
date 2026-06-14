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
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops/mutateguard"
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

	// OwnTx flags a tool whose underlying mutation commits in its OWN separate
	// transaction (committed internally BEFORE the orchestrator commit) rather
	// than running inside the orchestrator tx. For such a tool, the mutation has
	// already persisted by the time the orchestrator reaches its own COMMIT, so a
	// commit-phase fault leaves the mutation applied while the audit rows roll
	// back — a "commit_uncertain" condition (reconciliation needed), NOT a
	// "mutation_failed" one. For an in-tx tool the same fault rolls the mutation
	// back atomically, so it stays mutation_failed. IsOwnTxMutation derives this.
	OwnTx bool
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
	// sem caps process-wide mutating concurrency BELOW the pool MaxConns so a
	// burst of mutations cannot exhaust the shared pgxpool / advisory-lock slots
	// and brown out the core gateway. A nil sem (or a disabled one) is the legacy
	// unbounded behavior.
	sem *mutateguard.Semaphore
	// txDeadline bounds a single mutation transaction (client-side ctx deadline +
	// a server-side SET LOCAL statement_timeout). Zero is the disabled sentinel:
	// no deadline, no statement_timeout — byte-for-byte legacy behavior.
	txDeadline time.Duration
	// acquireWait bounds how long Execute waits for a concurrency slot before
	// returning ErrMutateBusy. Zero falls back to the parent ctx deadline only.
	acquireWait time.Duration
}

// MutateOption configures the orchestrator additively. With no options the
// orchestrator is byte-for-byte the legacy unbounded, no-deadline orchestrator
// (every guard carries a disable sentinel).
type MutateOption func(*MutateOrchestrator)

// WithConcurrencyGuard caps concurrent mutations (acquired BEFORE BeginTx). A nil
// or disabled Semaphore is a no-op (legacy unbounded). acquireWait bounds the
// wait for a slot before ErrMutateBusy; zero uses the parent ctx deadline only.
func WithConcurrencyGuard(sem *mutateguard.Semaphore, acquireWait time.Duration) MutateOption {
	return func(o *MutateOrchestrator) {
		o.sem = sem
		o.acquireWait = acquireWait
	}
}

// WithTxDeadline bounds a single mutation tx (client ctx deadline + server
// statement_timeout). Zero/negative disables it (legacy: no deadline).
func WithTxDeadline(d time.Duration) MutateOption {
	return func(o *MutateOrchestrator) {
		if d > 0 {
			o.txDeadline = d
		}
	}
}

// NewMutateOrchestrator builds the orchestrator over a transaction beginner
// (the pgx pool). A nil beginner makes Execute fail closed. With no options it is
// byte-for-byte the legacy unbounded behavior; pass WithConcurrencyGuard /
// WithTxDeadline to enable the additive S2 guards.
func NewMutateOrchestrator(begin txBeginner, opts ...MutateOption) *MutateOrchestrator {
	o := &MutateOrchestrator{begin: begin}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// errAuditUnavailable is returned when the orchestrator has no transaction
// beginner wired — a mutation must never proceed without the atomic audit path.
var errAuditUnavailable = errors.New("hermesops: mutation audit transaction unavailable")

// ErrCommitAfterOwnTxMutation marks the specific residual fault where an OWN-TX
// tool's mutation already committed in its own transaction, the orchestrator
// then inserted its audit rows successfully, but the FINAL orchestrator COMMIT
// failed (transport/connection fault). The mutation persisted while the audit
// rows rolled back, so the outcome is uncertain (reconciliation needed) — it is
// NOT a "the mutation did not happen" failure. The HTTP layer maps this to the
// best-effort error_class "commit_uncertain". Only Execute wraps a commit-phase
// fault with this sentinel, and only when rec.OwnTx is set; an in-tx tool's
// commit fault rolls the mutation back atomically and stays mutation_failed.
var ErrCommitAfterOwnTxMutation = errors.New("hermesops: orchestrator commit failed after own-tx mutation persisted")

// ErrMutateBusy marks an acquire-timeout on the mutating concurrency cap: too
// many mutations are already in flight and the bounded acquire window elapsed
// before a slot freed. It is a clean back-pressure signal (mapped to HTTP 429
// upstream), NOT a failed mutation — nothing was begun. Only returned when the
// concurrency guard is enabled.
var ErrMutateBusy = errors.New("hermesops: mutating concurrency saturated, retry")

// ErrMutateTimeoutUncertain is the OWN-TX analogue of ErrCommitAfterOwnTxMutation
// for the tx-deadline path: an own-tx tool (dlq_replay/renew_trigger) hit the tx
// deadline AFTER its inner own-tx already committed. The mutation may have
// persisted while the orchestrator audit rows roll back, so the outcome is
// UNCERTAIN (reconciliation needed) and MUST NOT be reported as a clean
// "rolled back"/mutation_failed. The HTTP layer maps it to the best-effort
// error_class "mutate_timeout_uncertain". An in-tx tool's deadline rolls the
// mutation back atomically and stays the timeout/mutation_failed default.
var ErrMutateTimeoutUncertain = errors.New("hermesops: mutation tx deadline hit after own-tx mutation may have persisted")

// IsOwnTxMutation reports whether a mutating tool runs its mutation in its OWN
// separate transaction (committed internally before the orchestrator commit)
// rather than inside the orchestrator transaction. This is the single source of
// truth for the tx-mode of each H4 mutating tool, kept next to the tool-name
// constants so the orchestrator-commit-failure classification (commit_uncertain
// vs mutation_failed) cannot drift from how each tool actually persists.
//
//   - account_pause / account_resume run their enabled-flip INSIDE the
//     orchestrator tx (via txFromContext) — in-tx, atomic with the audit rows.
//   - dlq_replay / renew_trigger delegate to dlq.Service.Replay /
//     credentialstore.Store.Rotate, each of which owns its transaction and
//     commits before returning — own-tx.
func IsOwnTxMutation(toolName string) bool {
	switch toolName {
	case ToolDLQReplay, ToolRenewTrigger:
		return true
	default:
		return false
	}
}

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

	// S2 (a) CONCURRENCY CAP: reserve a mutating slot BEFORE BeginTx so the cap
	// bounds how many mutations HOLD a pool connection / advisory-lock slot at
	// once (acquiring after BeginTx would already have taken the conn). A disabled
	// guard is a no-op (legacy unbounded). On acquire-timeout return ErrMutateBusy
	// clean — nothing was begun, so this is pure back-pressure, never a hang.
	release, err := o.sem.Acquire(ctx, o.acquireWait)
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: %w: %w", ErrMutateBusy, err)
	}
	defer release()

	tx, err := o.begin.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: begin mutation tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Roll back on its OWN independent ctx so a tripped tx deadline (mutCtx
			// done) still lets the rollback run — releasing the conn + advisory lock.
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rollbackCtx)
		}
	}()

	// S2 (b) TX DEADLINE: bound THIS mutation tx with both a client-side ctx
	// deadline (mutCtx, threaded through the lock/audit/mutate/commit below) and a
	// server-side SET LOCAL statement_timeout. SET LOCAL is scoped to THIS tx only
	// and auto-resets at tx end, so it never touches core-path connections. Zero
	// deadline = disabled (mutCtx == ctx, no statement_timeout) — legacy behavior.
	mutCtx := ctx
	if o.txDeadline > 0 {
		var cancel context.CancelFunc
		mutCtx, cancel = context.WithTimeout(ctx, o.txDeadline)
		defer cancel()
		millis := o.txDeadline.Milliseconds()
		if _, err := tx.Exec(mutCtx, "SET LOCAL statement_timeout = $1", millis); err != nil {
			return ToolResult{}, fmt.Errorf("hermesops: set mutation statement_timeout: %w", err)
		}
	}

	// L4: serialize concurrent mutations on the SAME target. The lock is held for
	// the lifetime of this tx (released on commit/rollback), so a second operator
	// or replica blocks until this mutation + audit commit or abort.
	if _, err := tx.Exec(mutCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: acquire advisory lock: %w", err)
	}

	// L3: write + VERIFY the audit rows BEFORE the mutation. If either insert
	// fails, we return here with the tx rolled back and the mutation never run.
	if err := insertToolCallRow(mutCtx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: tool-call audit insert failed: %w", err)
	}
	if err := insertAdminAuditRow(mutCtx, tx, rec); err != nil {
		return ToolResult{}, fmt.Errorf("hermesops: admin audit insert failed: %w", err)
	}

	// Mutation runs only after the audit rows are accepted by the database. The
	// tx is threaded via context so a tool's Mutate can run tx-bound writes
	// (e.g. provider_accounts.enabled flip) atomically with the audit rows.
	result, mErr := mutate(withMutationTx(mutCtx, tx), tx)
	if mErr != nil {
		return ToolResult{}, o.classifyMutateErr(rec, mErr)
	}

	if err := tx.Commit(mutCtx); err != nil {
		// The mutation already ran (mErr was nil). If this tool committed its
		// mutation in its OWN transaction before this point, that mutation has
		// persisted even though this commit (carrying the audit rows) failed —
		// classify it commit_uncertain so reconciliation is signalled rather than
		// reporting "mutation_failed". An in-tx tool's mutation is part of THIS tx,
		// so this commit failure rolls it back atomically and stays the default.
		if rec.OwnTx {
			return ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w: %w", ErrCommitAfterOwnTxMutation, err)
		}
		return ToolResult{}, fmt.Errorf("hermesops: commit mutation tx: %w", err)
	}
	committed = true
	return result, nil
}

// classifyMutateErr wraps a mutate()-phase error for the tx-deadline path.
//
// The dangerous case it guards (correctness constraint): an OWN-TX tool
// (dlq_replay/renew_trigger) whose inner own-tx ALREADY COMMITTED before the
// orchestrator's tx deadline (mutCtx) tripped a later step. The mutation may
// have persisted, so the deadline error MUST be classified as
// ErrMutateTimeoutUncertain (reconciliation needed) — NEVER as a clean rolled-back
// mutation_failed, which would falsely claim the replay/rotate did not happen.
//
// This only fires when (1) the tx deadline is enabled, (2) the error is a
// context deadline, and (3) the tool is own-tx. An in-tx tool's deadline rolls
// the mutation back atomically (it is part of THIS tx), so it is returned
// unwrapped and stays the timeout/mutation_failed default upstream. A non-deadline
// mutate error (a real tool failure) is also returned unchanged.
func (o *MutateOrchestrator) classifyMutateErr(rec MutationAuditRecord, mErr error) error {
	if o.txDeadline <= 0 || !rec.OwnTx {
		return mErr
	}
	if !errors.Is(mErr, context.DeadlineExceeded) {
		return mErr
	}
	return fmt.Errorf("hermesops: own-tx mutation deadline: %w: %w", ErrMutateTimeoutUncertain, mErr)
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
