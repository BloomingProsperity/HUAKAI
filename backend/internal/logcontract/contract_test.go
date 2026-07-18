package logcontract

import "testing"

func TestGlobalEnumsRejectUnknownValues(t *testing.T) {
	for _, category := range []string{"operation", "financial", "security", "error", "access", "recovery"} {
		if !ValidCategory(category) {
			t.Fatalf("合法分类被拒绝: %s", category)
		}
	}
	for _, invalid := range []string{"", "audit", "system", "ACCESS"} {
		if ValidCategory(invalid) {
			t.Fatalf("未知分类被接受: %q", invalid)
		}
	}
	if !ValidMachineIdentifier("billing.refund_failed") || ValidMachineIdentifier("Billing refund failed") {
		t.Fatal("机器标识校验没有区分稳定标识与展示文本")
	}
}
