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

// 守 mid-string 通配拒绝(retro S3): 'a*b' 等会被 gate 当精确串静默失配, 创建期必须拒。
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

// 守拒绝形态与 gate 语义对齐(R3): 被拒的 mid-string 通配, 若真落库, 会被
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

type capturingAudit struct {
	created, deleted int
	lastAdmin        int64
}

func (c *capturingAudit) RouteCreated(_ context.Context, _ Route, adminID int64) {
	c.created++
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
}
