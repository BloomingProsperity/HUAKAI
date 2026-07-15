//go:build integration_pg

// 模型主体运维写面的真 PostgreSQL 判别测试。
// 每个用例都把正确路径与具体破坏路径区分开，避免只证明“没有报错”。
package registry

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func platformModelAccess() AdminModelAccess {
	return AdminModelAccess{Role: modelAdminRolePlatform, Actor: "integration:platform"}
}

func tenantModelAccess(tenantID int64) AdminModelAccess {
	return AdminModelAccess{
		Role: modelAdminRoleTenant, ScopeTenantID: tenantID,
		Actor: fmt.Sprintf("integration:tenant:%d", tenantID),
	}
}

func tenantModelTarget(tenantID int64) AdminModelTarget {
	return AdminModelTarget{Scope: ModelScopeTenant, TenantID: tenantID}
}

func stringPointer(value string) *string { return &value }
func int32Pointer(value int32) *int32    { return &value }

// CRUD 全链同时钉住字段回读、PATCH 子集保持、active↔disabled 与每次写的快照 +1。
// 破坏任一写后的 bump、PATCH 合并或 deleted 过滤，精确版本/字段/列表断言都会转红。
func TestModelsAdmin_CRUDReadBackAndSnapshots(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	f.setSnapshot(10)
	r := NewPostgresRegistry(pool, nil)
	target := tenantModelTarget(f.tenantID)
	access := tenantModelAccess(f.tenantID)

	created, err := r.CreateAdminModel(ctx, CreateAdminModelInput{
		Access: access, Target: target,
		CanonicalID: "manual/crud-" + f.suffix, ProtocolFamily: "openai_chat",
		DefaultProviderModelID: "provider-before", DefaultContextWindow: 8192,
		DefaultRequestTimeoutMS: 45000, PricingClass: "manual-standard",
		ModelOwner: "租户运维", Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateAdminModel: %v", err)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 11 {
		t.Fatalf("创建后 snapshot=%d want 11", version)
	}
	if created.ID <= 0 || created.TenantID == nil || *created.TenantID != f.tenantID ||
		created.Scope != ModelScopeTenant || created.CanonicalID != "manual/crud-"+f.suffix ||
		created.ProtocolFamily != "openai_chat" || created.DefaultProviderModelID != "provider-before" ||
		created.DefaultContextWindow != 8192 || created.DefaultRequestTimeoutMS != 45000 ||
		created.PricingClass != "manual-standard" || created.ModelOwner != "租户运维" || created.Status != "active" {
		t.Fatalf("创建回读字段不一致：%+v", created)
	}

	readBack, err := r.GetAdminModel(ctx, access, target, created.ID)
	if err != nil {
		t.Fatalf("GetAdminModel: %v", err)
	}
	if readBack.CanonicalID != created.CanonicalID || readBack.DefaultRequestTimeoutMS != 45000 {
		t.Fatalf("Get 回读不一致：%+v", readBack)
	}

	updated, err := r.UpdateAdminModel(ctx, UpdateAdminModelInput{
		Access: access, Target: target, ID: created.ID,
		DefaultProviderModelID: stringPointer("provider-after"),
		DefaultContextWindow:   int32Pointer(32768),
	})
	if err != nil {
		t.Fatalf("UpdateAdminModel: %v", err)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 12 {
		t.Fatalf("更新后 snapshot=%d want 12", version)
	}
	if updated.DefaultProviderModelID != "provider-after" || updated.DefaultContextWindow != 32768 {
		t.Fatalf("更新字段未生效：%+v", updated)
	}
	if updated.CanonicalID != created.CanonicalID || updated.ProtocolFamily != "openai_chat" ||
		updated.DefaultRequestTimeoutMS != 45000 || updated.PricingClass != "manual-standard" ||
		updated.ModelOwner != "租户运维" {
		t.Fatalf("PATCH 覆盖了未提交字段：%+v", updated)
	}

	disabled, err := r.UpdateAdminModel(ctx, UpdateAdminModelInput{
		Access: access, Target: target, ID: created.ID, Status: stringPointer("disabled"),
	})
	if err != nil || disabled.Status != "disabled" {
		t.Fatalf("停用结果=%+v err=%v", disabled, err)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 13 {
		t.Fatalf("停用后 snapshot=%d want 13", version)
	}

	enabled, err := r.UpdateAdminModel(ctx, UpdateAdminModelInput{
		Access: access, Target: target, ID: created.ID, Status: stringPointer("active"),
	})
	if err != nil || enabled.Status != "active" {
		t.Fatalf("启用结果=%+v err=%v", enabled, err)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 14 {
		t.Fatalf("启用后 snapshot=%d want 14", version)
	}

	if err := r.SoftDeleteAdminModel(ctx, access, target, created.ID); err != nil {
		t.Fatalf("SoftDeleteAdminModel: %v", err)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 15 {
		t.Fatalf("软删后 snapshot=%d want 15", version)
	}
	items, err := r.ListAdminModels(ctx, access, target)
	if err != nil {
		t.Fatalf("ListAdminModels: %v", err)
	}
	for _, item := range items {
		if item.ID == created.ID {
			t.Fatalf("软删模型仍出现在列表：%+v", item)
		}
	}
	if _, err := r.GetAdminModel(ctx, access, target, created.ID); !errors.Is(err, ErrModelAdminNotFound) {
		t.Fatalf("软删后 Get err=%v want ErrModelAdminNotFound", err)
	}
}

// B 操作者用“自己的 tenant target + A 的 id”探测，授权前置检查会放行 target B，
// 必须由 SQL 的 scope+tenant_id 谓词挡住。去掉该谓词后读/改/删至少一项会命中 A 并转红。
func TestModelsAdmin_CrossTenantIDScopedInQueries(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	r := NewPostgresRegistry(pool, nil)
	aTarget := tenantModelTarget(f.tenantID)
	created, err := r.CreateAdminModel(ctx, CreateAdminModelInput{
		Access: tenantModelAccess(f.tenantID), Target: aTarget,
		CanonicalID: "manual/cross-" + f.suffix, ProtocolFamily: "anthropic_messages",
		DefaultProviderModelID: "cross-provider", DefaultContextWindow: 1000,
		DefaultRequestTimeoutMS: 60000, PricingClass: "standard", ModelOwner: "A", Status: "active",
	})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	bAccess := tenantModelAccess(f.otherTenantID)
	bTarget := tenantModelTarget(f.otherTenantID)
	if _, err := r.GetAdminModel(ctx, bAccess, aTarget, created.ID); !errors.Is(err, ErrModelAdminForbidden) {
		t.Errorf("service 跨租户目标 Get err=%v want ErrModelAdminForbidden", err)
	}
	if _, err := r.GetAdminModel(ctx, bAccess, bTarget, created.ID); !errors.Is(err, ErrModelAdminNotFound) {
		t.Errorf("跨租户 Get err=%v want ErrModelAdminNotFound", err)
	}
	if _, err := r.UpdateAdminModel(ctx, UpdateAdminModelInput{
		Access: bAccess, Target: bTarget, ID: created.ID, ModelOwner: stringPointer("B 越权"),
	}); !errors.Is(err, ErrModelAdminNotFound) {
		t.Errorf("跨租户 Update err=%v want ErrModelAdminNotFound", err)
	}
	if err := r.SoftDeleteAdminModel(ctx, bAccess, bTarget, created.ID); !errors.Is(err, ErrModelAdminNotFound) {
		t.Errorf("跨租户 Delete err=%v want ErrModelAdminNotFound", err)
	}
	positive, err := r.GetAdminModel(ctx, tenantModelAccess(f.tenantID), aTarget, created.ID)
	if err != nil || positive.ModelOwner != "A" || positive.Status != "active" {
		t.Fatalf("正控制读取失败或被越权改写：model=%+v err=%v", positive, err)
	}
}

// tenant operator 只有在继承策略开启后才能读 global，且始终禁止写。
// 放开继承读门或 global 写守卫后，对应断言会转红。
func TestModelsAdmin_TenantOperatorCannotWriteGlobal(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	f.createdGlobalRows = true
	r := NewPostgresRegistry(pool, nil)
	target := AdminModelTarget{Scope: ModelScopeGlobal}
	operator := tenantModelAccess(f.tenantID)
	if _, err := r.CreateAdminModel(ctx, CreateAdminModelInput{
		Access: operator, Target: target, CanonicalID: "manual/global-denied-" + f.suffix,
		ProtocolFamily: "openai_chat", DefaultProviderModelID: "denied",
		DefaultContextWindow: 1, DefaultRequestTimeoutMS: 1,
		PricingClass: "standard", ModelOwner: "operator", Status: "active",
	}); !errors.Is(err, ErrModelAdminForbidden) {
		t.Fatalf("operator Create global err=%v want ErrModelAdminForbidden", err)
	}
	globalID := f.seedModel(modelOpts{scope: ModelScopeGlobal, canonicalID: "manual/global-existing-" + f.suffix})
	if _, err := r.GetAdminModel(ctx, operator, target, globalID); !errors.Is(err, ErrModelAdminForbidden) {
		t.Fatalf("未继承目录的 operator Get global err=%v want ErrModelAdminForbidden", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO model_registry_tenant_policies (tenant_id, inherit_global_catalog)
VALUES ($1, true)
ON CONFLICT (tenant_id) DO UPDATE SET inherit_global_catalog = true`, f.tenantID); err != nil {
		t.Fatalf("启用 global 目录继承：%v", err)
	}
	if _, err := r.GetAdminModel(ctx, operator, target, globalID); err != nil {
		t.Fatalf("operator 应可只读 global：%v", err)
	}
	if _, err := r.UpdateAdminModel(ctx, UpdateAdminModelInput{
		Access: operator, Target: target, ID: globalID, Status: stringPointer("disabled"),
	}); !errors.Is(err, ErrModelAdminForbidden) {
		t.Fatalf("operator Update global err=%v want ErrModelAdminForbidden", err)
	}
	if err := r.SoftDeleteAdminModel(ctx, operator, target, globalID); !errors.Is(err, ErrModelAdminForbidden) {
		t.Fatalf("operator Delete global err=%v want ErrModelAdminForbidden", err)
	}
}

// 同一租户内 canonical_id 重复必须映射为 ErrConflict；不同租户同名作为正控制可创建。
func TestModelsAdmin_CanonicalConflictIsScoped(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	r := NewPostgresRegistry(pool, nil)
	canonicalID := "manual/duplicate-" + f.suffix
	base := CreateAdminModelInput{
		Access: platformModelAccess(), Target: tenantModelTarget(f.tenantID),
		CanonicalID: canonicalID, ProtocolFamily: "openai_chat", DefaultProviderModelID: "one",
		DefaultContextWindow: 4096, DefaultRequestTimeoutMS: 60000,
		PricingClass: "standard", ModelOwner: "owner", Status: "active",
	}
	if _, err := r.CreateAdminModel(ctx, base); err != nil {
		t.Fatalf("首次创建：%v", err)
	}
	if _, err := r.CreateAdminModel(ctx, base); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复创建 err=%v want ErrConflict", err)
	}
	base.Target = tenantModelTarget(f.otherTenantID)
	base.DefaultProviderModelID = "other-tenant"
	other, err := r.CreateAdminModel(ctx, base)
	if err != nil {
		t.Fatalf("不同租户同名应允许：%v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM models WHERE id = $1`, other.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM model_registry_snapshots WHERE tenant_id = $1`, f.otherTenantID)
	})
}

// global 写必须让所有受影响租户各 +1。若误写成“global 无 tenant 所以不 bump”或只 bump 单租户，断言转红。
func TestModelsAdmin_GlobalCreateBumpsEveryInheritingTenant(t *testing.T) {
	ctx := context.Background()
	pool := openIntegrationPool(t, ctx)
	f := newFixture(t, ctx, pool)
	f.createdGlobalRows = true
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM model_registry_snapshots WHERE tenant_id = $1`, f.otherTenantID)
		_, _ = pool.Exec(c, `DELETE FROM model_registry_tenant_policies WHERE tenant_id = $1`, f.otherTenantID)
	})
	if _, err := pool.Exec(ctx, `
INSERT INTO model_registry_tenant_policies (tenant_id, inherit_global_catalog)
VALUES ($1, true), ($2, true)
ON CONFLICT (tenant_id) DO UPDATE SET inherit_global_catalog = true`, f.tenantID, f.otherTenantID); err != nil {
		t.Fatalf("seed policies: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO model_registry_snapshots (tenant_id, version)
VALUES ($1, 20), ($2, 30)
ON CONFLICT (tenant_id) DO UPDATE SET version = EXCLUDED.version`, f.tenantID, f.otherTenantID); err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}
	r := NewPostgresRegistry(pool, nil)
	if _, err := r.CreateAdminModel(ctx, CreateAdminModelInput{
		Access: platformModelAccess(), Target: AdminModelTarget{Scope: ModelScopeGlobal},
		CanonicalID: "manual/global-bump-" + f.suffix, ProtocolFamily: "openai_chat",
		DefaultProviderModelID: "global-provider", DefaultContextWindow: 16000,
		DefaultRequestTimeoutMS: 60000, PricingClass: "standard", ModelOwner: "platform", Status: "active",
	}); err != nil {
		t.Fatalf("Create global: %v", err)
	}
	if version := readSnapVer(t, ctx, pool, f.tenantID); version != 21 {
		t.Fatalf("首租户 snapshot=%d want 21", version)
	}
	if version := readSnapVer(t, ctx, pool, f.otherTenantID); version != 31 {
		t.Fatalf("次租户 snapshot=%d want 31", version)
	}
}
