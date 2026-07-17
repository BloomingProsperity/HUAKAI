//go:build integration_pg

package adminhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/bodyparamgate"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// TestChannelCatalogRewriteGatesReachRuntimeConsumers 从真实 HTTP 写口开始，跨过
// PostgreSQL 与 registry，再让三门进入实际请求改写函数。它同时咬住 create
// 三门落库回显，以及 update 只改一门时另外两门不得被清空。
func TestChannelCatalogRewriteGatesReachRuntimeConsumers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := openAdminIntegrationPool(t, ctx)
	tenantID, poolGroupID, alias := seedChannelRewriteGateRegistry(t, ctx, pool)
	queries := admindb.New(pool)
	deps := AdminChannelCatalogDeps{
		Auth:    apiKeyAuthStub{ident: tenantOperator(tenantID)},
		Queries: queries,
		Store:   NewChannelCatalogStoreAdapter(queries, pool),
	}
	resolver := registry.NewPostgresRegistry(pool, nil)

	createBody := fmt.Sprintf(
		`{"pool_group_id":%d,"name":"rewrite-gates","enabled":true,"body_param_strips":["drop_create"],"param_override":{"temperature":0.25,"metadata":{"source":"create"}},"sensitive_words":["word_create"]}`,
		poolGroupID,
	)
	createdRec := invokeChannelCatalogMutation(t, deps, http.MethodPost, "/admin/v1/channels", createBody)
	assertChannelCatalogStatus(t, createdRec, http.StatusCreated)
	var created channelCatalogItem
	decodeChannelCatalogBody(t, createdRec, &created)
	assertChannelRewriteGateItem(t, created, []string{"drop_create"}, 0.25, []string{"word_create"})

	getRec := invokeChannelCatalogMutation(t, deps, http.MethodGet,
		fmt.Sprintf("/admin/v1/channels/%d", created.ID), "")
	assertChannelCatalogStatus(t, getRec, http.StatusOK)
	var got channelCatalogItem
	decodeChannelCatalogBody(t, getRec, &got)
	assertChannelRewriteGateItem(t, got, []string{"drop_create"}, 0.25, []string{"word_create"})

	listRec := invokeChannelCatalogMutation(t, deps, http.MethodGet, "/admin/v1/channels?limit=10", "")
	assertChannelCatalogStatus(t, listRec, http.StatusOK)
	var listed channelCatalogListResponse
	decodeChannelCatalogBody(t, listRec, &listed)
	if len(listed.Items) != 1 {
		t.Fatalf("list items=%d want 1", len(listed.Items))
	}
	assertChannelRewriteGateItem(t, listed.Items[0], []string{"drop_create"}, 0.25, []string{"word_create"})

	binding := resolveRewriteGateBinding(t, ctx, resolver, alias, tenantID, poolGroupID)
	assertBodyParamConsumers(t, binding, "drop_create", 0.25)
	assertSensitiveWordConsumer(t, binding, "word_create")

	// 只提交 body_param_strips；SQL presence 分支必须保留另两门。
	updateBody := fmt.Sprintf(
		`{"pool_group_id":%d,"name":"rewrite-gates","enabled":true,"body_param_strips":["drop_updated"]}`,
		poolGroupID,
	)
	updatedRec := invokeChannelCatalogMutation(t, deps, http.MethodPut,
		fmt.Sprintf("/admin/v1/channels/%d", created.ID), updateBody)
	assertChannelCatalogStatus(t, updatedRec, http.StatusOK)
	var updated channelCatalogItem
	decodeChannelCatalogBody(t, updatedRec, &updated)
	assertChannelRewriteGateItem(t, updated, []string{"drop_updated"}, 0.25, []string{"word_create"})
	getRec = invokeChannelCatalogMutation(t, deps, http.MethodGet,
		fmt.Sprintf("/admin/v1/channels/%d", created.ID), "")
	assertChannelCatalogStatus(t, getRec, http.StatusOK)
	decodeChannelCatalogBody(t, getRec, &got)
	assertChannelRewriteGateItem(t, got, []string{"drop_updated"}, 0.25, []string{"word_create"})

	// 复用同一个 registry 实例二次解析，固定“管理更新后立即生效”的缓存契约。
	updatedBinding := resolveRewriteGateBinding(t, ctx, resolver, alias, tenantID, poolGroupID)
	assertBodyParamConsumers(t, updatedBinding, "drop_updated", 0.25)
	assertSensitiveWordConsumer(t, updatedBinding, "word_create")
}

func assertChannelRewriteGateItem(t *testing.T, item channelCatalogItem, strips []string, temperature float64, words []string) {
	t.Helper()
	if item.ID <= 0 || !reflect.DeepEqual(item.BodyParamStrips, strips) || !reflect.DeepEqual(item.SensitiveWords, words) {
		t.Fatalf("三门数组回显不一致:item=%+v want strips=%v words=%v", item, strips, words)
	}
	var override map[string]any
	if err := json.Unmarshal(item.ParamOverride, &override); err != nil {
		t.Fatalf("解析 param_override 回显:%v body=%s", err, item.ParamOverride)
	}
	if override["temperature"] != temperature {
		t.Fatalf("temperature=%v want %v override=%v", override["temperature"], temperature, override)
	}
	metadata, ok := override["metadata"].(map[string]any)
	if !ok || metadata["source"] != "create" {
		t.Fatalf("param_override 嵌套值未精确回显:%v", override)
	}
}

func resolveRewriteGateBinding(t *testing.T, ctx context.Context, resolver registry.Registry, alias string, tenantID, poolGroupID int64) registry.BindingMetadata {
	t.Helper()
	resolved, err := resolver.ResolveModel(ctx, alias, tenantID)
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	for _, binding := range resolved.BindingMetadata {
		if binding.PoolGroupID == poolGroupID {
			return binding
		}
	}
	t.Fatalf("registry 未返回 pool_group_id=%d 的 binding:%+v", poolGroupID, resolved.BindingMetadata)
	return registry.BindingMetadata{}
}

func assertBodyParamConsumers(t *testing.T, binding registry.BindingMetadata, stripKey string, temperature float64) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"model":"x","%s":"remove-me","temperature":0.9,"keep":"yes"}`, stripKey))
	rewritten, err := bodyparamgate.ApplyParamOverride(body, binding.ParamOverride)
	if err != nil {
		t.Fatalf("ApplyParamOverride: %v", err)
	}
	rewritten, err = bodyparamgate.StripBodyParams(rewritten, binding.BodyParamStrips)
	if err != nil {
		t.Fatalf("StripBodyParams: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("解析参数门输出:%v body=%s", err, rewritten)
	}
	if _, exists := root[stripKey]; exists {
		t.Fatalf("body_param_strips 未消费 registry 值:body=%s", rewritten)
	}
	if root["temperature"] != temperature || root["keep"] != "yes" {
		t.Fatalf("param_override 未消费或误伤旁路字段:body=%s", rewritten)
	}
}

func assertSensitiveWordConsumer(t *testing.T, binding registry.BindingMetadata, word string) {
	t.Helper()
	body := []byte(fmt.Sprintf(`{"system":"contains %s","messages":[{"role":"user","content":"clean"}]}`, word))
	rewritten, err := gateway.ApplyDispatchBodyControls(body, gateway.DispatchBodyControlsFromBinding(binding))
	if err != nil {
		t.Fatalf("ApplyDispatchBodyControls: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("解析敏感词门输出:%v body=%s", err, rewritten)
	}
	system, _ := root["system"].(string)
	want := string(word[0]) + "\u200b" + word[1:]
	if !strings.Contains(system, want) || strings.Contains(system, word) {
		t.Fatalf("sensitive_words 未进入实际改写:got=%q want contains %q", system, want)
	}
}

func seedChannelRewriteGateRegistry(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, int64, string) {
	t.Helper()
	suffix := strings.ToLower(uuid.NewString())
	alias := "rewrite-gates-" + suffix
	var tenantID int64
	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "rewrite-gates-"+suffix).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	var poolGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1,$2) RETURNING id`, tenantID, "rewrite-pool-"+suffix).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	var modelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO models (tenant_id, scope, canonical_id, protocol_family, default_provider_model_id,
		 default_context_window, default_request_timeout_ms, pricing_class, status)
		 VALUES ($1,'tenant',$2,'anthropic_messages',$3,200000,60000,'standard','active') RETURNING id`,
		tenantID, "huakai/rewrite-gates-"+suffix, "rewrite-provider-"+suffix,
	).Scan(&modelID); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_aliases (tenant_id, scope, model_id, public_alias_normalized, public_alias_display, status)
		 VALUES ($1,'tenant',$2,$3,$3,'active')`, tenantID, modelID, alias,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO model_pool_bindings
		 (tenant_id, model_id, pool_group_id, priority, weight, selection_mode, fallback_class, enabled, reason)
		 VALUES ($1,$2,$3,100,1,'strict_priority','normal',true,'rewrite gate integration')`,
		tenantID, modelID, poolGroupID,
	); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM admin_audit_events WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM model_pool_bindings WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM model_registry_capabilities WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM model_aliases WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM models WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM model_registry_snapshots WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	return tenantID, poolGroupID, alias
}
