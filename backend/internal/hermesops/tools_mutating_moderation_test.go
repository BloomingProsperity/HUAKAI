package hermesops

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BloomingProsperity/HUAKAI/internal/moderation"
)

// --- moderation_keyword_enable/disable 的 RBAC 底线(L1)----------------------

func TestModerationKeywordToggle_PlatformAdminOnly(t *testing.T) {
	// 审核规则由部署者代租户维护；租户管理员只查看违规并解封自身租户用户。
	reg := NewRegistry()
	deps := ModerationKeywordMutationDeps{
		GetKeyword: func(_ context.Context, tenantID, id int64) (moderation.KeywordRule, error) {
			return moderation.KeywordRule{ID: id, TenantID: tenantID}, nil
		},
		SetEnabledInTx: func(_ context.Context, _ pgx.Tx, _, _ int64, _ bool) error { return nil },
	}
	reg.Register(ModerationKeywordEnableSpec(deps))
	reg.Register(ModerationKeywordDisableSpec(deps))
	if _, err := reg.AuthorizeMutating(ToolModerationKeywordEnable, RoleTenantOperator); !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("tenant_operator moderation_keyword_enable err=%v want ErrToolForbidden", err)
	}
	if _, err := reg.AuthorizeMutating(ToolModerationKeywordEnable, RolePlatformAdmin); err != nil {
		t.Fatalf("platform_admin moderation_keyword_enable err=%v want nil", err)
	}
	if _, err := reg.AuthorizeMutating(ToolModerationKeywordDisable, "unknown_role"); !errors.Is(err, ErrToolForbidden) {
		t.Fatalf("unknown role moderation_keyword_disable err=%v want ErrToolForbidden", err)
	}
}

// --- 可提议语义(Proposable + RequiresConfirmation)-----------------------------

func TestModerationKeywordToggle_ProposableButRequiresConfirmation(t *testing.T) {
	// 回归(安全不变量):enable/disable 是可逆但安全敏感的 B 级 → Proposable=true(LLM 可提议),
	// 但 RequiresConfirmation=true(关掉一个内容过滤器仍需 operator 确认才执行)。两者缺一,安全语义就破。
	// 变异检验:把 moderationKeywordToggleSpec 里的 Proposable 改成 false → 第一个断言红;
	// 把 RequiresConfirmation 改成 false → 第二个断言红。
	for _, spec := range []ToolSpec{ModerationKeywordEnableSpec(ModerationKeywordMutationDeps{}), ModerationKeywordDisableSpec(ModerationKeywordMutationDeps{})} {
		if !spec.Proposable {
			t.Fatalf("%s Proposable=false want true (可逆 B 级应可被 LLM 提议)", spec.Name)
		}
		if !spec.RequiresConfirmation {
			t.Fatalf("%s RequiresConfirmation=false want true (关掉内容过滤器仍需 operator 确认)", spec.Name)
		}
		if !spec.Mutating || spec.RequiredRole != RolePlatformAdmin {
			t.Fatalf("%s Mutating=%v role=%s want true/platform_admin", spec.Name, spec.Mutating, spec.RequiredRole)
		}
	}
}

// --- Resolve(预览 + 跨租户复检 + keyword 原文不外露)----------------------------

func TestModerationKeywordToggle_ResolvePreviewAndTenantRecheck(t *testing.T) {
	// 回归(L2 + 跨租户):Resolve 预览 current->next 的 enabled,并拒绝那些租户与请求租户
	// 不一致的关键词。变异检验:去掉 kw.TenantID != req.TenantID 守卫,则对外租户关键词的
	// resolve 会返回一个 plan 而不是 ErrTargetResolution(第一段断言翻红)。
	deps := ModerationKeywordMutationDeps{
		GetKeyword: func(_ context.Context, _, id int64) (moderation.KeywordRule, error) {
			// 模拟 DB 返回一个租户不匹配的行(纵深防御 —— 查询本身按租户过滤,
			// 但我们仍对返回的行复检)。
			return moderation.KeywordRule{ID: id, TenantID: 999, Keyword: "foreign-word", ReasonCode: "keyword_match", Enabled: true}, nil
		},
	}
	spec := ModerationKeywordDisableSpec(deps)
	if _, err := spec.Resolve(context.Background(), ToolRequest{TenantID: 7, Args: map[string]any{"keyword_id": float64(5)}}); !errors.Is(err, ErrTargetResolution) {
		t.Fatalf("foreign-tenant resolve err=%v want ErrTargetResolution", err)
	}

	// 同租户:disable 预览应是 current=true -> next=false,携带 reason_code + keyword_length,
	// 且**绝不**携带 keyword 原文(隐私不变量)。
	deps.GetKeyword = func(_ context.Context, tenantID, id int64) (moderation.KeywordRule, error) {
		return moderation.KeywordRule{ID: id, TenantID: tenantID, Keyword: "secret-term", ReasonCode: "abuse_terms", Enabled: true}, nil
	}
	spec = ModerationKeywordDisableSpec(deps)
	plan, err := spec.Resolve(context.Background(), ToolRequest{TenantID: 7, Args: map[string]any{"keyword_id": float64(5)}})
	if err != nil {
		t.Fatalf("resolve err=%v want nil", err)
	}
	if plan.Preview["current_enabled"] != true || plan.Preview["next_enabled"] != false {
		t.Fatalf("disable preview current=%v next=%v want true->false", plan.Preview["current_enabled"], plan.Preview["next_enabled"])
	}
	if plan.Preview["reason_code"] != "abuse_terms" || plan.Preview["no_op"] != false {
		t.Fatalf("disable preview reason_code=%v no_op=%v want abuse_terms/false", plan.Preview["reason_code"], plan.Preview["no_op"])
	}
	// 隐私不变量(关键):预览以 keyword_length 计数(len("secret-term")=11)表征关键词,
	// 且任何预览值都不得等于 keyword 原文。变异检验:把预览里的 keyword_length 换成 kw.Keyword
	// (回显原文)→ 下面"没有原文"的断言翻红。
	if plan.Preview["keyword_length"] != 11 {
		t.Fatalf("disable preview keyword_length=%v want 11 (len of 'secret-term')", plan.Preview["keyword_length"])
	}
	for k, v := range plan.Preview {
		if v == "secret-term" {
			t.Fatalf("preview key %q leaked keyword plaintext (隐私不变量:拦截词原文绝不外露)", k)
		}
	}
	if plan.TargetType != "moderation_keyword" || plan.TargetID != 5 {
		t.Fatalf("plan target=%s/%d want moderation_keyword/5", plan.TargetType, plan.TargetID)
	}

	// enable 同一条已启用的关键词 → next=true,no_op=true(预览如实标注空操作)。
	planEnable, err := ModerationKeywordEnableSpec(deps).Resolve(context.Background(), ToolRequest{TenantID: 7, Args: map[string]any{"keyword_id": float64(5)}})
	if err != nil {
		t.Fatalf("enable resolve err=%v", err)
	}
	if planEnable.Preview["next_enabled"] != true || planEnable.Preview["no_op"] != true {
		t.Fatalf("enable preview next=%v no_op=%v want true/true", planEnable.Preview["next_enabled"], planEnable.Preview["no_op"])
	}
}

// --- Mutate(按 targetEnabled + 租户调用)----------------------------------------

func TestModerationKeywordToggle_MutateSetsEnabledByTargetAndTenant(t *testing.T) {
	// 回归:disable 的 Mutate 以 enabled=false 调用 SetEnabledInTx,enable 以 enabled=true;
	// 且**用请求租户 + plan.TargetID** 调用(租户 scope 第三处绑死在 SQL 入参)。
	// 变异检验:
	//   - 在 moderationKeywordToggleSpec 把 targetEnabled 写死为 true → disable 的 enabled 断言红;
	//   - 把 Mutate 里 SetEnabledInTx 的 tenantID 实参从 req.TenantID 改成别的(去掉租户绑死)
	//     → gotTenant 断言红。
	rec := &moderationEnabledRecorder{}
	tx := &moderationFakeTx{}
	ctx := withMutationTx(context.Background(), tx)
	deps := ModerationKeywordMutationDeps{
		SetEnabledInTx: func(_ context.Context, gotTx pgx.Tx, tenantID, id int64, enabled bool) error {
			rec.calledTx = gotTx
			rec.gotTenant = tenantID
			rec.gotID = id
			rec.gotEnabled = enabled
			return nil
		},
	}
	plan := MutationPlan{TargetType: "moderation_keyword", TargetID: 5, Preview: map[string]any{"current_enabled": true}}

	res, err := ModerationKeywordDisableSpec(deps).Mutate(ctx, ToolRequest{TenantID: 7, ActorSource: "token", ActorID: 42}, plan)
	if err != nil {
		t.Fatalf("disable mutate err=%v", err)
	}
	if rec.gotEnabled != false {
		t.Fatalf("disable set enabled=%v want false", rec.gotEnabled)
	}
	if rec.gotTenant != 7 || rec.gotID != 5 {
		t.Fatalf("disable called tenant=%d id=%d want 7/5 (租户 scope 必须绑死在 SQL 入参)", rec.gotTenant, rec.gotID)
	}
	if rec.calledTx != tx {
		t.Fatalf("disable did not pass orchestrator tx through to SetEnabledInTx")
	}
	if res.Summary["enabled"] != false || res.Summary["previous_enabled"] != true || res.Summary["keyword_id"] != int64(5) {
		t.Fatalf("disable summary=%+v want enabled=false previous=true keyword_id=5", res.Summary)
	}

	planEnable := MutationPlan{TargetType: "moderation_keyword", TargetID: 9, Preview: map[string]any{"current_enabled": false}}
	if _, err := ModerationKeywordEnableSpec(deps).Mutate(ctx, ToolRequest{TenantID: 7, ActorSource: "token", ActorID: 42}, planEnable); err != nil {
		t.Fatalf("enable mutate err=%v", err)
	}
	if rec.gotEnabled != true {
		t.Fatalf("enable set enabled=%v want true", rec.gotEnabled)
	}
	if rec.gotID != 9 {
		t.Fatalf("enable called id=%d want 9", rec.gotID)
	}
}

func TestModerationKeywordToggle_MutateFailClosedWithoutTx(t *testing.T) {
	// 回归:缺 orchestrator tx 时 Mutate 必须 fail-closed(ErrDependencyUnwired),
	// 绝不在事务外擅自写。变异检验:去掉 Mutate 里 tx==nil 的守卫 → 此处不再返回该 error。
	deps := ModerationKeywordMutationDeps{
		SetEnabledInTx: func(_ context.Context, _ pgx.Tx, _, _ int64, _ bool) error { return nil },
	}
	plan := MutationPlan{TargetType: "moderation_keyword", TargetID: 5, Preview: map[string]any{"current_enabled": true}}
	if _, err := ModerationKeywordDisableSpec(deps).Mutate(context.Background(), ToolRequest{TenantID: 7}, plan); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("mutate without tx err=%v want ErrDependencyUnwired", err)
	}
}

// --- fake ------------------------------------------------------------------

type moderationEnabledRecorder struct {
	calledTx   pgx.Tx
	gotTenant  int64
	gotID      int64
	gotEnabled bool
}

// moderationFakeTx 是一个最小的 pgx.Tx 占位实现:本测试只断言 Mutate 是否把 orchestrator 的
// tx 原样传给 SetEnabledInTx(真正的 SQL 由 SetEnabledInTx 的 fake 拦截),故各方法均为空操作。
type moderationFakeTx struct{}

func (tx *moderationFakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("UPDATE 1"), nil
}
func (tx *moderationFakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return moderationErrRow{err: errors.New("queryrow unused")}
}
func (tx *moderationFakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unused")
}
func (tx *moderationFakeTx) Commit(context.Context) error          { return nil }
func (tx *moderationFakeTx) Rollback(context.Context) error        { return nil }
func (tx *moderationFakeTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("unused") }
func (tx *moderationFakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (tx *moderationFakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *moderationFakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *moderationFakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (tx *moderationFakeTx) Conn() *pgx.Conn { return nil }

type moderationErrRow struct{ err error }

func (r moderationErrRow) Scan(...any) error { return r.err }
