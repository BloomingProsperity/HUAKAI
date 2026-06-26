package headerfirewall

import (
	"net/http"
	"testing"
)

// TestStripHopByHopRequestHeaders 守护 SEC-098:逐跳(hop-by-hop)请求头
//(以及任何在 Connection 中点名的头)必须在转发前被移除,而普通的端到端(end-to-end)
// 头则要保留。变异:跳过 hopByHopRequestHeaders 循环里的 h.Del -> Keep-Alive/Upgrade
// 存活下来 -> 本测试失败。
func TestStripHopByHopRequestHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Upgrade", "websocket")
	h.Set("Transfer-Encoding", "chunked")
	h.Set("Connection", "X-Custom-Hop")
	h.Set("X-Custom-Hop", "drop-me")
	h.Set("Authorization", "Bearer keep")
	h.Set("Content-Type", "application/json")

	StripHopByHopRequestHeaders(h)

	for _, name := range []string{"Keep-Alive", "Upgrade", "Transfer-Encoding", "Connection", "X-Custom-Hop"} {
		if got := h.Get(name); got != "" {
			t.Fatalf("hop-by-hop header %q=%q want stripped", name, got)
		}
	}
	if got := h.Get("Authorization"); got != "Bearer keep" {
		t.Fatalf("Authorization=%q want preserved (end-to-end header wrongly stripped)", got)
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want preserved", got)
	}
}
