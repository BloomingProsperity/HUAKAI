package servingcapability

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

// TestAllContractAuthModesMaterializeCompatibly 是 G1 泛化的前置安全网:每个 serving
// 契约声明的 (vendor, authMode) 组合,经 handler registry 解出的 runtimeKind 必须被
// 该族自己的契约接受。若此测试红,说明契约的 RuntimeCredentialKinds 漏了某 authMode
// 的 runtime kind——把 ValidateProtocolCompatibility 泛化到全族会因此误拒合法账号。
// 先修契约(补 runtime kind),再泛化。
func TestAllContractAuthModesMaterializeCompatibly(t *testing.T) {
	reg := credentialstore.DefaultHandlerRegistry()
	var noHandler []string
	for _, c := range DefaultContracts() {
		for _, authMode := range c.AuthModes {
			handler, err := reg.MustLookup(c.Vendor, authMode)
			if err != nil {
				// 无 handler 的契约无法物化账号；发布态由各 family 的定向契约测试
				// 约束，此处只记录缺口并继续检查可物化组合的 runtime kind。
				noHandler = append(noHandler, c.Family+"/"+authMode)
				continue
			}
			if err := ValidateAccountCompatibility(c.Family, c.Vendor, authMode, handler.RuntimeKind()); err != nil {
				t.Errorf("family %s: 契约自声明的合法组合 vendor=%s authMode=%s runtimeKind=%s 却被拒(契约 RuntimeCredentialKinds 漏项,泛化会误拒合法账号): %v",
					c.Family, c.Vendor, authMode, handler.RuntimeKind(), err)
			}
		}
	}
	if len(noHandler) > 0 {
		t.Logf("契约无 handler、不可导入: %v", noHandler)
	}
}
