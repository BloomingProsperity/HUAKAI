package admin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func TestCanActOnTenantMatrix(t *testing.T) {
	platform := mustIdentity(t, admin.IdentityClaims{
		TokenID: 1, Source: admin.AdminSourceToken, Role: admin.RolePlatformAdmin,
	}, nil)
	resellerNodes := []admin.TenantScopeNode{
		{TenantID: 10, Depth: 0, ScopeRootIsChild: true},
		{TenantID: 11, Depth: 1, ScopeRootIsChild: true},
		{TenantID: 12, Depth: 2, ScopeRootIsChild: true},
	}
	reseller := mustIdentity(t, admin.IdentityClaims{
		TokenID: 2, Source: admin.AdminSourceToken, Role: admin.RoleTenantOperator, ScopeTenantID: 10,
	}, resellerNodes)

	tests := []struct {
		name     string
		identity admin.AdminIdentity
		target   int64
		want     error
		breaks   string
	}{
		{"平台根访问任意租户", platform, 999, nil, "破坏点→删平台全域分支时本断言转红"},
		{"分销商访问自己", reseller, 10, nil, "破坏点→删 scope 根放行分支时本断言转红"},
		{"分销商访问直接子级", reseller, 11, nil, "破坏点→不装载后代集合时本断言转红"},
		{"分销商访问孙级", reseller, 12, nil, "破坏点→递归查询退化成一层时真 PG 同名矩阵与本行为断言转红"},
		{"分销商拒绝兄弟树", reseller, 20, admin.ErrAdminForbidden, "破坏点→删非成员拒绝分支时本断言转红"},
		{"分销商拒绝平台根", reseller, 1, admin.ErrAdminForbidden, "破坏点→把父链方向写反时本断言转红"},
		{"分销商拒绝不存在租户", reseller, 999, admin.ErrAdminForbidden, "破坏点→把任意正 ID 当后代时本断言转红"},
		{"合法身份拒绝零目标", reseller, 0, admin.ErrAdminForbidden, "破坏点→删除非正 target 拒绝时本断言转红"},
		{"scope 缺失拒绝", admin.AdminIdentity{Role: admin.RoleTenantOperator}, 10, admin.ErrAdminUnauthorized, "破坏点→scope 空值默认放行时本断言转红"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.identity.CanActOnTenant(tc.target)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("%s：CanActOnTenant(%d)=%v，期望 %v", tc.breaks, tc.target, err, tc.want)
			}
		})
	}

	unknownRole := reseller
	unknownRole.Role = "unknown"
	if err := unknownRole.CanActOnTenant(10); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→未知角色默认放行时本断言转红：err=%v", err)
	}
}

func TestNewAdminIdentityRejectsScopedPlatformAdmin(t *testing.T) {
	nodes := []admin.TenantScopeNode{
		{TenantID: 10, Depth: 0, ScopeRootIsChild: true},
		{TenantID: 11, Depth: 1, ScopeRootIsChild: true},
	}
	_, err := admin.NewAdminIdentity(context.Background(), admin.IdentityClaims{
		TokenID: 3, Source: admin.AdminSourceToken,
		Role: admin.RolePlatformAdmin, ScopeTenantID: 10,
	}, fixedScope(nodes))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→删除 platform_admin 非零 scope 构造守卫时本断言转红：err=%v", err)
	}
}

func TestAdminIdentityRejectsAbnormalTenantTrees(t *testing.T) {
	deep := make([]admin.TenantScopeNode, 0, admin.MaxTenantScopeDepth+2)
	for depth := int32(0); depth <= admin.MaxTenantScopeDepth+1; depth++ {
		deep = append(deep, admin.TenantScopeNode{
			TenantID: int64(depth) + 10, Depth: depth, ScopeRootIsChild: true,
		})
	}
	_, err := admin.NewAdminIdentity(context.Background(), admin.IdentityClaims{
		Role: admin.RoleTenantOperator, ScopeTenantID: 10,
	}, fixedScope(deep))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→删除深度 32 上限时本断言转红：err=%v", err)
	}

	cycle := []admin.TenantScopeNode{
		{TenantID: 10, Depth: 0, ScopeRootIsChild: true},
		{TenantID: 11, Depth: 1, ScopeRootIsChild: true},
		{TenantID: 10, Depth: 2, CycleDetected: true, ScopeRootIsChild: true},
	}
	_, err = admin.NewAdminIdentity(context.Background(), admin.IdentityClaims{
		Role: admin.RoleTenantOperator, ScopeTenantID: 10,
	}, fixedScope(cycle))
	if !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→删除环检测拒绝时本断言转红：err=%v", err)
	}
}

func TestProviderAccountControlPlaneScopePolicy(t *testing.T) {
	platform := mustIdentity(t, admin.IdentityClaims{Role: admin.RolePlatformAdmin}, nil)
	rootOperator := mustIdentity(t, admin.IdentityClaims{
		Role: admin.RoleTenantOperator, ScopeTenantID: 1,
	}, []admin.TenantScopeNode{{TenantID: 1, Depth: 0}})
	reseller := mustIdentity(t, admin.IdentityClaims{
		Role: admin.RoleTenantOperator, ScopeTenantID: 10,
	}, []admin.TenantScopeNode{{TenantID: 10, Depth: 0, ScopeRootIsChild: true}})

	if err := platform.CanAccessProviderAccountControlPlane(); err != nil {
		t.Fatalf("破坏点→误封平台根时本断言转红：%v", err)
	}
	if err := rootOperator.CanAccessProviderAccountControlPlane(); err != nil {
		t.Fatalf("破坏点→改变既有单租户 root operator 语义时本断言转红：%v", err)
	}
	if err := reseller.CanAccessProviderAccountControlPlane(); !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("破坏点→删除分销商敏感面守卫时本断言转红：%v", err)
	}
	if err := (admin.AdminIdentity{Role: admin.RolePlatformAdmin}).CanAccessProviderAccountControlPlane(); !errors.Is(err, admin.ErrAdminUnauthorized) {
		t.Fatalf("破坏点→scope 缺失默认放行敏感面时本断言转红：%v", err)
	}
}

func mustIdentity(t *testing.T, claims admin.IdentityClaims, nodes []admin.TenantScopeNode) admin.AdminIdentity {
	t.Helper()
	var loader admin.TenantScopeLoader
	if nodes != nil {
		loader = fixedScope(nodes)
	}
	identity, err := admin.NewAdminIdentity(context.Background(), claims, loader)
	if err != nil {
		t.Fatalf("构造测试身份失败：%v", err)
	}
	return identity
}

func fixedScope(nodes []admin.TenantScopeNode) admin.TenantScopeLoader {
	return func(context.Context, int64) ([]admin.TenantScopeNode, error) {
		return append([]admin.TenantScopeNode(nil), nodes...), nil
	}
}
