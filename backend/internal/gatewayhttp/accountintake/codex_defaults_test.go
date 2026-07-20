package accountintake

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestApplyCodexAccountDefaultsMakesNewAccountSchedulable(t *testing.T) {
	in := applyCodexAccountDefaults(PlanInput{
		SourceKind:      intake.SourceCLI,
		DefaultVendor:   credentialstore.VendorOpenAI,
		DefaultAuthMode: credentialstore.AuthModeCodexCLIOAuth,
	})
	if in.Account.NamePrefix != "codex" || in.Account.AccountType != "oauth" {
		t.Fatalf("账号默认值=%+v", in.Account)
	}
	if in.Account.CapConcurrency == nil || *in.Account.CapConcurrency != 3 ||
		in.Account.Priority == nil || *in.Account.Priority != 50 {
		t.Fatalf("调度默认值=%+v", in.Account)
	}
	for _, capability := range codexDefaultCapabilities {
		if !containsString(in.Account.CapabilityFlags, capability) {
			t.Fatalf("默认能力=%v，缺少 %s", in.Account.CapabilityFlags, capability)
		}
	}
}

func TestApplyCodexAccountDefaultsPreservesExplicitOperationsSettings(t *testing.T) {
	concurrency := int32(9)
	priority := int32(7)
	in := applyCodexAccountDefaults(PlanInput{Account: AccountDefaults{
		ExactName:       "指定账号",
		AccountType:     "session",
		CapConcurrency:  &concurrency,
		Priority:        &priority,
		CapabilityFlags: []string{"custom", "stream"},
	}})
	if in.Account.ExactName != "指定账号" || in.Account.AccountType != "session" ||
		*in.Account.CapConcurrency != 9 || *in.Account.Priority != 7 {
		t.Fatalf("显式配置被覆盖：%+v", in.Account)
	}
	if len(in.Account.CapabilityFlags) != 5 || in.Account.CapabilityFlags[0] != "custom" {
		t.Fatalf("能力合并错误：%v", in.Account.CapabilityFlags)
	}
}

func TestResolveCodexLaneRejectsInvalidIdentifiersBeforeQuery(t *testing.T) {
	service := &Service{}
	_, err := service.resolveCodexLane(t.Context(), 7, AccountDefaults{ProviderID: -1})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v，期望 ErrInvalidInput", err)
	}
}
