package hermesops

import (
	"context"
	"errors"
	"testing"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProviderCatalogListSpec(t *testing.T) {
	deps := ProviderCatalogListDeps{
		List: func(_ context.Context, params admindb.ListAdminProvidersByTenantParams) ([]admindb.ListAdminProvidersByTenantRow, error) {
			if params.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7", params.TenantID)
			}
			if params.PageLimit != catalogListLimit {
				t.Fatalf("PageLimit 应为 catalogListLimit=%d, got %d", catalogListLimit, params.PageLimit)
			}
			return []admindb.ListAdminProvidersByTenantRow{
				{ID: 1, Code: "anthropic", DisplayName: "Anthropic", UpstreamProtocol: "anthropic_messages", Enabled: true},
				{ID: 2, Code: "openai", DisplayName: "OpenAI", UpstreamProtocol: "openai_chat", Enabled: false},
			}, nil
		},
	}
	res, err := ProviderCatalogListSpec(deps).Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["provider_count"].(int) != 2 || res.Summary["enabled_count"].(int) != 1 {
		t.Fatalf("count 错: %v", res.Summary)
	}
	items := res.Summary["items"].([]map[string]any)
	if items[0]["code"] != "anthropic" || items[0]["upstream_protocol"] != "anthropic_messages" || items[0]["enabled"] != true {
		t.Fatalf("provider[0] 投影错: %v", items[0])
	}
}

func TestProviderCatalogListNilDep(t *testing.T) {
	_, err := ProviderCatalogListSpec(ProviderCatalogListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestProviderCatalogListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := ProviderCatalogListDeps{
		List: func(_ context.Context, _ admindb.ListAdminProvidersByTenantParams) ([]admindb.ListAdminProvidersByTenantRow, error) {
			return nil, sentinel
		},
	}
	_, err := ProviderCatalogListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}

func TestChannelCatalogListSpec(t *testing.T) {
	deps := ChannelCatalogListDeps{
		List: func(_ context.Context, params admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error) {
			if params.TenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7", params.TenantID)
			}
			if params.PageLimit != catalogListLimit {
				t.Fatalf("PageLimit 应为 catalogListLimit=%d, got %d", catalogListLimit, params.PageLimit)
			}
			return []admindb.ListAdminChannelsByTenantRow{
				{ID: 10, PoolGroupID: 3, Name: "anthropic-channel", FailoverStatusCodes: []int32{429, 503}, Enabled: true},
				{ID: 11, PoolGroupID: 3, Name: "openai-channel", Enabled: false},
			}, nil
		},
	}
	res, err := ChannelCatalogListSpec(deps).Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["channel_count"].(int) != 2 || res.Summary["enabled_count"].(int) != 1 {
		t.Fatalf("count 错: %v", res.Summary)
	}
	items := res.Summary["items"].([]map[string]any)
	c0 := items[0]
	if c0["name"] != "anthropic-channel" || c0["pool_group_id"].(int64) != 3 || c0["enabled"] != true {
		t.Fatalf("channel[0] 投影错: %v", c0)
	}
	codes := c0["failover_status_codes"].([]int32)
	if len(codes) != 2 || codes[0] != 429 {
		t.Fatalf("failover_status_codes 投影错: %v", codes)
	}
}

func TestChannelCatalogListNilDep(t *testing.T) {
	_, err := ChannelCatalogListSpec(ChannelCatalogListDeps{}).Run(context.Background(), req(7))
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestChannelCatalogListReadErrorBubbles(t *testing.T) {
	sentinel := errors.New("db down")
	deps := ChannelCatalogListDeps{
		List: func(_ context.Context, _ admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error) {
			return nil, sentinel
		},
	}
	_, err := ChannelCatalogListSpec(deps).Run(context.Background(), req(7))
	if !errors.Is(err, sentinel) {
		t.Fatalf("读错误应上抛, got %v", err)
	}
}
