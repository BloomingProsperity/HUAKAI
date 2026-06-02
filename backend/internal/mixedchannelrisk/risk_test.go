package mixedchannelrisk

import "testing"

func TestEvaluateReportsDifferentSourceVendorAndCredentialType(t *testing.T) {
	report := Evaluate(Account{
		ProviderID: 11, ChannelID: 9, AccountType: "api_key", Vendor: "openai", AuthMode: "api_key",
	}, []Account{{
		ID: 61, ProviderID: 8, ChannelID: 9, AccountType: "oauth", Vendor: "anthropic", AuthMode: "claude_ai_oauth",
	}})
	if !report.HighRisk {
		t.Fatal("HighRisk=false want true")
	}
	seen := map[string]bool{}
	for _, item := range report.Items {
		seen[item.Dimension] = true
		if item.ExistingAccountID != 61 {
			t.Fatalf("ExistingAccountID=%d want 61", item.ExistingAccountID)
		}
	}
	for _, dim := range []string{"source", "vendor", "credential_type"} {
		if !seen[dim] {
			t.Fatalf("missing risk dimension %s in %+v", dim, report.Items)
		}
	}
}

func TestEvaluateSameSourceVendorAndCredentialTypeIsNotRisk(t *testing.T) {
	report := Evaluate(Account{
		ProviderID: 11, ChannelID: 9, AccountType: "api_key", Vendor: "openai", AuthMode: "api_key",
	}, []Account{{
		ID: 61, ProviderID: 11, ChannelID: 9, AccountType: "api_key", Vendor: "openai", AuthMode: "api_key",
	}})
	if report.HighRisk || len(report.Items) != 0 {
		t.Fatalf("report=%+v want no risk", report)
	}
}
