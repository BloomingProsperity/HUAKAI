package accountbundle

import (
	"testing"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestProxyRefBindsSourceProxyIdentity(t *testing.T) {
	username := "operator"
	first := admindb.GetProxyRow{
		ID: 1, Protocol: "HTTP", Host: "proxy.example.test", Port: 8080, AuthUsername: &username,
	}
	second := first
	second.ID = 2

	firstRef := proxyRef(first)
	if firstRef == proxyRef(second) {
		t.Fatal("不同源代理即使公开连接配置相同，也不得共享迁移引用")
	}
	if firstRef != proxyRef(first) {
		t.Fatal("同一源代理的迁移引用必须稳定")
	}
}
