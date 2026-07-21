package anthropicoauth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// 本文件守护本包仅存的活体部件:只走 Rust sidecar 的 HTTP client 入口
// (DefaultHTTPClient / HTTPClient / NewHTTPClient)。它们被生产 wiring
// (cmd/gateway/wiring.go 的 NewHTTPClient)与刷新链(credentialworker
// adapters/anthropic.go 的 DefaultHTTPClient)依赖。安全语义:sidecar 不可用
// 时必须 fail-closed(装显式失败 transport),绝不静默退回标准库 TLS——否则
// Claude OAuth 出站会以裸指纹打上游,属指纹泄露事故。

// sidecar 不可用时,DefaultHTTPClient 必须返回带显式失败 transport 的 client,
// 且绝不退化成 http.DefaultClient / http.DefaultTransport。
// 判别 mutation:把 transport.go 的 HTTPClient 出错分支改成 return
// http.DefaultClient,本测试立即变红。
func TestDefaultHTTPClientFailsClosedWithoutRustSidecar(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET", "/home/ubuntu/.cache/huakai-codex/missing-sidecar.sock")
	client := DefaultHTTPClient()
	if client == nil || client.Transport == nil {
		t.Fatal("DefaultHTTPClient 必须安装显式失败 transport")
	}
	if client == http.DefaultClient || client.Transport == http.DefaultTransport {
		t.Fatal("DefaultHTTPClient 不得静默使用标准库 HTTP")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.anthropic.com/v1/oauth/token", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	if _, err = client.Do(req); err == nil || !strings.Contains(err.Error(), "Rust mimicry transport unavailable") {
		t.Fatalf("client.Do err=%v,期望明确报告 Rust sidecar 不可用", err)
	}
}

// HTTPClient 在 sidecar 不可用时必须打出带 reason_class 的结构化告警日志,
// 并仍返回携带确定错误的 fail-closed client(非 http.DefaultClient)。
// 判别 mutation:删掉 transport.go 里 HTTPClient 的 slog.Error 调用,本测试
// 因日志缺失立即变红。
func TestHTTPClientMissingRustSidecarLogsAndFailsClosed(t *testing.T) {
	var logs bytes.Buffer
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(oldDefault)

	factory := transport.NewFactory()
	factory.SidecarSocketPath = "/home/ubuntu/.cache/huakai-codex/missing-sidecar.sock"
	client := HTTPClient(factory)
	if client == nil {
		t.Fatal("失败关闭入口仍应返回携带确定错误的 client")
	}
	if client == http.DefaultClient {
		t.Fatal("sidecar 不可用时不得返回 http.DefaultClient")
	}
	got := logs.String()
	for _, want := range []string{
		"anthropicoauth Rust transport unavailable",
		"reason_class=mimicry_transport_unavailable",
		mimicry.SidecarProfileAnthropicCLIMimicryV1,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("log %q missing %q", got, want)
		}
	}
}
