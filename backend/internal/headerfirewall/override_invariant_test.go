package headerfirewall

import (
	"context"
	"net/http"
	"testing"
)

// 本文件取证 HCSF(热路径配置安全围栏)默认翻转的**安全前提**:把响应头围栏接通真实上游流量
// 的前提,是运营者配置的 AllowOverride **永远掀不动内置 deny 名单**(否则翻默认会让 Set-Cookie/
// Authorization/CF-*/X-Amz-* 等敏感上游头泄露给客户端)。现有 firewall_test 只对 Set-Cookie 一个头
// 验证了这点,本表驱动测试把断言扩到**全部 16 条内置 deny 规则**(13 exact + 3 prefix),并补 nil
// settings 的 fail-closed 断言。纯测试、零生产改动。

// builtInDenySamples 对每条内置 deny 规则给一个真实样本头名。
// prefix 规则(CF-/X-Amz-/X-Amzn-)用一个真实会出现的具体头名(如 CF-Ray)以验证前缀匹配生效。
var builtInDenySamples = []struct {
	name   string
	header string
}{
	{"set-cookie", "Set-Cookie"},
	{"set-cookie2", "Set-Cookie2"},
	{"authorization", "Authorization"},
	{"proxy-authenticate", "Proxy-Authenticate"},
	{"proxy-authorization", "Proxy-Authorization"},
	{"www-authenticate", "WWW-Authenticate"},
	{"x-real-ip", "X-Real-IP"},
	{"x-forwarded-for", "X-Forwarded-For"},
	{"x-forwarded-host", "X-Forwarded-Host"},
	{"x-forwarded-proto", "X-Forwarded-Proto"},
	{"x-forwarded-port", "X-Forwarded-Port"},
	{"x-cloud-trace-context", "X-Cloud-Trace-Context"},
	{"server", "Server"},
	{"cf-prefix", "CF-Ray"},               // prefixRule("CF-")
	{"x-amz-prefix", "X-Amz-Cf-Id"},       // prefixRule("X-Amz-")
	{"x-amzn-prefix", "X-Amzn-RequestId"}, // prefixRule("X-Amzn-")
}

// 不变量:即使运营者把某内置 deny 头名同时塞进 extraDeny 和 AllowOverride 试图"解禁",
// FilterResponseHeaders 仍必须剥离它(内置 deny 在 allowOverride 之前无条件生效)。
// 变异:若 FilterResponseHeaders 让 allowOverride 能掀内置 deny(把第 71 行改成
// `denyBuiltIn(name) && !matchesDynamic(name, allowOverride)`),则对应样本头不再被剥离 → 本测试转红。
func TestAllowOverrideCannotResurrectAnyBuiltInDenyHeader(t *testing.T) {
	for _, c := range builtInDenySamples {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			h.Set(c.header, "leaked-secret-value")
			h.Set("Content-Type", "application/json") // 良性对照头,必须保留(防"全剥"的假绿)

			// 运营者把该敏感头名显式列进 extraDeny + AllowOverride,试图让它流到客户端。
			filtered := FilterResponseHeaders(h, []string{c.header}, []string{c.header})

			if got := filtered.Get(c.header); got != "" {
				t.Fatalf("内置 deny 头 %q 即使被显式 allowOverride 也必须剥离,实得 %q", c.header, got)
			}
			if filtered.Get("Content-Type") != "application/json" {
				t.Fatalf("良性头 Content-Type 被误删(过度剥离)")
			}
		})
	}
}

// fail-closed:settings=nil → Policy{} 空表 → 只剩内置 deny 兜底。
// 运营配置整体缺失时绝不能放行敏感上游头。变异:若 PolicyFromPlatformSettings(nil) 返回非空
// 或 FilterResponseHeaders 漏掉内置 deny,则 Authorization 泄露 → 转红。
func TestPolicyFromNilSettingsIsFailClosed(t *testing.T) {
	policy := PolicyFromPlatformSettings(context.Background(), nil)
	if len(policy.ExtraDeny) != 0 || len(policy.AllowOverride) != 0 {
		t.Fatalf("nil settings 必须得空 Policy(fail-closed),实得 %+v", policy)
	}

	h := http.Header{}
	h.Set("Authorization", "Bearer secret")
	h.Set("Set-Cookie", "session=x")
	h.Set("Content-Type", "application/json")
	filtered := FilterResponseHeaders(h, policy.ExtraDeny, policy.AllowOverride)

	if filtered.Get("Authorization") != "" {
		t.Fatal("nil settings 下 Authorization 仍必须被内置 deny 剥离")
	}
	if filtered.Get("Set-Cookie") != "" {
		t.Fatal("nil settings 下 Set-Cookie 仍必须被内置 deny 剥离")
	}
	if filtered.Get("Content-Type") != "application/json" {
		t.Fatal("良性头 Content-Type 应保留")
	}
}
