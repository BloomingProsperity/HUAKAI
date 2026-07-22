package hermesops

import (
	"context"
	"errors"
	"testing"
)

func mutatingSpec(name string) ToolSpec {
	return ToolSpec{
		Name: name, Category: CategoryMutating, Description: "测试改动工具",
		Mutating: true, RequiresConfirmation: true, Proposable: true,
		RequiredRole: RolePlatformAdmin, InputSchema: ObjectSchema(nil),
		Resolve: func(context.Context, ToolRequest) (MutationPlan, error) {
			return MutationPlan{}, nil
		},
		Mutate: func(context.Context, ToolRequest, MutationPlan) (ToolResult, error) {
			return ToolResult{}, nil
		},
	}
}

func nonProposableMutatingSpec(name string) ToolSpec {
	spec := mutatingSpec(name)
	spec.Proposable = false
	return spec
}

func TestCatalogForRole过滤角色和危险工具(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(okSpec("tenant_read", RoleTenantOperator))
	_ = reg.Register(okSpec("platform_read", RolePlatformAdmin))
	_ = reg.Register(mutatingSpec("platform_proposal"))
	_ = reg.Register(nonProposableMutatingSpec("platform_secret_change"))

	tenantCatalog := reg.CatalogForRole(RoleTenantOperator, true)
	if len(tenantCatalog) != 1 || tenantCatalog[0].Name != "tenant_read" {
		t.Fatalf("租户目录=%v，预期只有 tenant_read", catalogNames(tenantCatalog))
	}
	platformCatalog := reg.CatalogForRole(RolePlatformAdmin, true)
	if got := catalogNames(platformCatalog); len(got) != 3 || got[0] != "platform_proposal" || got[1] != "platform_read" || got[2] != "tenant_read" {
		t.Fatalf("平台目录=%v，预期包含两项只读和一项可提议工具", got)
	}
	for _, tool := range platformCatalog {
		if tool.Name == "platform_secret_change" {
			t.Fatal("不可提议的改动工具泄露到了模型目录")
		}
	}
}

func TestCatalogForRole关闭提议后只有只读工具(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register(okSpec("audit_lookup", RoleTenantOperator))
	_ = reg.Register(mutatingSpec("account_pause"))

	catalog := reg.CatalogForRole(RolePlatformAdmin, false)
	if len(catalog) != 1 || catalog[0].Name != "audit_lookup" || !catalog[0].ReadOnly {
		t.Fatalf("关闭提议后的目录=%+v，预期只有只读工具", catalog)
	}
}

func TestCatalog复制JSONSchema(t *testing.T) {
	reg := NewRegistry()
	spec := okSpec("with_schema", RoleTenantOperator)
	spec.InputSchema = ObjectSchema(map[string]any{
		"account_id": PositiveIntegerSchema("账号 ID"),
	}, "account_id")
	if err := reg.Register(spec); err != nil {
		t.Fatalf("注册失败：%v", err)
	}

	catalog := reg.CatalogForRole(RoleTenantOperator, false)
	properties := catalog[0].InputSchema["properties"].(map[string]any)
	properties["account_id"].(map[string]any)["minimum"] = 99
	stored, _ := reg.Get("with_schema")
	storedProperties := stored.InputSchema["properties"].(map[string]any)
	if got := storedProperties["account_id"].(map[string]any)["minimum"]; got != 1 {
		t.Fatalf("目录返回值修改了注册表 schema：minimum=%v", got)
	}
}

func TestRegistry拒绝重复名和坏Schema(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(okSpec("same", RoleTenantOperator)); err != nil {
		t.Fatalf("首次注册失败：%v", err)
	}
	if err := reg.Register(okSpec("same", RoleTenantOperator)); !errors.Is(err, ErrDuplicateTool) {
		t.Fatalf("重复注册错误=%v，预期 ErrDuplicateTool", err)
	}
	if err := reg.Validate(); !errors.Is(err, ErrDuplicateTool) {
		t.Fatalf("Validate 错误=%v，预期保留重复注册错误", err)
	}

	bad := okSpec("bad_schema", RoleTenantOperator)
	bad.InputSchema = map[string]any{"type": "object"}
	if err := NewRegistry().Register(bad); !errors.Is(err, ErrInvalidToolSchema) {
		t.Fatalf("坏 schema 错误=%v，预期 ErrInvalidToolSchema", err)
	}
}

func catalogNames(catalog []CatalogTool) []string {
	out := make([]string, len(catalog))
	for index, tool := range catalog {
		out[index] = tool.Name
	}
	return out
}
