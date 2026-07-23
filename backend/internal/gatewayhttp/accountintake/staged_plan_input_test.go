package accountintake

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
)

func Test暂存计划经过JSONB重排仍保留哈希原始字节(t *testing.T) {
	original := PlanInput{
		TenantID: 7, SourceKind: intake.SourceCRSSync,
		DefaultVendor: "openai", DefaultAuthMode: "api_key",
		Account: AccountDefaults{
			ProviderID: 11, ChannelID: 12, NamePrefix: "source", AccountType: "api_key",
			Extra:                  json.RawMessage(`{"z":1, "a":{"second":2,"first":1}}`),
			TempUnschedulableRules: json.RawMessage(`[{"z":2, "a":1}]`),
		},
		Now: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(stagedInputFromPlan(original))
	if err != nil {
		t.Fatal(err)
	}
	var jsonb any
	if err := json.Unmarshal(raw, &jsonb); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(jsonb)
	if err != nil {
		t.Fatal(err)
	}
	var loaded stagedPlanInput
	if err := json.Unmarshal(reordered, &loaded); err != nil {
		t.Fatal(err)
	}
	restored := loaded.withContent("secret")
	if !bytes.Equal(restored.Account.Extra, original.Account.Extra) {
		t.Fatalf("extra 漂移：got=%s want=%s", restored.Account.Extra, original.Account.Extra)
	}
	if !bytes.Equal(restored.Account.TempUnschedulableRules, original.Account.TempUnschedulableRules) {
		t.Fatalf("临时摘除规则漂移：got=%s want=%s",
			restored.Account.TempUnschedulableRules, original.Account.TempUnschedulableRules)
	}
}

func Test旧暂存行仍能解码并等待自然过期(t *testing.T) {
	raw := []byte(`{
	  "tenant_id":7,
	  "source_kind":"oauth",
	  "default_vendor":"openai",
	  "default_auth_mode":"chatgpt_oauth",
	  "account":{
	    "provider_id":11,
	    "channel_id":12,
	    "name_prefix":"legacy",
	    "account_type":"oauth",
	    "extra":{"legacy":true},
	    "temp_unschedulable_rules":[{"status":429}]
	  },
	  "now":"2026-07-23T12:00:00Z"
	}`)
	var loaded stagedPlanInput
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	restored := loaded.withContent("secret")
	if restored.TenantID != 7 || restored.SourceKind != intake.SourceOAuth ||
		!bytes.Contains(restored.Account.Extra, []byte(`"legacy"`)) ||
		!bytes.Contains(restored.Account.TempUnschedulableRules, []byte(`429`)) {
		t.Fatalf("旧暂存行解码失败：%+v", restored)
	}
}
