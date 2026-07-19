package accountintake

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
)

func TestBundleStableConfigValidationAndExactName(t *testing.T) {
	enabled := true
	concurrency := int32(4)
	queue := int32(2)
	weight := int32(1)
	ratio := 0.7
	rpm := int64(30)
	input := PlanInput{
		TenantID: 7, SourceKind: intake.SourceJSON,
		DefaultVendor: "openai", DefaultAuthMode: "api_key", Content: `{"api_key":"secret"}`,
		Account: AccountDefaults{
			ProviderID: 2, ChannelID: 3, ExactName: "原账号名称", AccountType: "api_key",
			Enabled: &enabled, CapConcurrency: &concurrency, CapQueueSticky: &queue,
			CapQueueFallback: &queue, StaticWeight: &weight, UpstreamCostRatio: &ratio,
			RPMLimit: &rpm, Extra: json.RawMessage(`{"source":"bundle"}`),
			TempUnschedulableRules: json.RawMessage(`[]`), CustomErrorCodes: []int32{429, 503},
		},
	}
	normalized := normalizeInput(input)
	if err := validateInput(normalized); err != nil {
		t.Fatalf("valid bundle config: %v", err)
	}
	if got := accountName(normalized.Account, 99); got != "原账号名称" {
		t.Fatalf("exact account name=%q", got)
	}

	negative := int64(-1)
	normalized.Account.RPMLimit = &negative
	if err := validateInput(normalized); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative rpm err=%v want invalid input", err)
	}
}

func TestPlanHashChangesWhenProxyMaterialChanges(t *testing.T) {
	base := PlanInput{
		TenantID: 7, SourceKind: intake.SourceJSON, Content: `{"api_key":"secret"}`,
		Account: AccountDefaults{
			ProviderID: 2, ChannelID: 3, ExactName: "账号", AccountType: "api_key",
			Proxy: &ProxyMaterial{
				Protocol: "http", Host: "proxy.example", Port: 8080,
				AuthUsername: "operator", AuthSecret: "first-secret", SourceRef: "bundle-proxy",
			},
		},
	}
	first, err := planHash(base, intake.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	proxyCopy := *base.Account.Proxy
	proxyCopy.AuthSecret = "second-secret"
	changed.Account.Proxy = &proxyCopy
	second, err := planHash(changed, intake.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("代理秘密变化后计划哈希没有变化")
	}
	proxyCopy = *base.Account.Proxy
	proxyCopy.Host = "other-proxy.example"
	changed.Account.Proxy = &proxyCopy
	third, err := planHash(changed, intake.Plan{})
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("代理端点变化后计划哈希没有变化")
	}
}
