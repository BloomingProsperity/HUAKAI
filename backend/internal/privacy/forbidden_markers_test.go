package privacy

import "testing"

// 一等凭证形态判别:本仓自己签发的 hk_* key(admin/keygen.go)、裸 JWT、
// Google/GitHub token 必须被禁写扫描识别。变异靶:从标记表删任一新条目 → 红。
func TestFirstPartyCredentialMarkersDetected(t *testing.T) {
	for _, probe := range []string{
		"hk_live_abcdefgh23456789abcdefgh",
		"hk_test_abcdefgh23456789abcdefgh",
		"hk_admin_abcdefgh23456789abcdefgh",
		"eyJhbGciOiJIUzI1NiJ9.fake.fake", // 裸 JWT 头
		"AIzaFAKEFAKEFAKEFAKE",           // Google API key 形态
		"ghp_FAKEFAKEFAKEFAKE",           // GitHub PAT
		"github_pat_FAKEFAKE",            // GitHub 细粒度 PAT
	} {
		if !ContainsForbiddenRawData([]byte(probe)) {
			t.Errorf("一等凭证形态未被识别: %q", probe)
		}
	}
}

// map key 扫描判别:秘密出现在 JSON map key 位时同样命中(扫描树形表示与
// 输出原文表示必须一致)。变异靶:去掉 containsForbiddenValue 的 key 子串扫 → 红。
func TestMapKeysCarryingSecretsDetected(t *testing.T) {
	if !ContainsForbiddenRawData([]byte(`{"sk-live-FAKE":7}`)) {
		t.Fatal("JSON map key 承载的秘密未被识别")
	}
}

// 宽词窄化方向判别:独立 "ant-" 已窄化为 "sk-ant-",tenant-42 这类正常词
// 不再误杀;sk-ant- 真实凭证形态仍命中。
func TestAntMarkerNarrowedToSkAnt(t *testing.T) {
	if ContainsForbiddenRawData([]byte("tenant-42")) {
		t.Fatal(`宽词 "ant-" 误杀 tenant-42(应已窄化为 "sk-ant-")`)
	}
	if !ContainsForbiddenRawData([]byte("sk-ant-fakekey")) {
		t.Fatal("sk-ant- 凭证形态必须命中")
	}
}
