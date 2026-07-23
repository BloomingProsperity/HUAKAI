package accountintake

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

// TestCanonicalReversalIdentity 判别性守卫:反转身份别名必须归一到 serving 契约认的
// 规范身份;非别名身份必须原样保留(幂等)。删除 credentialstore 里的归一分支会使本测试转红。
func TestCanonicalReversalIdentity(t *testing.T) {
	cases := []struct {
		name                 string
		vendor, authMode     string
		wantVendor, wantAuth string
	}{
		{"gemini/antigravity 别名归一", credentialstore.VendorGemini, credentialstore.AuthModeAntigravity, credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth},
		{"antigravity/oauth 规范身份幂等", credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth, credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth},
		{"gemini/code_assist 非别名不动", credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist},
		{"openai/codex 非别名不动", credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotVendor, gotAuth := credentialstore.CanonicalReversalIdentity(tc.vendor, tc.authMode)
			if gotVendor != tc.wantVendor || gotAuth != tc.wantAuth {
				t.Fatalf("credentialstore.CanonicalReversalIdentity(%q,%q)=(%q,%q) want (%q,%q)",
					tc.vendor, tc.authMode, gotVendor, gotAuth, tc.wantVendor, tc.wantAuth)
			}
		})
	}
}

// TestReversalIdentityNormalizationSatisfiesServingContract 双向一致性守卫:把身份归一
// 与 serving 契约绑死——归一产出必须被 antigravity_session 契约接受,归一前的别名必须被拒。
// 任一侧漂移(改归一产出 或 改 serving 契约身份)都会使本测试转红,防止导入↔serving 身份分裂。
func TestReversalIdentityNormalizationSatisfiesServingContract(t *testing.T) {
	const family = registrydefault.ProtocolAntigravitySession
	if !servingcapability.HasContract(family) {
		t.Fatalf("serving 契约缺失:%s", family)
	}

	vendor, authMode := credentialstore.CanonicalReversalIdentity(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity)
	if err := servingcapability.ValidateAccountCompatibility(family, vendor, authMode, credentialstore.RuntimeSessionToken); err != nil {
		t.Fatalf("归一身份 (%q,%q) 应被 %s 契约接受,却被拒:%v", vendor, authMode, family, err)
	}

	if err := servingcapability.ValidateAccountCompatibility(family, credentialstore.VendorGemini, credentialstore.AuthModeAntigravity, credentialstore.RuntimeSessionToken); err == nil {
		t.Fatalf("未归一的别名 (gemini,antigravity) 竟被 %s 契约接受;归一逻辑与契约已不一致", family)
	}
}
