package proxyadmin

import (
	"errors"
	"testing"
)

// TestProxyHostSafe_BlocksNeverLegitTargets 守护:云 metadata / loopback /
// link-local / unspecified / multicast 这些【绝无合法代理用途】的目标被判不安全。
// 变异证伪:把 proxyHostSafe 改成恒 return true,或删掉任一 Is* 判定,对应行转红。
// 关键区分:169.254.169.254 是 link-local(覆盖 AWS/GCP/Azure metadata IP),
// 必须被 IsLinkLocalUnicast 挡住,而不是靠单独硬编码。
func TestProxyHostSafe_BlocksNeverLegitTargets(t *testing.T) {
	blocked := []string{
		"169.254.169.254",          // 云 metadata(link-local)
		"169.254.170.2",            // ECS task metadata(link-local)
		"fd00:ec2::254",            // AWS IMDS-over-IPv6(ULA 私网段,靠 blockedMetadataIPs 特判)
		"[fd00:ec2::254]",          // 带括号同上
		"127.0.0.1",                // loopback v4
		"::1",                      // loopback v6
		"[::1]",                    // 带方括号的 v6 loopback
		"0.0.0.0",                  // unspecified v4
		"::",                       // unspecified v6
		"224.0.0.1",                // multicast v4
		"ff02::1",                  // multicast v6
		"fe80::1",                  // link-local v6
		"localhost",                // 本机主机名
		"metadata.google.internal", // GCP metadata 主机名
		"instance-data",            // EC2 metadata 主机名
		"metadata",                 // 裸 metadata 主机名
	}
	for _, h := range blocked {
		if proxyHostSafe(h) {
			t.Errorf("host %q 应判为不安全(metadata/loopback/link-local 类),却放行了", h)
		}
	}
}

// TestProxyHostSafe_AllowsLegitProxies 守护反向不变量:RFC1918 私网与 .internal
// 类主机名是【合法企业/内网出口代理】,必须放行,封死会误伤正常配置。
// 变异证伪:若把 IsPrivate 也加进阻断面(模仿 passthrough endpoint 守卫),
// 10.0.0.9 / 192.168.x / proxy.internal 行转红。
func TestProxyHostSafe_AllowsLegitProxies(t *testing.T) {
	allowed := []string{
		"10.0.0.9",         // 企业私网代理(现有集成测试 fixture)
		"192.168.1.1",      // 私网
		"172.16.0.1",       // 私网
		"fd00::1",          // ULA 私网 v6
		"proxy.internal",   // 内网主机名(现有集成测试 fixture)
		"proxy.example.com", // 公网主机名
		"1.1.1.1",          // 公网 v4
	}
	for _, h := range allowed {
		if !proxyHostSafe(h) {
			t.Errorf("host %q 是合法代理目标(私网/内网名/公网),却被误判不安全", h)
		}
	}
}

// TestValidateCreateRejectsUnsafeHost 守护守卫真正接进写路径:metadata host
// 经 validateCreate 必返 ErrUnsafeHost(而非 nil 或其它 sentinel)。
// 变异证伪:删掉 validateCommon 里的 `if !proxyHostSafe(host)` 分支,则返 nil,转红。
func TestValidateCreateRejectsUnsafeHost(t *testing.T) {
	base := CreateInput{TenantID: 7, Name: "p", Protocol: "http", Port: 3128, Status: "active"}

	bad := base
	bad.Host = "169.254.169.254"
	if err := validateCreate(bad); !errors.Is(err, ErrUnsafeHost) {
		t.Fatalf("metadata host 应返 ErrUnsafeHost,got %v", err)
	}

	// 反向:合法私网代理 host 不被 SSRF 门拦下(应过校验)。
	ok := base
	ok.Host = "10.0.0.9"
	if err := validateCreate(ok); err != nil {
		t.Fatalf("合法私网代理 host 不应被拒,got %v", err)
	}
}
