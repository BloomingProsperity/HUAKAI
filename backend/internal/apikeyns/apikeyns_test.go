package apikeyns

import "testing"

// TestBaseDefaultAndOverride 守护:env 空=默认 hk;合法值生效;非法值回落默认。
// 变异证伪:把 sanitizeBase 改成恒 return defaultBase → 自定义用例红;
// 把 validBase 上限去掉 → 超长用例(应回落 hk)红。
func TestBaseDefaultAndOverride(t *testing.T) {
	cases := []struct {
		set  bool
		val  string
		want string
	}{
		{false, "", "hk"},       // 未设=默认
		{true, "", "hk"},        // 空=默认
		{true, "sk", "sk"},      // 合法覆盖
		{true, "SK", "sk"},      // 大写归一为小写
		{true, "ab12", "ab12"},  // 字母数字混合
		{true, "x_y", "hk"},     // 含下划线非法→回落
		{true, "has-dash", "hk"},// 含连字符非法→回落
		{true, "abc123", "abc123"}, // 6 字符合法(上限)
		{true, "abc1234", "hk"},    // 7 字符 >6 非法→回落(守上限收紧到 6)
		{true, "toolongpfx", "hk"}, // 远超上限→回落
		{true, "好", "hk"},       // 非 ASCII→回落
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("HUAKAI_API_KEY_PREFIX", c.val)
		} else {
			t.Setenv("HUAKAI_API_KEY_PREFIX", "")
		}
		if got := Base(); got != c.want {
			t.Errorf("Base(val=%q)=%q want %q", c.val, got, c.want)
		}
	}
}

// TestPrefixesTrackBase 守护:live/test 前缀随 base 走,且签发与校验同源。
// 变异证伪:把 LivePrefix 写死 "hk_live_" → sk 用例红。
func TestPrefixesTrackBase(t *testing.T) {
	t.Setenv("HUAKAI_API_KEY_PREFIX", "sk")
	if LivePrefix() != "sk_live_" || TestPrefix() != "sk_test_" {
		t.Fatalf("base=sk: live=%q test=%q want sk_live_/sk_test_", LivePrefix(), TestPrefix())
	}
	t.Setenv("HUAKAI_API_KEY_PREFIX", "")
	if LivePrefix() != "hk_live_" || TestPrefix() != "hk_test_" {
		t.Fatalf("默认: live=%q test=%q want hk_live_/hk_test_", LivePrefix(), TestPrefix())
	}
}

// TestValidCustomerFormat 守护入站过滤随 base 走:配置 sk 后只认 sk_ 客户前缀,
// 不再认旧 hk_ 默认(否则签发 sk 校验认 hk 就漂移)。变异证伪:把过滤写死 hk_ →
// "认 sk 拒 hk" 断言红。
func TestValidCustomerFormat(t *testing.T) {
	t.Setenv("HUAKAI_API_KEY_PREFIX", "sk")
	if !ValidCustomerFormat("sk_live_abcdef") || !ValidCustomerFormat("sk_test_abcdef") {
		t.Fatal("base=sk 应认 sk_live_/sk_test_")
	}
	if ValidCustomerFormat("hk_live_abcdef") {
		t.Fatal("base=sk 不应再认旧 hk_live_(签发/校验须同源)")
	}
	if ValidCustomerFormat("randomtoken") || ValidCustomerFormat("sk_admin_x") {
		t.Fatal("异源/admin 前缀不应被当客户 token 放过")
	}
}

// TestConfiguredBaseError 守护启动期 fail-loud:非法非空值报错,空/合法不报。
// 变异证伪:让 ConfiguredBaseError 恒返 nil → 非法用例红。
func TestConfiguredBaseError(t *testing.T) {
	t.Setenv("HUAKAI_API_KEY_PREFIX", "")
	if err := ConfiguredBaseError(); err != nil {
		t.Fatalf("空值应 nil,got %v", err)
	}
	t.Setenv("HUAKAI_API_KEY_PREFIX", "sk")
	if err := ConfiguredBaseError(); err != nil {
		t.Fatalf("合法值应 nil,got %v", err)
	}
	t.Setenv("HUAKAI_API_KEY_PREFIX", "x_y")
	if err := ConfiguredBaseError(); err == nil {
		t.Fatal("非法值应 fail-loud 报错")
	}
}

// TestAdminPrefixFixed 守护 admin 前缀不可配(权限边界)。
func TestAdminPrefixFixed(t *testing.T) {
	if AdminPrefix != "hk_admin_" {
		t.Fatalf("AdminPrefix=%q want hk_admin_(不可配)", AdminPrefix)
	}
}

// TestIssuedPrefixIsAccepted 守护核心不变量:签发用的前缀(LivePrefix/TestPrefix)
// 出来的 token 必须被入站过滤(ValidCustomerFormat)接受——这是 token 前缀可配的
// 头号坑(签发用 A、校验认 B 则签出的 key 自己登录不过)。单一真相源使其恒成立。
// 变异证伪:让 ValidCustomerFormat 写死 hk_ 而 LivePrefix 随 base → base=sk 时
// 签发的 sk_live_ token 不被接受,断言红。
func TestIssuedPrefixIsAccepted(t *testing.T) {
	for _, base := range []string{"", "sk", "myco12"} {
		t.Setenv("HUAKAI_API_KEY_PREFIX", base)
		if !ValidCustomerFormat(LivePrefix()+"0123456789") || !ValidCustomerFormat(TestPrefix()+"0123456789") {
			t.Fatalf("base=%q:签发前缀 token 必须被入站过滤接受(签发/校验同源)", base)
		}
	}
}
