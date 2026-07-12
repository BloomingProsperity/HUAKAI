package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func paStrPtr(s string) *string { return &s }

// 造含**敏感/自由文本**的账号行:Extra(原始 blob)、RateLimitReason、Tags 值、ProxyGroupID 各埋哨兵串,
// 验证投影绝不 echo 它们(§14 区分性变异:若投影回退露明文,no-leak 断言转红)。
func fakeAccountRows() []admindb.AdminProviderAccountRow {
	return []admindb.AdminProviderAccountRow{
		{
			ID: 11, ProviderID: 2, ChannelID: 5, Name: "anthropic-primary", AccountType: "oauth",
			Enabled: true, HealthState: "healthy", CredentialState: "active", OAuthEndpointHealth: "ok",
			Priority: 1, StaticWeight: 100, CapConcurrency: 8, InFlightCount: 2, PoolMode: true,
			ProbeModel: paStrPtr("claude-probe"), ModelAllowList: []string{"claude-3-5-sonnet"},
			CapabilityFlags: []string{"vision"}, TokenVersion: 3,
			Tags:               []string{"SENTINEL-TAG-must-not-leak", "prod"},
			Extra:              []byte(`{"SENTINEL-EXTRA":"must-not-leak"}`),
			RateLimitReason:    paStrPtr("SENTINEL-RLREASON-must-not-leak"),
			ProxyGroupID:       paStrPtr("SENTINEL-PROXYGRP-must-not-leak"),
			LastRefreshOutcome: paStrPtr("success"),
		},
		{
			ID: 12, ProviderID: 2, ChannelID: 6, Name: "anthropic-backup", AccountType: "oauth",
			Enabled: false, HealthState: "revoked", CredentialState: "revoked",
			Priority: 2, StaticWeight: 50,
		},
		{
			ID: 13, ProviderID: 3, ChannelID: 7, Name: "openai-1", AccountType: "api_key",
			Enabled: true, HealthState: "healthy", CredentialState: "active",
		},
	}
}

func TestProviderAccountListSpec(t *testing.T) {
	deps := ProviderAccountListDeps{
		List: func(_ context.Context, params admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
			if params.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7(必须用已鉴权 req.TenantID)", params.TenantID)
			}
			if params.LimitCount != providerAccountListLimit {
				t.Fatalf("LimitCount 应为 providerAccountListLimit=%d, got %d", providerAccountListLimit, params.LimitCount)
			}
			return fakeAccountRows(), nil
		},
	}
	spec := ProviderAccountListSpec(deps)

	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["account_count"].(int) != 3 {
		t.Fatalf("account_count 应 3, got %v", res.Summary["account_count"])
	}
	// enabled_count 计算值:2 个 Enabled=true。
	if res.Summary["enabled_count"].(int) != 2 {
		t.Fatalf("enabled_count 应 2, got %v", res.Summary["enabled_count"])
	}
	// by_health_state:healthy=2, revoked=1(过滤前全量统计)。
	byHS := res.Summary["by_health_state"].(map[string]any)
	if byHS["healthy"] != 2 || byHS["revoked"] != 1 {
		t.Fatalf("by_health_state 错: %v", byHS)
	}

	items := res.Summary["items"].([]map[string]any)
	a0 := items[0]
	if a0["id"].(int64) != 11 || a0["name"] != "anthropic-primary" || a0["health_state"] != "healthy" {
		t.Fatalf("account[0] 投影错: %v", a0)
	}
	if a0["priority"].(int32) != 1 || a0["cap_concurrency"].(int32) != 8 || a0["credential_state"] != "active" {
		t.Fatalf("account[0] 路由/状态投影错: %v", a0)
	}
	// Tags 只露 count(不露值),ProxyID 存在→has_proxy(但本行无 ProxyID→false)。
	if a0["tag_count"].(int) != 2 {
		t.Fatalf("tag_count 应 2(不露 tag 值), got %v", a0["tag_count"])
	}
	if a0["has_proxy"] != false {
		t.Fatalf("account[0] 无 ProxyID,has_proxy 应 false: %v", a0["has_proxy"])
	}

	// 绝不投明文敏感/自由文本键,也不回投 tenant_id。
	for _, leakKey := range []string{"extra", "rate_limit_reason", "tags", "proxy_group_id", "tenant_id"} {
		if _, has := a0[leakKey]; has {
			t.Fatalf("泄露明文/自由文本键 %q: %v", leakKey, a0)
		}
	}

	// §14 核心 no-leak:整个 Summary 序列化后绝不含任何敏感哨兵串。
	blob, err := json.Marshal(res.Summary)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, sentinel := range []string{"SENTINEL-TAG", "SENTINEL-EXTRA", "SENTINEL-RLREASON", "SENTINEL-PROXYGRP"} {
		if strings.Contains(string(blob), sentinel) {
			t.Fatalf("敏感字段泄露到工具输出: 命中哨兵 %q\n%s", sentinel, blob)
		}
	}
}

// state 过滤参数透传到 SQL(SQL 把 ” 当 no-filter、把具体值匹配对应状态)。
func TestProviderAccountListStateFilterPassthrough(t *testing.T) {
	var gotState string
	deps := ProviderAccountListDeps{
		List: func(_ context.Context, params admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
			gotState = params.StateFilter
			return nil, nil
		},
	}
	r := req(7)
	r.Args["state"] = "disabled"
	if _, err := ProviderAccountListSpec(deps).Run(context.Background(), r); err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotState != "disabled" {
		t.Fatalf("state 过滤未透传, got %q", gotState)
	}
}

func TestProviderAccountListNilDep(t *testing.T) {
	_, err := ProviderAccountListSpec(ProviderAccountListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestProviderAccountListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := ProviderAccountListDeps{
		List: func(_ context.Context, _ admindb.ListAdminProviderAccountsParams) ([]admindb.AdminProviderAccountRow, error) {
			return nil, sentinel
		},
	}
	_, err := ProviderAccountListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}
