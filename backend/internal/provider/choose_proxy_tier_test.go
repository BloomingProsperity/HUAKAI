package provider

import (
	"errors"
	"testing"
)

// PROXY-03:优先级为 account > tenant-default > direct,且在每一个
// 已绑定但不健康的层级都要 fail-closed(已配置但损坏的代理绝不能
// 静默回退到更低层级或直连——那会泄露该账号真实出口 IP,破坏 IP 隔离)。
func TestChooseProxyTier(t *testing.T) {
	// account 已绑定 + 健康 -> account 层级
	f, use, err := chooseProxyTier(proxyRow{hasProxyBound: true, proxyIsHealthy: true, protocol: "http", host: "acct", port: 1})
	if err != nil || !use || f.host != "acct" {
		t.Fatalf("account healthy: use=%v host=%q err=%v", use, f.host, err)
	}

	// account 已绑定 + 不健康 -> fail-closed,即便存在一个健康的 default
	_, use, err = chooseProxyTier(proxyRow{
		hasProxyBound: true, proxyIsHealthy: false,
		hasDefaultBound: true, defaultIsHealthy: true, dhost: "def",
	})
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("account bound-unhealthy MUST fail-closed (not fall to default): use=%v err=%v", use, err)
	}

	// 无 account 代理 + default 健康 -> default 层级
	f, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: true, dprotocol: "http", dhost: "def", dport: 2})
	if err != nil || !use || f.host != "def" {
		t.Fatalf("default healthy: use=%v host=%q err=%v", use, f.host, err)
	}

	// 无 account 代理 + default 不健康 -> fail-closed,而非直连
	_, use, err = chooseProxyTier(proxyRow{hasDefaultBound: true, defaultIsHealthy: false})
	if !errors.Is(err, ErrProxyUnhealthy) || use {
		t.Fatalf("default bound-unhealthy MUST fail-closed (not direct): use=%v err=%v", use, err)
	}

	// 两者都没有 -> 直连
	_, use, err = chooseProxyTier(proxyRow{})
	if err != nil || use {
		t.Fatalf("no proxy bound -> direct: use=%v err=%v", use, err)
	}

	// account 健康时优先于健康的 default
	f, use, _ = chooseProxyTier(proxyRow{
		hasProxyBound: true, proxyIsHealthy: true, host: "acct",
		hasDefaultBound: true, defaultIsHealthy: true, dhost: "def",
	})
	if !use || f.host != "acct" {
		t.Fatalf("account tier must win over tenant-default, got host=%q", f.host)
	}
}
