package tenantcapability

import "testing"

func TestCapabilityCatalogRejectsUnknownValues(t *testing.T) {
	if len(All()) != 6 {
		t.Fatalf("capability count=%d want 6", len(All()))
	}
	if _, err := Parse("account_intake.claude_cookie"); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse("account_intake.attacker"); err == nil {
		t.Fatal("unknown capability must be rejected")
	}
}
