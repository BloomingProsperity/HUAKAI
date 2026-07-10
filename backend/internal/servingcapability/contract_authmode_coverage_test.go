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
	var a1NoHandler []string
	for _, c := range DefaultContracts() {
		for _, authMode := range c.AuthModes {
			handler, err := reg.MustLookup(c.Vendor, authMode)
			if err != nil {
				// A1(已在 official-api-module-audit 记录):契约声明但无 credential handler
				// =无法导入账号。这类族本就不可创建有效凭据,G1 泛化后在 MustLookup 处被拒;
				// 修 A1(补 handler+DB 或降级 release)属独立切片,此处记录不 fail。
				a1NoHandler = append(a1NoHandler, c.Family+"/"+authMode)
				continue
			}
			if err := ValidateAccountCompatibility(c.Family, c.Vendor, authMode, handler.RuntimeKind()); err != nil {
				t.Errorf("family %s: 契约自声明的合法组合 vendor=%s authMode=%s runtimeKind=%s 却被拒(契约 RuntimeCredentialKinds 漏项,泛化会误拒合法账号): %v",
					c.Family, c.Vendor, authMode, handler.RuntimeKind(), err)
			}
		}
	}
	if len(a1NoHandler) > 0 {
		t.Logf("A1 已知 gap(契约声明但无 handler,不可导入,见 official-api-module-audit): %v", a1NoHandler)
	}
}
