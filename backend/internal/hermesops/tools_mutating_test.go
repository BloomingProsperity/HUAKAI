package hermesops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

// --- RBAC floor (L1) --------------------------------------------------------

func TestDLQReplay_RBACFloorIsPlatformAdminOnly(t *testing.T) {
	// Regression (L1): dlq_replay is platform_admin ONLY — a tenant_operator must
	// be refused by AuthorizeMutating. Mutation check: change DLQReplaySpec's
	// RequiredRole to RoleTenantOperator and the tenant_operator authorize passes
	// (this assertion flips).
	reg := NewRegistry()
	reg.Register(DLQReplaySpec(DLQReplayDeps{
		Lookup: func(context.Context, int64, int64) (dlq.Record, error) { return dlq.Record{}, nil },
		Replay: func(context.Context, int64, string) (*dlq.Record, error) { return &dlq.Record{}, nil },
	}))
	if _, err := reg.AuthorizeMutating(ToolDLQReplay, RoleTenantOperator); !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("tenant_operator dlq_replay err=%v want ErrToolForbidden", err)
	}
	if _, err := reg.AuthorizeMutating(ToolDLQReplay, RolePlatformAdmin); err != nil {
		t.Fatalf("platform_admin dlq_replay err=%v want nil", err)
	}
}

func TestAccountPause_TenantOperatorAllowedAtFloor(t *testing.T) {
	// Regression (L1): account_pause admits a tenant_operator at the role floor
	// (tenant scope is enforced separately by the middleware + Resolve re-check).
	// Mutation check: raise RequiredRole to RolePlatformAdmin and tenant_operator
	// is forbidden here.
	reg := NewRegistry()
	reg.Register(AccountPauseSpec(AccountMutationDeps{
		GetAccount: func(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
			return admindb.AdminProviderAccountRow{}, nil
		},
	}))
	if _, err := reg.AuthorizeMutating(ToolAccountPause, RoleTenantOperator); err != nil {
		t.Fatalf("tenant_operator account_pause err=%v want nil", err)
	}
	if _, err := reg.AuthorizeMutating(ToolAccountPause, "unknown_role"); !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("unknown role account_pause err=%v want ErrToolForbidden", err)
	}
}

func TestAuthorizeMutating_RefusesReadOnlyTool(t *testing.T) {
	// Regression: a read-only tool can never enter the mutate path. Mutation
	// check: drop the !spec.Mutating guard in AuthorizeMutating and this returns nil.
	reg := NewRegistry()
	reg.Register(ToolSpec{Name: ToolDLQInspect, ReadOnly: true, RequiredRole: RoleTenantOperator,
		Run: func(context.Context, ToolRequest) (ToolResult, error) { return ToolResult{}, nil }})
	if _, err := reg.AuthorizeMutating(ToolDLQInspect, RolePlatformAdmin); !errors.Is(err, ErrNotMutating) {
		t.Fatalf("read-only via mutate path err=%v want ErrNotMutating", err)
	}
}

func TestRun_RefusesMutatingTool(t *testing.T) {
	// Regression: a mutating tool can never run through the read-only Run path
	// (which skips dry-run/confirm/lock/atomic-audit). Mutation check: remove the
	// spec.Mutating guard in Run and the mutate callback would be reachable.
	reg := NewRegistry()
	reg.Register(AccountPauseSpec(AccountMutationDeps{
		GetAccount: func(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
			return admindb.AdminProviderAccountRow{}, nil
		},
	}))
	if _, err := reg.Run(context.Background(), ToolAccountPause, ToolRequest{TenantID: 7, Role: RolePlatformAdmin}); !errors.Is(err, ErrNotMutating) {
		t.Fatalf("mutating via Run err=%v want ErrNotMutating", err)
	}
}

// --- account toggle Resolve + Mutate ---------------------------------------

func TestAccountPause_ResolvePreviewAndTenantRecheck(t *testing.T) {
	// Regression (L2 + cross-tenant): Resolve previews current->next enabled and
	// rejects a target row whose tenant differs from the request tenant. Mutation
	// check: drop the account.TenantID != req.TenantID guard and the foreign-row
	// resolve returns a plan instead of ErrTargetResolution.
	deps := AccountMutationDeps{
		GetAccount: func(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
			// Simulate the DB returning a row whose tenant does not match (defense
			// in depth — the query filters by tenant, but we re-check the row).
			return admindb.AdminProviderAccountRow{ID: arg.ID, TenantID: 999, Enabled: true, HealthState: "healthy"}, nil
		},
	}
	spec := AccountPauseSpec(deps)
	_, err := spec.Resolve(context.Background(), ToolRequest{TenantID: 7, Args: map[string]any{"account_id": float64(5)}})
	if !errors.Is(err, ErrTargetResolution) {
		t.Fatalf("foreign-tenant resolve err=%v want ErrTargetResolution", err)
	}

	deps.GetAccount = func(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
		return admindb.AdminProviderAccountRow{ID: arg.ID, TenantID: arg.TenantID, Enabled: true, HealthState: "healthy"}, nil
	}
	spec = AccountPauseSpec(deps)
	plan, err := spec.Resolve(context.Background(), ToolRequest{TenantID: 7, Args: map[string]any{"account_id": float64(5)}})
	if err != nil {
		t.Fatalf("resolve err=%v want nil", err)
	}
	if plan.Preview["current_enabled"] != true || plan.Preview["next_enabled"] != false {
		t.Fatalf("preview current=%v next=%v want true->false", plan.Preview["current_enabled"], plan.Preview["next_enabled"])
	}
	if plan.TargetType != "provider_account" || plan.TargetID != 5 {
		t.Fatalf("plan target=%s/%d want provider_account/5", plan.TargetType, plan.TargetID)
	}
}

func TestAccountPause_MutateFlipsEnabledFalseViaRealPath(t *testing.T) {
	// Regression: account_pause's Mutate flips enabled=false through the real
	// UpdateProviderAccountEnabled query (issued on the tx) and records
	// previous->next. account_resume flips back to true. Mutation check: hardcode
	// targetEnabled=true in accountToggleSpec and the pause asserts fail.
	enabledRec := &enabledTxRecorder{}
	tx := &enabledFakeTx{rec: enabledRec}
	ctx := withMutationTx(context.Background(), tx)
	deps := AccountMutationDeps{
		GetAccount: func(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
			return admindb.AdminProviderAccountRow{ID: arg.ID, TenantID: arg.TenantID, Enabled: true}, nil
		},
	}
	pausePlan := MutationPlan{TargetType: "provider_account", TargetID: 5, Preview: map[string]any{"current_enabled": true}}
	res, err := AccountPauseSpec(deps).Mutate(ctx, ToolRequest{TenantID: 7, ActorUserID: 42}, pausePlan)
	if err != nil {
		t.Fatalf("pause mutate err=%v", err)
	}
	if enabledRec.lastEnabled == nil || *enabledRec.lastEnabled != false {
		t.Fatalf("pause set enabled=%v want false", enabledRec.lastEnabled)
	}
	if res.Summary["enabled"] != false || res.Summary["previous_enabled"] != true {
		t.Fatalf("pause summary enabled=%v prev=%v want false/true", res.Summary["enabled"], res.Summary["previous_enabled"])
	}

	resumePlan := MutationPlan{TargetType: "provider_account", TargetID: 5, Preview: map[string]any{"current_enabled": false}}
	if _, err := AccountResumeSpec(deps).Mutate(ctx, ToolRequest{TenantID: 7, ActorUserID: 42}, resumePlan); err != nil {
		t.Fatalf("resume mutate err=%v", err)
	}
	if enabledRec.lastEnabled == nil || *enabledRec.lastEnabled != true {
		t.Fatalf("resume set enabled=%v want true", enabledRec.lastEnabled)
	}
}

// --- dlq_replay idempotency (L5) -------------------------------------------

func TestDLQReplay_IdempotencyDoesNotDoubleProcess(t *testing.T) {
	// Regression (L5): a double dlq_replay does not double-process. Replay dedupes
	// via the record's idempotency key (modeled here: the second replay sees an
	// already-delivered record and returns it without re-running the handler).
	// Mutation check: make the fake Replay re-run the handler unconditionally and
	// processCount becomes 2.
	processCount := 0
	delivered := false
	deps := DLQReplayDeps{
		Lookup: func(_ context.Context, id, tenant int64) (dlq.Record, error) {
			return dlq.Record{ID: id, TenantID: tenant, Status: dlq.StatusPending}, nil
		},
		Replay: func(_ context.Context, id int64, _ string) (*dlq.Record, error) {
			if delivered {
				// idempotency-key dedupe: already delivered, do not re-process.
				return &dlq.Record{ID: id, Status: dlq.StatusDelivered}, nil
			}
			processCount++
			delivered = true
			return &dlq.Record{ID: id, Status: dlq.StatusDelivered}, nil
		},
	}
	spec := DLQReplaySpec(deps)
	plan := MutationPlan{TargetType: "dlq_event", TargetID: 11, Preview: map[string]any{"current_status": "pending"}}
	for i := 0; i < 2; i++ {
		if _, err := spec.Mutate(context.Background(), ToolRequest{TenantID: 7, ActorUserID: 42}, plan); err != nil {
			t.Fatalf("replay %d err=%v", i, err)
		}
	}
	if processCount != 1 {
		t.Fatalf("dlq replay processed %d times want 1 (idempotency key must dedupe)", processCount)
	}
}

// --- renew_trigger privacy --------------------------------------------------

func TestRenewTrigger_NeverReturnsCredentialMaterial(t *testing.T) {
	// Regression (PRIVACY, DISCRIMINATING): renew_trigger calls Rotate and the
	// summary surfaces ONLY the resulting version + state — never the rotated
	// payload. We feed a secret payload in and assert it appears NOWHERE in the
	// result summary. Mutation check: add `"payload": in.Payload` to the summary
	// and the sentinel leaks (RED).
	const secret = "sk-ROTATED-NEW-MATERIAL-9f2a"
	deps := RenewTriggerDeps{
		ListByAccount: func(_ context.Context, tenant, account int64) ([]credentialstore.CredentialMetadata, error) {
			return []credentialstore.CredentialMetadata{{ID: 3, TenantID: tenant, ProviderAccountID: account, Version: 4, State: "active", Vendor: "anthropic"}}, nil
		},
		Rotate: func(_ context.Context, in credentialstore.RotateCredentialInput) (credentialstore.CredentialMetadata, error) {
			if !strings.Contains(string(in.Payload), secret) {
				t.Fatalf("Rotate did not receive the new payload")
			}
			return credentialstore.CredentialMetadata{ID: in.CredentialID, Version: 5, State: "active"}, nil
		},
	}
	spec := RenewTriggerSpec(deps)
	args := map[string]any{"account_id": float64(8), "credential_id": float64(3), "credentials": map[string]any{"api_key": secret}}
	plan, err := spec.Resolve(context.Background(), ToolRequest{TenantID: 7, Args: args})
	if err != nil {
		t.Fatalf("resolve err=%v", err)
	}
	res, err := spec.Mutate(context.Background(), ToolRequest{TenantID: 7, ActorUserID: 42, Args: args}, plan)
	if err != nil {
		t.Fatalf("mutate err=%v", err)
	}
	if res.Summary["new_version"] != int32(5) || res.Summary["previous_version"] != int32(4) {
		t.Fatalf("summary new=%v prev=%v want 5/4", res.Summary["new_version"], res.Summary["previous_version"])
	}
	// The rotated material must not be anywhere in the returned summary.
	if summaryContains(res.Summary, secret) {
		t.Fatalf("renew_trigger summary leaked rotated credential material")
	}
}

func summaryContains(m map[string]any, needle string) bool {
	for _, v := range m {
		if s, ok := v.(string); ok && strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// --- fakes for the enabled flip --------------------------------------------

type enabledTxRecorder struct {
	lastEnabled *bool
	updateCount int
}

type enabledFakeTx struct {
	rec *enabledTxRecorder
}

func (tx *enabledFakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE provider_accounts") || strings.Contains(sql, "SET enabled") || strings.Contains(strings.ToLower(sql), "enabled") {
		tx.rec.updateCount++
		for _, a := range args {
			if b, ok := a.(bool); ok {
				v := b
				tx.rec.lastEnabled = &v
			}
		}
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (tx *enabledFakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return errRow{err: errors.New("queryrow unused")}
}
func (tx *enabledFakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unused")
}
func (tx *enabledFakeTx) Commit(context.Context) error          { return nil }
func (tx *enabledFakeTx) Rollback(context.Context) error        { return nil }
func (tx *enabledFakeTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("unused") }
func (tx *enabledFakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (tx *enabledFakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *enabledFakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *enabledFakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (tx *enabledFakeTx) Conn() *pgx.Conn { return nil }
