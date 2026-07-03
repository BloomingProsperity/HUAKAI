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

// 豁免口径判别:携带敏感词根的合法字段名(refresh_token_fingerprint 含 refresh_token
// 词根)不被 sensitiveKey 误杀——由 sensitiveKeyExemptions 豁免。
// 变异靶:sensitiveKey 不走 exemptSensitiveKey 豁免 → refresh_token_fingerprint 被判敏感 → 红。
func TestAllowlistedKeyNamesNotFlagged(t *testing.T) {
	for _, probe := range []string{
		`{"alert_type":"ban_signal","credential_version":3}`,
		`{"account_credential_id":"c1","refresh_token_fingerprint":"fp"}`,
	} {
		if ContainsForbiddenRawData([]byte(probe)) {
			t.Errorf("合法字段名被误杀: %s", probe)
		}
	}
}

// 复审 S2 回归守卫:map key 禁写扫描曾复用值扫描的宽词(credential / sk-),把合法
// 结构化字段名当秘密误杀,连累 logfacade/DLQ/voucher 三消费方整条塌成 [REDACTED]/sentinel。
// 收敛为前缀锚定的 token 形态后,字段名不再命中。
// 变异靶:map key 分支改回 containsForbiddenString(k) 宽词子串扫 → credential_* 全被误判 → 红。
func TestLegitFieldNamesNotFlaggedByKeyScan(t *testing.T) {
	for _, probe := range []string{
		`{"credential_state":"active"}`,
		`{"credential_type":"oauth"}`,
		`{"credential_vendor":"anthropic"}`,
		`{"credential_auth_mode":"api_key"}`,
		`{"credential_status":"healthy"}`,
		`{"disk-usage-pct":42}`, // 含 sk- 子串,前缀锚定不咬
	} {
		if ContainsForbiddenRawData([]byte(probe)) {
			t.Errorf("合法字段名被 key 扫描误杀: %s", probe)
		}
	}
	// 反向:裸凭证 token 作 map key 仍必须被前缀锚定网住。
	for _, probe := range []string{
		`{"eyJhbGciOiJIUzI1NiJ9":"x"}`,        // JWT 作 key
		`{"sk-ant-api03-fakekey":"x"}`,        // Anthropic key 作 key
		`{"AIzaFAKEFAKEFAKEFAKE":"x"}`,        // Google key 作 key
	} {
		if !ContainsForbiddenRawData([]byte(probe)) {
			t.Errorf("裸凭证作 key 未被网住: %s", probe)
		}
	}
}

// 复审 S3-a 判别:eyj/aiza 曾是小写宽子串,误杀含 aiza 的人名/邮箱/地名与 base64 文本。
// 收窄为大小写敏感 eyJ/AIza 后,合法运维值不再被 fail-closed 抹掉,真凭证仍命中。
// 变异靶:把 eyJ/AIza 判定改回小写 "eyj"/"aiza" 子串扫 → faiza/Raiza/aizawl 全被误判 → 红。
func TestShortMarkersCaseSensitiveNoOverRedact(t *testing.T) {
	for _, probe := range []string{
		"faiza.hussain@example.com",
		"welcome Raiza",
		"aizawl-region",
		"abeyjxyz",
	} {
		if ContainsForbiddenRawData([]byte(probe)) {
			t.Errorf("合法值被短标记误杀: %q", probe)
		}
	}
	for _, probe := range []string{
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig", // 真 JWT
		"AIzaSyD-Example_Key_1234567890abcdef",       // 真 Google key
		"token=AIzaSyABCDEF",                          // 嵌入位
	} {
		if !ContainsForbiddenRawData([]byte(probe)) {
			t.Errorf("真凭证形态未命中: %q", probe)
		}
	}
}
