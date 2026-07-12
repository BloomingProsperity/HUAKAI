package hermesops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// mutatingSpec 构造一个最小的 PROPOSABLE MUTATING spec(Mutating=true、Proposable=true,
// 类似 account_pause/resume),好让目录过滤器有一个真实的 mutating 工具。mutating 工具设置
// Resolve/Mutate(而非 Run);这里它们是非 nil 的桩,使 spec 形态良好。
func mutatingSpec(name string) ToolSpec {
	return ToolSpec{
		Name: name, Category: CategoryMutating, Mutating: true, RequiresConfirmation: true,
		Proposable:   true,
		RequiredRole: RolePlatformAdmin,
		Resolve: func(_ context.Context, _ ToolRequest) (MutationPlan, error) {
			return MutationPlan{}, nil
		},
		Mutate: func(_ context.Context, _ ToolRequest, _ MutationPlan) (ToolResult, error) {
			return ToolResult{}, nil
		},
	}
}

// nonProposableMutatingSpec 构造一个 NOT(不)可被 LLM 提议的 MUTATING spec(Proposable=false,
// 类似 renew_trigger / 凭证轮换):运营者可经 H1 confirm 路径驱动它,但 LLM 必须永远看不到、也不能
// 提议它。
func nonProposableMutatingSpec(name string) ToolSpec {
	s := mutatingSpec(name)
	s.Proposable = false
	return s
}

func TestReadOnlyCatalogExcludesMutatingTools(t *testing.T) {
	// 回归(SAFETY、有区分度):面向 LLM 的目录必须 ONLY(只)暴露只读工具。变异:若 ReadOnlyCatalog
	// 中的 `s.Mutating` 过滤器被去掉,account_pause 就会出现在目录里,模型可能被告知它存在。我们同时
	// 注册一个只读工具 AND(和)一个 mutating 工具,并断言 mutating 的缺席 AND(且)只读的在场——
	// 无论过滤器被移除(mutating 泄漏进来)还是被反转(只读被丢掉),测试都会失败。
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator)) // 只读
	reg.Register(mutatingSpec("account_pause"))              // mutating

	catalog := reg.ReadOnlyCatalog()

	if len(catalog) != 1 {
		t.Fatalf("catalog size=%d want 1 (only the read-only tool)", len(catalog))
	}
	if catalog[0].Name != "audit_lookup" {
		t.Fatalf("catalog[0]=%q want audit_lookup", catalog[0].Name)
	}
	for _, c := range catalog {
		if c.Name == "account_pause" {
			t.Fatalf("MUTATING tool account_pause leaked into the LLM catalog")
		}
	}
}

func TestReadOnlyCatalogIsNameSortedAndSchemaCopied(t *testing.T) {
	// 回归:为了让 LLM 上下文确定,目录顺序必须稳定(按 name 排序),且返回的 InputSchema 必须是
	// 一个拷贝,这样调用方就无法通过目录条目改动 registry 的 schema。
	reg := NewRegistry()
	z := okSpec("zeta", RoleTenantOperator)
	z.InputSchema = map[string]string{"k": "v"}
	reg.Register(z)
	reg.Register(okSpec("alpha", RoleTenantOperator))

	catalog := reg.ReadOnlyCatalog()
	if len(catalog) != 2 || catalog[0].Name != "alpha" || catalog[1].Name != "zeta" {
		t.Fatalf("catalog order=%v want [alpha zeta]", []string{catalog[0].Name, catalog[1].Name})
	}
	// 改动返回的 schema 不得影响 registry 存储的 schema。
	catalog[1].InputSchema["k"] = "tampered"
	if got, _ := reg.Get("zeta"); got.InputSchema["k"] != "v" {
		t.Fatalf("registry schema mutated through catalog copy: %q", got.InputSchema["k"])
	}
}

func TestProposableCatalogIncludesMutatingWithFlags(t *testing.T) {
	// ProposableCatalog(Phase B)DOES(确实)包含 mutating 工具——但每个都被标记
	// Mutating + RequiresConfirmation,这样 runner/LLM 才会渲染一个确认步骤。只读工具被纳入但
	// NO(无)标志。有区分度:若 mutating 分支上的置标志被去掉,mutating 条目看起来就会像一个可
	// 直接运行的只读工具——本测试会变红。
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator)) // 只读
	reg.Register(mutatingSpec("account_pause"))              // mutating

	cat := reg.ProposableCatalog()
	if len(cat) != 2 {
		t.Fatalf("proposable catalog size=%d want 2 (read-only + mutating)", len(cat))
	}
	byName := map[string]CatalogTool{}
	for _, c := range cat {
		byName[c.Name] = c
	}
	ro, ok := byName["audit_lookup"]
	if !ok {
		t.Fatal("read-only audit_lookup missing from proposable catalog")
	}
	if ro.Mutating || ro.RequiresConfirmation {
		t.Fatalf("read-only tool must NOT be flagged mutating/requires_confirmation: %+v", ro)
	}
	mut, ok := byName["account_pause"]
	if !ok {
		t.Fatal("mutating account_pause missing from proposable catalog (it must be PROPOSABLE)")
	}
	if !mut.Mutating || !mut.RequiresConfirmation {
		t.Fatalf("mutating tool must be flagged Mutating+RequiresConfirmation: %+v", mut)
	}
}

func TestReadOnlyCatalogJSONUnchangedByNewFields(t *testing.T) {
	// SAFETY/兼容:加入 Mutating/RequiresConfirmation(omitempty)必须让 ReadOnlyCatalog 的线上
	// 输出逐字节一致——一个只读条目 MUST NOT(绝不能)序列化这些新键。有区分度:去掉 omitempty 标签,
	// 本测试就会变红。
	reg := NewRegistry()
	reg.Register(okSpec("audit_lookup", RoleTenantOperator))
	blob, err := json.Marshal(reg.ReadOnlyCatalog())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, k := range []string{"mutating", "requires_confirmation"} {
		if strings.Contains(string(blob), k) {
			t.Fatalf("ReadOnlyCatalog JSON leaked new key %q (omitempty broken): %s", k, blob)
		}
	}

	// 而 ProposableCatalog 的 mutating 条目 DOES(确实)会序列化这些标志。
	reg.Register(mutatingSpec("account_pause"))
	pblob, err := json.Marshal(reg.ProposableCatalog())
	if err != nil {
		t.Fatalf("marshal proposable: %v", err)
	}
	for _, k := range []string{"mutating", "requires_confirmation"} {
		if !strings.Contains(string(pblob), k) {
			t.Fatalf("ProposableCatalog JSON missing flag key %q: %s", k, pblob)
		}
	}
}
