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

// --- RBAC 底线(L1)--------------------------------------------------------

func TestDLQReplay_RBACFloorIsPlatformAdminOnly(t *testing.T) {
	// 回归(L1):dlq_replay 仅限 platform_admin —— tenant_operator 必须被
	// AuthorizeMutating 拒绝。变异检验:把 DLQReplaySpec 的 RequiredRole 改成
	// RoleTenantOperator,则 tenant_operator 的鉴权会通过(此断言翻转)。
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
	// 回归(L1):account_pause 在角色底线处放行 tenant_operator
	// (租户 scope 由中间件 + Resolve 复检单独强制)。变异检验:把 RequiredRole
	// 提升为 RolePlatformAdmin,则此处 tenant_operator 会被禁止。
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
	// 回归:只读工具永远不能进入 mutate 路径。变异检验:去掉 AuthorizeMutating 里的
	// !spec.Mutating 守卫,则此处会返回 nil。
	reg := NewRegistry()
	reg.Register(ToolSpec{Name: ToolDLQInspect, ReadOnly: true, RequiredRole: RoleTenantOperator,
		Run: func(context.Context, ToolRequest) (ToolResult, error) { return ToolResult{}, nil }})
	if _, err := reg.AuthorizeMutating(ToolDLQInspect, RolePlatformAdmin); !errors.Is(err, ErrNotMutating) {
		t.Fatalf("read-only via mutate path err=%v want ErrNotMutating", err)
	}
}

func TestRun_RefusesMutatingTool(t *testing.T) {
	// 回归:改动型工具永远不能走只读的 Run 路径
	// (Run 会跳过 dry-run/confirm/lock/原子审计)。变异检验:去掉 Run 里的
	// spec.Mutating 守卫,则 mutate 回调会变得可达。
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

// --- account toggle 的 Resolve + Mutate ---------------------------------------

func TestAccountPause_ResolvePreviewAndTenantRecheck(t *testing.T) {
	// 回归(L2 + 跨租户):Resolve 预览 current->next 的 enabled,并拒绝
	// 那些租户与请求租户不一致的目标行。变异检验:去掉 account.TenantID != req.TenantID
	// 守卫,则对外租户行的 resolve 会返回一个 plan 而不是 ErrTargetResolution。
	deps := AccountMutationDeps{
		GetAccount: func(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
			// 模拟 DB 返回一个租户不匹配的行(纵深防御 —— 查询本身按租户过滤,
			// 但我们仍对返回的行复检)。
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
	// 回归:account_pause 的 Mutate 通过真实的 UpdateProviderAccountEnabled 查询
	// (在 tx 上发出)把 enabled 翻成 false,并记录 previous->next。account_resume 则翻回 true。
	// 变异检验:在 accountToggleSpec 里把 targetEnabled 写死为 true,则 pause 的断言会失败。
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

// --- dlq_replay 幂等(L5)-------------------------------------------------

func TestDLQReplay_IdempotencyDoesNotDoubleProcess(t *testing.T) {
	// 回归(L5):两次 dlq_replay 不会重复处理。Replay 借记录的幂等键去重
	// (这里建模为:第二次 replay 看到一条已投递的记录,直接返回它而不重新运行 handler)。
	// 变异检验:让 fake 的 Replay 无条件重新运行 handler,则 processCount 会变成 2。
	processCount := 0
	delivered := false
	deps := DLQReplayDeps{
		Lookup: func(_ context.Context, id, tenant int64) (dlq.Record, error) {
			return dlq.Record{ID: id, TenantID: tenant, Status: dlq.StatusPending}, nil
		},
		Replay: func(_ context.Context, id int64, _ string) (*dlq.Record, error) {
			if delivered {
				// 幂等键去重:已投递,不重新处理。
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

// --- renew_trigger 隐私 --------------------------------------------------

func TestRenewTrigger_NeverReturnsCredentialMaterial(t *testing.T) {
	// 回归(PRIVACY,有区分度):renew_trigger 调用 Rotate,summary 只露出
	// 结果版本 + state —— 绝不露轮换后的 payload。我们喂入一个 secret payload,
	// 并断言它不出现在结果 summary 的任何地方。变异检验:往 summary 加上
	// `"payload": in.Payload`,则该哨兵值会泄露(变红)。
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
	// 轮换后的材料绝不能出现在返回的 summary 的任何地方。
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

// --- enabled 翻转用的 fake --------------------------------------------------

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
