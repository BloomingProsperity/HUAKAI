//go:build integration_pg

package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/modelsync"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
)

func TestModelDiscoveryLifecycleAndAtomicity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openIntegrationPool(t, ctx)
	suffix := uuid.NewString()
	fixture := seedModelDiscoveryFixture(t, ctx, pool, suffix)
	defer cleanupModelDiscoveryFixture(t, pool, fixture)
	store := NewPostgresRegistry(pool, nil)

	primaryID := "gpt-discovery-primary-" + suffix
	ignoredID := "gpt-discovery-ignored-" + suffix
	initial := modelsync.Catalog{Vendor: modelsync.VendorOpenAI, Models: []modelsync.Model{
		discoveryTestModel(primaryID), discoveryTestModel(ignoredID),
	}}
	result, err := store.ApplyVendorCatalog(ctx, initial, modelsync.ApplyOptions{Reason: "integration", Actor: "model_sync_test"})
	if err != nil {
		t.Fatalf("首次同步发现模型: %v", err)
	}
	if result.Added != 0 || result.Discovered != 2 || result.Disabled != 0 {
		t.Fatalf("首次同步结果=%+v，期望只发现两个模型且不自动上架", result)
	}
	if _, err := store.ResolveModel(ctx, primaryID, fixture.tenantID); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("待审模型在上架前 ResolveModel err=%v，期望 ErrUnknownModel", err)
	}
	var runtimeRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM models WHERE canonical_id = $1 AND deleted_at IS NULL`,
		vendorCanonicalID(modelsync.VendorOpenAI, primaryID)).Scan(&runtimeRows); err != nil {
		t.Fatalf("统计上架前模型行: %v", err)
	}
	if runtimeRows != 0 {
		t.Fatalf("待审模型在上架前已写入 models：count=%d", runtimeRows)
	}

	page, err := store.ListModelDiscoveries(ctx, ModelDiscoveryListParams{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform},
		Vendor: modelsync.VendorOpenAI, Status: ModelDiscoveryPending, Search: primaryID,
	})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("查询待审模型 page=%+v err=%v", page, err)
	}
	primary := page.Items[0]
	if _, err := store.ListModelDiscoveries(ctx, ModelDiscoveryListParams{
		Access: ModelDiscoveryAccess{Role: modelAdminRoleTenant},
	}); !errors.Is(err, ErrModelDiscoveryForbidden) {
		t.Fatalf("租户管理员读取全局发现箱 err=%v，期望 forbidden", err)
	}

	promote := ModelDiscoveryDecision{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform, Actor: "admin_token:501", RequestID: "req-promote-" + suffix},
		ID:     primary.ID, Reason: "已核对供应商公开目录",
	}
	promoted, err := store.PromoteModelDiscovery(ctx, promote)
	if err != nil {
		t.Fatalf("上架待审模型: %v", err)
	}
	if promoted.Status != ModelDiscoveryPromoted || promoted.PromotedModelID == nil {
		t.Fatalf("上架结果=%+v", promoted)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO model_pool_bindings (tenant_id, model_id, pool_group_id, reason)
VALUES ($1, $2, $3, 'model-discovery-integration')
`, fixture.tenantID, *promoted.PromotedModelID, fixture.poolGroupID); err != nil {
		t.Fatalf("为已上架模型建立租户路由: %v", err)
	}
	resolved, err := store.ResolveModel(ctx, primaryID, fixture.tenantID)
	if err != nil {
		t.Fatalf("上架并绑定后解析模型: %v", err)
	}
	if resolved.DefaultProviderModelID != primaryID || resolved.CanonicalModelID != vendorCanonicalID(modelsync.VendorOpenAI, primaryID) {
		t.Fatalf("解析结果=%+v", resolved)
	}
	assertModelDiscoveryLog(t, ctx, pool, primary.ID, "promote_model_discovery", "admin_token:501", 1)
	if _, err := store.PromoteModelDiscovery(ctx, promote); err != nil {
		t.Fatalf("重复上架应幂等: %v", err)
	}
	assertModelDiscoveryLog(t, ctx, pool, primary.ID, "promote_model_discovery", "admin_token:501", 1)

	ignored := findModelDiscoveryByProviderID(t, ctx, store, ignoredID)
	ignore := ModelDiscoveryDecision{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform, Actor: "admin_user:77", RequestID: "req-ignore-" + suffix},
		ID:     ignored.ID, Reason: "该模型不进入运营目录",
	}
	ignored, err = store.IgnoreModelDiscovery(ctx, ignore)
	if err != nil || ignored.Status != ModelDiscoveryIgnored {
		t.Fatalf("忽略模型 result=%+v err=%v", ignored, err)
	}
	if _, err := store.IgnoreModelDiscovery(ctx, ignore); err != nil {
		t.Fatalf("重复忽略应幂等: %v", err)
	}
	assertModelDiscoveryLog(t, ctx, pool, ignored.ID, "ignore_model_discovery", "admin_user:77", 1)
	result, err = store.ApplyVendorCatalog(ctx, initial, modelsync.ApplyOptions{Reason: "repeat", Actor: "model_sync_test"})
	if err != nil {
		t.Fatalf("忽略后重跑同步: %v", err)
	}
	if !containsModelRef(result.Ignored, ignoredID) {
		t.Fatalf("重跑结果 ignored=%v，未保留已忽略项", result.Ignored)
	}
	if got := findModelDiscoveryByProviderID(t, ctx, store, ignoredID).Status; got != ModelDiscoveryIgnored {
		t.Fatalf("忽略项重跑后 status=%q", got)
	}

	transientID := "gpt-discovery-transient-" + suffix
	withTransient := modelsync.Catalog{Vendor: modelsync.VendorOpenAI, Models: []modelsync.Model{
		discoveryTestModel(primaryID), discoveryTestModel(ignoredID), discoveryTestModel(transientID),
	}}
	if result, err = store.ApplyVendorCatalog(ctx, withTransient, modelsync.ApplyOptions{}); err != nil || result.Discovered != 1 {
		t.Fatalf("发现临时模型 result=%+v err=%v", result, err)
	}
	transient := findModelDiscoveryByProviderID(t, ctx, store, transientID)
	if result, err = store.ApplyVendorCatalog(ctx, initial, modelsync.ApplyOptions{}); err != nil ||
		result.DiscoveryAbsent != 1 || !containsModelRef(result.Removed, transientID) {
		t.Fatalf("临时模型消失 result=%+v err=%v", result, err)
	}
	if got := findModelDiscoveryByProviderID(t, ctx, store, transientID).Status; got != ModelDiscoveryAbsent {
		t.Fatalf("消失模型 status=%q want absent", got)
	}
	if result, err = store.ApplyVendorCatalog(ctx, withTransient, modelsync.ApplyOptions{}); err != nil || result.Discovered != 1 {
		t.Fatalf("临时模型重新出现 result=%+v err=%v", result, err)
	}
	if got := findModelDiscoveryByProviderID(t, ctx, store, transientID).Status; got != ModelDiscoveryPending {
		t.Fatalf("重新出现模型 status=%q want pending", got)
	}
	if transient.ID != findModelDiscoveryByProviderID(t, ctx, store, transientID).ID {
		t.Fatal("重新出现时不得新建重复发现行")
	}

	geminiID := "gemini-discovery-" + suffix
	result, err = store.ApplyVendorCatalog(ctx, modelsync.Catalog{
		Vendor: modelsync.VendorGemini,
		Models: []modelsync.Model{{
			ID: geminiID, DisplayName: "Gemini Discovery " + suffix, OwnedBy: "google",
			ProtocolFamily: "gemini", ContextWindow: 1048576, Capabilities: []string{"chat", "vision"},
		}}}, modelsync.ApplyOptions{})
	if err != nil || result.Discovered != 1 {
		t.Fatalf("Gemini 模型发现 result=%+v err=%v", result, err)
	}
	if got := findModelDiscoveryByProviderID(t, ctx, store, geminiID).ProtocolFamily; got != registrydefault.ProtocolGeminiMessages {
		t.Fatalf("Gemini 发现项 protocol_family=%q want %q", got, registrydefault.ProtocolGeminiMessages)
	}

	assertModelDiscoveryIdentityConflict(t, ctx, pool, store, suffix, primaryID, ignoredID, transientID)
	assertModelDiscoveryMultiVendorRollback(t, ctx, pool, store, suffix)
	assertConcurrentModelDiscoveryPromotion(t, ctx, pool, store, suffix)
}

type modelDiscoveryFixture struct {
	suffix      string
	tenantID    int64
	poolGroupID int64
}

func seedModelDiscoveryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) modelDiscoveryFixture {
	t.Helper()
	var fixture modelDiscoveryFixture
	fixture.suffix = suffix
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "model-discovery-"+suffix).Scan(&fixture.tenantID); err != nil {
		t.Fatalf("创建发现箱测试租户: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO model_registry_tenant_policies (tenant_id, inherit_global_catalog) VALUES ($1, true)`, fixture.tenantID); err != nil {
		t.Fatalf("开启测试租户全局目录继承: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		fixture.tenantID, "model-discovery-pool-"+suffix).Scan(&fixture.poolGroupID); err != nil {
		t.Fatalf("创建发现箱测试池: %v", err)
	}
	return fixture
}

func cleanupModelDiscoveryFixture(t *testing.T, pool *pgxpool.Pool, fixture modelDiscoveryFixture) {
	t.Helper()
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `DELETE FROM admin_audit_events WHERE target_type = 'model_discovery' AND target_id IN (SELECT id FROM model_discovery_inbox WHERE provider_model_id LIKE '%' || $1 || '%')`, fixture.suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM model_pool_bindings WHERE tenant_id = $1`, fixture.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM model_discovery_inbox WHERE provider_model_id LIKE '%' || $1 || '%'`, fixture.suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM model_registry_capabilities WHERE scope = 'global' AND model_id IN (SELECT id FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%')`, fixture.suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM model_aliases WHERE scope = 'global' AND model_id IN (SELECT id FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%')`, fixture.suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM models WHERE scope = 'global' AND canonical_id LIKE '%' || $1 || '%'`, fixture.suffix)
	_, _ = pool.Exec(ctx, `DELETE FROM model_registry_snapshots WHERE tenant_id = $1`, fixture.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM model_registry_tenant_policies WHERE tenant_id = $1`, fixture.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM pool_groups WHERE tenant_id = $1`, fixture.tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, fixture.tenantID)
}

func discoveryTestModel(id string) modelsync.Model {
	return modelsync.Model{
		ID: id, DisplayName: "Discovery " + id, OwnedBy: "openai",
		ProtocolFamily: "openai_chat", ContextWindow: 128000, Capabilities: []string{"chat", "tools"},
	}
}

func findModelDiscoveryByProviderID(t *testing.T, ctx context.Context, store *PostgresRegistry, providerModelID string) ModelDiscovery {
	t.Helper()
	page, err := store.ListModelDiscoveries(ctx, ModelDiscoveryListParams{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform}, Search: providerModelID,
	})
	if err != nil {
		t.Fatalf("按 provider model id 查询发现项: %v", err)
	}
	for _, item := range page.Items {
		if item.ProviderModelID == providerModelID {
			return item
		}
	}
	t.Fatalf("未找到发现项 %q，page=%+v", providerModelID, page)
	return ModelDiscovery{}
}

func assertModelDiscoveryLog(t *testing.T, ctx context.Context, pool *pgxpool.Pool, discoveryID int64, action, actor string, want int) {
	t.Helper()
	var count int
	var category string
	err := pool.QueryRow(ctx, `
SELECT count(*), COALESCE(max(log_category), '')
FROM admin_audit_events
WHERE target_type = 'model_discovery' AND target_id = $1 AND action = $2 AND actor_id = $3
`, discoveryID, action, actor).Scan(&count, &category)
	if err != nil {
		t.Fatalf("查询模型发现日志: %v", err)
	}
	if count != want || (want > 0 && category != "operation") {
		t.Fatalf("模型发现日志 count=%d category=%q want count=%d operation", count, category, want)
	}
}

func assertModelDiscoveryIdentityConflict(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *PostgresRegistry, suffix string, catalogIDs ...string) {
	t.Helper()
	collisionID := "gpt-discovery-collision-" + suffix
	var manualModelID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO models (tenant_id, scope, canonical_id, protocol_family, default_provider_model_id, model_owner)
VALUES (NULL, 'global', $1, 'openai_chat', $2, 'operator') RETURNING id
`, "manual/"+collisionID, collisionID).Scan(&manualModelID); err != nil {
		t.Fatalf("创建人工冲突模型: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO model_aliases (tenant_id, scope, model_id, public_alias_normalized, public_alias_display, source)
VALUES (NULL, 'global', $1, $2, $3, 'operator')
`, manualModelID, AliasNormalize(collisionID), collisionID); err != nil {
		t.Fatalf("创建人工冲突别名: %v", err)
	}
	models := make([]modelsync.Model, 0, len(catalogIDs)+1)
	for _, id := range catalogIDs {
		models = append(models, discoveryTestModel(id))
	}
	models = append(models, discoveryTestModel(collisionID))
	if _, err := store.ApplyVendorCatalog(ctx, modelsync.Catalog{Vendor: modelsync.VendorOpenAI, Models: models}, modelsync.ApplyOptions{}); err != nil {
		t.Fatalf("同步人工冲突模型: %v", err)
	}
	discovery := findModelDiscoveryByProviderID(t, ctx, store, collisionID)
	_, err := store.PromoteModelDiscovery(ctx, ModelDiscoveryDecision{
		Access: ModelDiscoveryAccess{Role: modelAdminRolePlatform, Actor: "admin_token:502"},
		ID:     discovery.ID, Reason: "验证冲突",
	})
	if !errors.Is(err, ErrModelDiscoveryConflict) {
		t.Fatalf("人工别名占位上架 err=%v，期望显式冲突", err)
	}
	var source string
	var gotModelID int64
	if err := pool.QueryRow(ctx, `SELECT model_id, source FROM model_aliases WHERE scope='global' AND public_alias_normalized=$1 AND deleted_at IS NULL`, AliasNormalize(collisionID)).Scan(&gotModelID, &source); err != nil {
		t.Fatalf("读取人工冲突别名: %v", err)
	}
	if gotModelID != manualModelID || source != "operator" {
		t.Fatalf("冲突后人工别名被篡改 model_id=%d source=%q", gotModelID, source)
	}
	if got := findModelDiscoveryByProviderID(t, ctx, store, collisionID).Status; got != ModelDiscoveryPending {
		t.Fatalf("冲突后发现状态=%q want pending", got)
	}
}

func assertModelDiscoveryMultiVendorRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *PostgresRegistry, suffix string) {
	t.Helper()
	rollbackID := "gpt-discovery-rollback-" + suffix
	_, err := store.ApplyVendorCatalogs(ctx, []modelsync.Catalog{
		{Vendor: modelsync.VendorOpenAI, Models: []modelsync.Model{discoveryTestModel(rollbackID)}},
		{Vendor: modelsync.VendorGemini, Models: []modelsync.Model{{
			ID: "gemini-invalid-" + suffix, ProtocolFamily: "gemini", Capabilities: []string{"unknown-capability"},
		}}},
	}, modelsync.ApplyOptions{})
	if err == nil {
		t.Fatal("第二供应商非法能力应使整批同步失败")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM model_discovery_inbox WHERE provider_model_id = $1`, rollbackID).Scan(&count); err != nil {
		t.Fatalf("查询回滚发现项: %v", err)
	}
	if count != 0 {
		t.Fatalf("多供应商失败后第一供应商仍落库 count=%d", count)
	}
}

func assertConcurrentModelDiscoveryPromotion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *PostgresRegistry, suffix string) {
	t.Helper()
	providerModelID := "gpt-discovery-concurrent-" + suffix
	var discoveryID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO model_discovery_inbox (
    vendor, model_id_normalized, provider_model_id, display_name, owned_by,
    protocol_family, context_window, capabilities
) VALUES ('openai', $1, $2, $3, 'openai', 'openai_chat', 128000, ARRAY['chat', 'tools'])
RETURNING id
`, AliasNormalize(providerModelID), providerModelID, "Concurrent "+providerModelID).Scan(&discoveryID); err != nil {
		t.Fatalf("创建并发上架发现项: %v", err)
	}

	type promotionResult struct {
		item ModelDiscovery
		err  error
	}
	start := make(chan struct{})
	results := make(chan promotionResult, 2)
	decision := ModelDiscoveryDecision{
		Access: ModelDiscoveryAccess{
			Role: modelAdminRolePlatform, Actor: "admin_token:concurrent", RequestID: "req-concurrent-" + suffix,
		},
		ID: discoveryID, Reason: "并发幂等上架验证",
	}
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			item, err := store.PromoteModelDiscovery(ctx, decision)
			results <- promotionResult{item: item, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("并发上架结果 err1=%v err2=%v", first.err, second.err)
	}
	if first.item.PromotedModelID == nil || second.item.PromotedModelID == nil ||
		*first.item.PromotedModelID != *second.item.PromotedModelID {
		t.Fatalf("并发上架没有收敛到同一模型 first=%+v second=%+v", first.item, second.item)
	}

	var modelRows, aliasRows, logRows int
	if err := pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM models
     WHERE scope = 'global' AND tenant_id IS NULL AND deleted_at IS NULL AND canonical_id = $1),
    (SELECT count(*) FROM model_aliases
     WHERE scope = 'global' AND tenant_id IS NULL AND deleted_at IS NULL AND public_alias_normalized = $2),
    (SELECT count(*) FROM admin_audit_events
     WHERE target_type = 'model_discovery' AND target_id = $3 AND action = 'promote_model_discovery')
`, vendorCanonicalID(modelsync.VendorOpenAI, providerModelID), AliasNormalize(providerModelID), discoveryID).
		Scan(&modelRows, &aliasRows, &logRows); err != nil {
		t.Fatalf("核对并发上架落库结果: %v", err)
	}
	if modelRows != 1 || aliasRows != 1 || logRows != 1 {
		t.Fatalf("并发上架落库 model=%d alias=%d log=%d，期望均为 1", modelRows, aliasRows, logRows)
	}
}

func containsModelRef(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
