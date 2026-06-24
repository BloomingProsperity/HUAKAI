package codebudget

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMimicryUTLSKeepsH1OnlyDefault(t *testing.T) {
	path := filepath.Join("..", "..", "internal", "transport", "mimicry", "utls_dialer.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	source := string(raw)
	requireMimicrySnippet(t, source, `return os.Getenv("HUAKAI_TRANSPORT_FORCE_H1") != "false"`)
	requireMimicrySnippet(t, source, `ForceAttemptHTTP2:     false`)
	requireMimicrySnippet(t, source, `cfg.NextProtos = []string{"http/1.1"}`)
	requireMimicrySnippet(t, source, `alpn.AlpnProtocols = []string{"http/1.1"}`)

	if strings.Contains(source, "ForceAttemptHTTP2:     true") || strings.Contains(source, "ForceAttemptHTTP2: true") {
		t.Fatal("Go uTLS 出口禁止重新打开 http.Transport.ForceAttemptHTTP2")
	}
}

func requireMimicrySnippet(t *testing.T, source, snippet string) {
	t.Helper()
	if !strings.Contains(source, snippet) {
		t.Fatalf("utls_dialer.go 缺少关键片段 %q", snippet)
	}
}
