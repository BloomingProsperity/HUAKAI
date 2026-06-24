package codebudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBufferedUpstreamResponseLimitHasSingleSource(t *testing.T) {
	root := filepath.Join("..", "..")
	gatewayPath := filepath.Join(root, "internal", "gateway", "upstream_dispatcher_hcsf.go")
	gatewayHTTPPath := filepath.Join(root, "internal", "gatewayhttp", "chat_completions_handler.go")

	gatewaySource := readSourceForLimitGuard(t, gatewayPath)
	gatewayHTTPSource := readSourceForLimitGuard(t, gatewayHTTPPath)

	requireSourceSnippet(t, gatewayPath, gatewaySource, "const MaxBufferedUpstreamResponseBytes = 1 << 20")
	requireSourceSnippet(t, gatewayPath, gatewaySource, "const maxBufferedUpstreamResponseBytes = MaxBufferedUpstreamResponseBytes")
	requireSourceSnippet(t, gatewayHTTPPath, gatewayHTTPSource, "const maxRawBufferedUpstreamBodyBytes = gateway.MaxBufferedUpstreamResponseBytes")

	for _, forbidden := range []string{
		"const maxRawBufferedUpstreamBodyBytes = 1 << 20",
		"const maxRawBufferedUpstreamBodyBytes = 1048576",
	} {
		if strings.Contains(gatewayHTTPSource, forbidden) {
			t.Fatalf("%s 禁止重新硬编码 raw buffered 上限,应引用 gateway.MaxBufferedUpstreamResponseBytes", gatewayHTTPPath)
		}
	}
}

func readSourceForLimitGuard(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(raw)
}

func requireSourceSnippet(t *testing.T, path, source, snippet string) {
	t.Helper()
	if !strings.Contains(source, snippet) {
		t.Fatalf("%s 缺少片段 %q", path, snippet)
	}
}
