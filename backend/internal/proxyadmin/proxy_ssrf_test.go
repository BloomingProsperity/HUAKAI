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

func TestProxyHostSafe_DefaultDeniesPrivateAndAllowsPublicHosts(t *testing.T) {
	for _, h := range []string{"10.0.0.9", "192.168.1.1", "172.16.0.1", "fd00::1"} {
		if proxyHostSafe(h) {
			t.Errorf("未获部署者授权的私网代理 %q 必须被拒绝", h)
		}
	}
	allowed := []string{
		"proxy.internal",    // 域名在实际拨号时解析并复核
		"proxy.example.com", // 公网主机名
		"1.1.1.1",           // 公网 v4
	}
	for _, h := range allowed {
		if !proxyHostSafe(h) {
			t.Errorf("host %q 是合法代理目标(私网/内网名/公网),却被误判不安全", h)
		}
	}
}

func TestProxyHostSafe_AllowsOnlyExplicitPrivateProxy(t *testing.T) {
	allowPrivateProxyHosts(t, "10.0.0.9,fd00::1")
	for _, host := range []string{"10.0.0.9", "fd00::1"} {
		if !proxyHostSafe(host) {
			t.Fatalf("部署者精确授权的私网代理 %q 应被允许", host)
		}
	}
	if proxyHostSafe("10.0.0.10") {
		t.Fatal("相邻但未授权的私网地址不得被放行")
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

	// 私网代理默认拒绝；部署者精确授权后才允许写入。
	ok := base
	ok.Host = "10.0.0.9"
	if err := validateCreate(ok); !errors.Is(err, ErrUnsafeHost) {
		t.Fatalf("未授权私网代理必须拒绝,got %v", err)
	}
	allowPrivateProxyHosts(t, "10.0.0.9")
	if err := validateCreate(ok); err != nil {
		t.Fatalf("部署者授权后的私网代理不应被拒,got %v", err)
	}
}
