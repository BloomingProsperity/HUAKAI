// HUAKAI · iKun

package routeadmin

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionenforce"
)

func ptrInt(v int) *int { return &v }

func baseInput() CreateInput {
	return CreateInput{TenantID: 5, Name: "r1", UserGroupMatch: "premium", ModelPatternMatch: "claude-*", PoolGroupID: 9, AdminID: 77}
}

// 守中段通配拒绝(retro S3): 'a*b' 等会被 gate 当精确串静默失配, 创建期必须拒。
// mutation: service.Create 删掉 ValidateModelPattern 调用 → 'a*b' 被接受落库 → 本测红。
func TestCreate_RejectsMidStringWildcard(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	for _, bad := range []string{"a*b", "*x", "a*b*c", "**", "claude-*-preview"} {
		in := baseInput()
		in.ModelPatternMatch = bad
		_, err := svc.Create(context.Background(), in)
		if !errors.Is(err, ErrInvalidModelPattern) {
			t.Fatalf("pattern %q: err=%v, want ErrInvalidModelPattern", bad, err)
		}
	}
}

// 守合法形态全通过: ”/'*'/'prefix*'/精确 都应被接受。
func TestCreate_AcceptsValidPatterns(t *testing.T) {
	for i, ok := range []string{"", "*", "claude-*", "gpt-4o"} {
		store := NewMemoryStore()
		svc := NewService(store, nil)
		in := baseInput()
		in.ModelPatternMatch = ok
		in.Name = "r" + string(rune('a'+i))
		if _, err := svc.Create(context.Background(), in); err != nil {
			t.Fatalf("pattern %q: unexpected err %v, want accept", ok, err)
		}
	}
}

// 守拒绝形态与 gate 语义对齐: 被拒的中段通配, 若真落库, 会被
// subscriptionenforce.ModelPatternMatches 当精确串 → 对"看似该命中"的 model 失配。
// 本测自证: 'a*b' 对 'axb' 不命中(证明拒绝是对的, 不是过度严格)。
func TestRejectedPatternsWouldSilentlyMisbehaveInGate(t *testing.T) {
	// 'a*b' 直觉上像通配 "a 开头 b 结尾", 但 gate 当精确串:
	if subscriptionenforce.ModelPatternMatches("a*b", "axb") {
		t.Fatal("expected gate to treat 'a*b' as exact (not wildcard) — mid-string wildcard is a trap, so creation must reject it")
	}
	// 而我们接受的 'a*' 是真前缀通配, 命中 'axb':
	if !subscriptionenforce.ModelPatternMatches("a*", "axb") {
		t.Fatal("expected accepted trailing-wildcard 'a*' to match 'axb'")
	}
}

// 守必填: tenant/name/user_group/pool_group 任一缺失 → ErrInvalidInput, 绝不落库。
func TestCreate_RejectsMissingRequired(t *testing.T) {
	cases := map[string]func(*CreateInput){
		"zero tenant":      func(c *CreateInput) { c.TenantID = 0 },
		"empty name":       func(c *CreateInput) { c.Name = "  " },
		"empty user_group": func(c *CreateInput) { c.UserGroupMatch = "" },
		"zero pool_group":  func(c *CreateInput) { c.PoolGroupID = 0 },
		"negative prio":    func(c *CreateInput) { c.MatchPriority = ptrInt(-1) },
	}
	for name, mut := range cases {
		store := NewMemoryStore()
		svc := NewService(store, nil)
		in := baseInput()
		mut(&in)
		if _, err := svc.Create(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: err=%v, want ErrInvalidInput", name, err)
		}
		if rs, _ := store.List(context.Background(), 5); len(rs) != 0 {
			t.Fatalf("%s: invalid input must not persist, got %d rows", name, len(rs))
		}
	}
}

// 守同租户重名拒绝。
func TestCreate_DuplicateName(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	if _, err := svc.Create(context.Background(), baseInput()); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(context.Background(), baseInput()); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("dup name: err=%v, want ErrDuplicateName", err)
	}
}

// 守 List 租户隔离: tenant 6 的 route 绝不出现在 tenant 5 的列表。
// mutation: store.List 漏掉 tenant 谓词 → 串租户列出 → 红。
func TestList_TenantScoped(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil)
	a := baseInput()
	a.TenantID = 5
	b := baseInput()
	b.TenantID = 6
	if _, err := svc.Create(context.Background(), a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := svc.Create(context.Background(), b); err != nil {
		t.Fatalf("create b: %v", err)
	}
	got, err := svc.List(context.Background(), 5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].TenantID != 5 {
		t.Fatalf("tenant 5 list = %+v, want exactly its own 1 route", got)
	}
}

// 守软删: 删后不在 List, Get 返 ErrRouteNotFound, 再删幂等返 not found。
func TestSoftDelete_RemovesFromListAndGet(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Delete(context.Background(), 5, r.ID, 77); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := svc.List(context.Background(), 5); len(got) != 0 {
		t.Fatalf("after delete list = %d, want 0", len(got))
	}
	if _, err := svc.Get(context.Background(), 5, r.ID); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("get after delete: err=%v, want ErrRouteNotFound", err)
	}
	if _, err := svc.Delete(context.Background(), 5, r.ID, 77); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("re-delete: err=%v, want ErrRouteNotFound (idempotent)", err)
	}
}

// 守 pool_group FK + 租户归属(S1 retro): 不存在 → ErrPoolGroupNotFound;
// 跨租户(pool 属另一租户)也必须 ErrPoolGroupNotFound, 绝不许建越租户路由。
// mutation: store.Create 退回"只查 id 存在、不校验 owner==tenant" → 跨租户 case 通过 → 本测红。
func TestCreate_PoolGroupOwnership(t *testing.T) {
	store := NewMemoryStore().WithPoolGroup(9, 5) // pool 9 属租户 5
	svc := NewService(store, nil)

	// 同租户合法(tenant 5, pool 9)。
	if _, err := svc.Create(context.Background(), baseInput()); err != nil {
		t.Fatalf("same-tenant pool: unexpected err %v", err)
	}
	// 不存在的 pool → 拒。
	unknown := baseInput()
	unknown.Name, unknown.PoolGroupID = "r-unknown", 999
	if _, err := svc.Create(context.Background(), unknown); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("unknown pool_group: err=%v, want ErrPoolGroupNotFound", err)
	}
	// 跨租户: 租户 6 引用属租户 5 的 pool 9 → 拒(无越租户路由)。
	cross := baseInput()
	cross.TenantID, cross.Name, cross.PoolGroupID = 6, "r-cross", 9
	if _, err := svc.Create(context.Background(), cross); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("cross-tenant pool_group: err=%v, want ErrPoolGroupNotFound (no cross-tenant route)", err)
	}
}

// 守全替换写入: 每个可编辑字段都真落库, 不可变字段(ID/TenantID/Enabled/CreatedAt)保留, 且改动经 Get 读回。
// mutation: Update 漏写某字段(如 pool_group_id) → 该断言红; 误改 ID/CreatedAt → 不可变断言红。
func TestUpdate_ChangesEditableFields(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil)
	r, err := svc.Create(context.Background(), baseInput()) // tenant5 name r1 ug premium pattern claude-* pool9 prio100
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := svc.Update(context.Background(), UpdateInput{
		TenantID: 5, ID: r.ID, Name: "r1-edited", UserGroupMatch: "vip",
		ModelPatternMatch: "gpt-*", PoolGroupID: 11, MatchPriority: ptrInt(5), AdminID: 77,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "r1-edited" || updated.UserGroupMatch != "vip" ||
		updated.ModelPatternMatch != "gpt-*" || updated.PoolGroupID != 11 || updated.MatchPriority != 5 {
		t.Fatalf("editable fields not all applied: %+v", updated)
	}
	if updated.ID != r.ID || updated.TenantID != 5 || !updated.Enabled || !updated.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("immutable fields not preserved: %+v (orig %+v)", updated, r)
	}
	got, _ := svc.Get(context.Background(), 5, r.ID)
	if got.Name != "r1-edited" || got.PoolGroupID != 11 || got.MatchPriority != 5 {
		t.Fatalf("Get after update returned stale row: %+v", got)
	}
}

// 守全替换 PUT 语义: 省略 match_priority(nil) → 回落 DB 默认 100, 非保留原值。
// mutation: Update 把 nil 当"保留原 prio 7" → 仍是 7 → 红。
func TestUpdate_MatchPriorityNilResetsToDefault(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	in := baseInput()
	in.MatchPriority = ptrInt(7)
	r, err := svc.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := svc.Update(context.Background(), UpdateInput{
		TenantID: 5, ID: r.ID, Name: "r1", UserGroupMatch: "premium",
		ModelPatternMatch: "claude-*", PoolGroupID: 9, MatchPriority: nil, AdminID: 77,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.MatchPriority != 100 {
		t.Fatalf("nil match_priority should reset to default 100 (full-replace PUT), got %d", updated.MatchPriority)
	}
}

// 守中段通配在 Update 也被拒(与 Create 同), 且被拒的非法值绝不落库。
// mutation: Service.Update 删 ValidateModelPattern 调用 → 'a*b' 被接受 → Get 返 'a*b' → 红。
func TestUpdate_RejectsMidStringWildcard(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(context.Background(), UpdateInput{
		TenantID: 5, ID: r.ID, Name: "r1", UserGroupMatch: "premium",
		ModelPatternMatch: "a*b", PoolGroupID: 9, AdminID: 77,
	}); !errors.Is(err, ErrInvalidModelPattern) {
		t.Fatalf("mid-string wildcard update: err=%v, want ErrInvalidModelPattern", err)
	}
	got, _ := svc.Get(context.Background(), 5, r.ID)
	if got.ModelPatternMatch != "claude-*" {
		t.Fatalf("rejected update must not mutate row, pattern=%q", got.ModelPatternMatch)
	}
}

// 守必填: tenant/id/name/user_group/pool_group 任一缺失或 prio 负 → ErrInvalidInput, 绝不改库。
func TestUpdate_RejectsMissingRequired(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	valid := UpdateInput{TenantID: 5, ID: r.ID, Name: "r1", UserGroupMatch: "premium", ModelPatternMatch: "claude-*", PoolGroupID: 9, AdminID: 77}
	cases := map[string]func(*UpdateInput){
		"zero tenant":      func(c *UpdateInput) { c.TenantID = 0 },
		"zero id":          func(c *UpdateInput) { c.ID = 0 },
		"empty name":       func(c *UpdateInput) { c.Name = "  " },
		"empty user_group": func(c *UpdateInput) { c.UserGroupMatch = "" },
		"zero pool_group":  func(c *UpdateInput) { c.PoolGroupID = 0 },
		"negative prio":    func(c *UpdateInput) { c.MatchPriority = ptrInt(-1) },
	}
	for name, mut := range cases {
		in := valid
		mut(&in)
		if _, err := svc.Update(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: err=%v, want ErrInvalidInput", name, err)
		}
	}
	got, _ := svc.Get(context.Background(), 5, r.ID)
	if got.Name != "r1" || got.MatchPriority != 100 {
		t.Fatalf("invalid updates must not persist; row mutated: %+v", got)
	}
}

// 守不存在/跨租户 update → ErrRouteNotFound, 且跨租户尝试绝不改到别租户的行(无越租户编辑)。
// mutation: store.Update 漏 tenant 谓词 → 跨租户 case 改成功 → 第三段断言红。
func TestUpdate_NotFoundAndTenantScoped(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput()) // tenant 5
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	miss := UpdateInput{TenantID: 5, ID: r.ID + 999, Name: "x", UserGroupMatch: "premium", ModelPatternMatch: "*", PoolGroupID: 9, AdminID: 77}
	if _, err := svc.Update(context.Background(), miss); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("update missing id: err=%v, want ErrRouteNotFound", err)
	}
	cross := UpdateInput{TenantID: 6, ID: r.ID, Name: "x", UserGroupMatch: "premium", ModelPatternMatch: "*", PoolGroupID: 9, AdminID: 77}
	if _, err := svc.Update(context.Background(), cross); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("cross-tenant update: err=%v, want ErrRouteNotFound (no cross-tenant edit)", err)
	}
	got, _ := svc.Get(context.Background(), 5, r.ID)
	if got.Name != "r1" {
		t.Fatalf("cross-tenant update must not mutate owner's row, name=%q", got.Name)
	}
}

// 守改名冲突排除自身: 改成同租户另一活路由名 → 冲突; 保持自身名改其它字段 → 不得自撞。
// mutation: store.Update 唯一性判没排除自身(id==in.ID) → 自身保名更新被误判 ErrDuplicateName → 第二段红。
func TestUpdate_DuplicateNameExcludesSelf(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	a := baseInput()
	a.Name = "alpha"
	ra, err := svc.Create(context.Background(), a)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b := baseInput()
	b.Name = "beta"
	rb, err := svc.Create(context.Background(), b)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := svc.Update(context.Background(), UpdateInput{
		TenantID: 5, ID: rb.ID, Name: "alpha", UserGroupMatch: "premium",
		ModelPatternMatch: "claude-*", PoolGroupID: 9, AdminID: 77,
	}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("rename beta→alpha: err=%v, want ErrDuplicateName", err)
	}
	if _, err := svc.Update(context.Background(), UpdateInput{
		TenantID: 5, ID: ra.ID, Name: "alpha", UserGroupMatch: "vip",
		ModelPatternMatch: "claude-*", PoolGroupID: 9, AdminID: 77,
	}); err != nil {
		t.Fatalf("self-name update must not self-conflict (exclude self), got %v", err)
	}
}

// 守 update 目标 pool_group 归属: 改到本租户合法 pool → 成功; 不存在或属别租户 → ErrPoolGroupNotFound。
// mutation: store.Update 退回"只查 id 存在、不校验 owner==tenant" → 跨租户 case 通过 → 红。
func TestUpdate_PoolGroupOwnership(t *testing.T) {
	store := NewMemoryStore().WithPoolGroup(9, 5).WithPoolGroup(12, 5).WithPoolGroup(20, 6)
	svc := NewService(store, nil)
	r, err := svc.Create(context.Background(), baseInput()) // tenant5 pool9
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	valid := UpdateInput{TenantID: 5, ID: r.ID, Name: "r1", UserGroupMatch: "premium", ModelPatternMatch: "claude-*", AdminID: 77}
	ok := valid
	ok.PoolGroupID = 12
	if _, err := svc.Update(context.Background(), ok); err != nil {
		t.Fatalf("same-tenant pool update: unexpected err %v", err)
	}
	unknown := valid
	unknown.PoolGroupID = 999
	if _, err := svc.Update(context.Background(), unknown); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("unknown pool update: err=%v, want ErrPoolGroupNotFound", err)
	}
	crossPool := valid
	crossPool.PoolGroupID = 20
	if _, err := svc.Update(context.Background(), crossPool); !errors.Is(err, ErrPoolGroupNotFound) {
		t.Fatalf("cross-tenant pool update: err=%v, want ErrPoolGroupNotFound", err)
	}
}

// 守审计: RouteUpdated 记一次且 adminID 取自调用方(91); 不误碰 create/delete 计数。
// mutation: Service.Update 漏调 audit.RouteUpdated → updated=0 → 红。
func TestUpdate_Audit(t *testing.T) {
	audit := &capturingAudit{}
	svc := NewService(NewMemoryStore(), audit)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Update(context.Background(), UpdateInput{
		TenantID: 5, ID: r.ID, Name: "r1", UserGroupMatch: "premium",
		ModelPatternMatch: "claude-*", PoolGroupID: 9, MatchPriority: ptrInt(3), AdminID: 91,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if audit.updated != 1 || audit.lastAdmin != 91 {
		t.Fatalf("after update: updated=%d admin=%d, want 1/91", audit.updated, audit.lastAdmin)
	}
	if audit.created != 1 || audit.deleted != 0 {
		t.Fatalf("update must not bump create/delete counters: created=%d deleted=%d", audit.created, audit.deleted)
	}
}

type capturingAudit struct {
	created, updated, deleted int
	lastAdmin                 int64
}

func (c *capturingAudit) RouteCreated(_ context.Context, _ Route, adminID int64) {
	c.created++
	c.lastAdmin = adminID
}
func (c *capturingAudit) RouteUpdated(_ context.Context, _ Route, adminID int64) {
	c.updated++
	c.lastAdmin = adminID
}
func (c *capturingAudit) RouteDeleted(_ context.Context, _ Route, adminID int64) {
	c.deleted++
	c.lastAdmin = adminID
}

// 守审计归属: 创建/删除各记一次, adminID 来自调用方(非 client 伪造)。
func TestAudit_RecordsCreateAndDeleteWithAdminID(t *testing.T) {
	audit := &capturingAudit{}
	svc := NewService(NewMemoryStore(), audit)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if audit.created != 1 || audit.lastAdmin != 77 {
		t.Fatalf("after create: created=%d admin=%d, want 1/77", audit.created, audit.lastAdmin)
	}
	if _, err := svc.Delete(context.Background(), 5, r.ID, 88); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if audit.deleted != 1 || audit.lastAdmin != 88 {
		t.Fatalf("after delete: deleted=%d admin=%d, want 1/88", audit.deleted, audit.lastAdmin)
	}
}

// 守 nil store 防御。
func TestNilStore(t *testing.T) {
	svc := NewService(nil, nil)
	if _, err := svc.Create(context.Background(), baseInput()); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil store create: err=%v, want ErrStoreNotConfigured", err)
	}
	if _, err := svc.Update(context.Background(), UpdateInput{TenantID: 5, ID: 1, Name: "r1", UserGroupMatch: "premium", PoolGroupID: 9}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil store update: err=%v, want ErrStoreNotConfigured", err)
	}
	if _, err := svc.SetEnabled(context.Background(), 5, 1, false, 77); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil store set-enabled: err=%v, want ErrStoreNotConfigured", err)
	}
}

// 守启停语义 ≠ 软删: 停用后该 route 仍在 List/Get(管理端可见以便再启用), 只是 Enabled=false; 再启用恢复;
// 且翻转只动 enabled, 其它列(Name/MatchPriority/PoolGroupID/CreatedAt)与 ID/TenantID 全保留。
// mutation: store.SetEnabled 误写别的列 / 不真翻 enabled → 对应断言红; 若把 SetEnabled 实现成软删 → List 变空 → 红。
func TestSetEnabled_DisableKeepsRouteListedThenReEnable(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	in := baseInput()
	in.MatchPriority = ptrInt(7)
	r, err := svc.Create(context.Background(), in) // 新建默认 Enabled=true
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !r.Enabled {
		t.Fatalf("freshly created route should be enabled, got %+v", r)
	}
	// 停用。
	disabled, err := svc.SetEnabled(context.Background(), 5, r.ID, false, 77)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("after SetEnabled(false) route must report Enabled=false")
	}
	// 其它列与不可变字段保留(只翻了 enabled): 闭合到全部非 enabled 列, 含 user_group_match。
	if disabled.Name != "r1" || disabled.UserGroupMatch != "premium" || disabled.ModelPatternMatch != "claude-*" ||
		disabled.MatchPriority != 7 || disabled.PoolGroupID != 9 ||
		disabled.ID != r.ID || disabled.TenantID != 5 || !disabled.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("SetEnabled must only flip enabled, other fields changed: %+v (orig %+v)", disabled, r)
	}
	// 关键区别于软删: 停用的 route 仍出现在 List 且 Get 可读(管理端要能看到以再启用)。
	got, _ := svc.List(context.Background(), 5)
	if len(got) != 1 || got[0].Enabled {
		t.Fatalf("disabled route must still be listed (not removed like soft-delete) and read enabled=false, got %+v", got)
	}
	one, err := svc.Get(context.Background(), 5, r.ID)
	if err != nil || one.Enabled {
		t.Fatalf("disabled route must still be gettable with enabled=false: route=%+v err=%v", one, err)
	}
	// 再启用恢复。
	reEnabled, err := svc.SetEnabled(context.Background(), 5, r.ID, true, 77)
	if err != nil || !reEnabled.Enabled {
		t.Fatalf("re-enable must restore Enabled=true: route=%+v err=%v", reEnabled, err)
	}
}

// 守幂等：把 enabled 设成当前值不报错，照常返回快照。
// mutation: 若实现对 same-value 报错/拒绝 → 任一段红。
func TestSetEnabled_Idempotent(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput()) // enabled=true
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, err := svc.SetEnabled(context.Background(), 5, r.ID, true, 77); err != nil || !got.Enabled {
		t.Fatalf("enable already-enabled must be no-op success: route=%+v err=%v", got, err)
	}
	if _, err := svc.SetEnabled(context.Background(), 5, r.ID, false, 77); err != nil {
		t.Fatalf("first disable: %v", err)
	}
	if got, err := svc.SetEnabled(context.Background(), 5, r.ID, false, 77); err != nil || got.Enabled {
		t.Fatalf("disable already-disabled must be no-op success (enabled=false): route=%+v err=%v", got, err)
	}
}

// 守不存在/已软删/跨租户 → ErrRouteNotFound, 且跨租户尝试绝不翻到别租户的行(无越租户启停)。
// mutation: store.SetEnabled 漏 tenant 谓词 → 跨租户翻转成功 → 末段红; 漏 deleted_at IS NULL → 软删行被启停 → soft-deleted 段红。
func TestSetEnabled_NotFoundTenantScopedAndSoftDeleted(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput()) // tenant 5
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.SetEnabled(context.Background(), 5, r.ID+999, false, 77); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("set-enabled missing id: err=%v, want ErrRouteNotFound", err)
	}
	// 跨租户: 租户 6 试图停用属租户 5 的行 → not found, 且不得真停用。
	if _, err := svc.SetEnabled(context.Background(), 6, r.ID, false, 77); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("cross-tenant set-enabled: err=%v, want ErrRouteNotFound (no cross-tenant flip)", err)
	}
	if got, _ := svc.Get(context.Background(), 5, r.ID); !got.Enabled {
		t.Fatalf("cross-tenant attempt must not flip owner's row, enabled=%v want true", got.Enabled)
	}
	// 已软删的行不可启停。
	if _, err := svc.Delete(context.Background(), 5, r.ID, 77); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}
	if _, err := svc.SetEnabled(context.Background(), 5, r.ID, true, 77); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("set-enabled on soft-deleted route: err=%v, want ErrRouteNotFound", err)
	}
}

// 守必填: tenant<=0 或 id<=0 → ErrInvalidInput, 不触达 store。
func TestSetEnabled_RejectsMissingRequired(t *testing.T) {
	svc := NewService(NewMemoryStore(), nil)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.SetEnabled(context.Background(), 0, r.ID, false, 77); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero tenant: err=%v, want ErrInvalidInput", err)
	}
	if _, err := svc.SetEnabled(context.Background(), 5, 0, false, 77); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero id: err=%v, want ErrInvalidInput", err)
	}
}

// 守审计: SetEnabled 记一次 RouteUpdated 且 adminID 取自调用方(91); 不误碰 create/delete 计数。
// mutation: Service.SetEnabled 漏调 audit.RouteUpdated → updated=0 → 红。
func TestSetEnabled_Audit(t *testing.T) {
	audit := &capturingAudit{}
	svc := NewService(NewMemoryStore(), audit)
	r, err := svc.Create(context.Background(), baseInput())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.SetEnabled(context.Background(), 5, r.ID, false, 91); err != nil {
		t.Fatalf("set-enabled: %v", err)
	}
	if audit.updated != 1 || audit.lastAdmin != 91 {
		t.Fatalf("after set-enabled: updated=%d admin=%d, want 1/91", audit.updated, audit.lastAdmin)
	}
	if audit.created != 1 || audit.deleted != 0 {
		t.Fatalf("set-enabled must not bump create/delete counters: created=%d deleted=%d", audit.created, audit.deleted)
	}
}
