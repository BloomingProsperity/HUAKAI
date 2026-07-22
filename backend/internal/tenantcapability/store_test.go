package tenantcapability

import (
	"errors"
	"testing"
)

func TestNormalizeCapability(t *testing.T) {
	got, err := normalizeCapability("  HERMES_OPERATIONS  ")
	if err != nil || got != HermesOperations {
		t.Fatalf("normalizeCapability()=(%q,%v)，期望 (%q,nil)", got, err, HermesOperations)
	}
	if _, err := normalizeCapability("unknown"); !errors.Is(err, ErrCapabilityUnknown) {
		t.Fatalf("未知能力错误=%v，期望 ErrCapabilityUnknown", err)
	}
}

func TestWithCapabilityDefaults(t *testing.T) {
	configured := Grant{TenantID: 7, Capability: HermesOperations, Enabled: true, Configured: true}
	got := withCapabilityDefaults(7, []Grant{configured})
	if len(got) != 2 {
		t.Fatalf("能力数量=%d，期望 2", len(got))
	}
	if got[0].Capability != AdvancedAccountIntake || got[0].Enabled || got[0].Configured {
		t.Fatalf("高级导入默认值异常: %+v", got[0])
	}
	if got[1] != configured {
		t.Fatalf("Hermes 能力=%+v，期望 %+v", got[1], configured)
	}
}
