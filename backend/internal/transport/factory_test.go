package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

func TestFactory_For_StandardDefault(t *testing.T) {
	f := NewFactory()
	rt, err := f.For(ProviderOpenAI, TransportModeStandard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// standard 未注入时不能直接复用 http.DefaultTransport：DefaultTransport
	// 的 Proxy 默认走 ProxyFromEnvironment，会让 Docker HTTP_PROXY env 劫持
	// 账号绑定代理的 IP 隔离设计（账号级代理只能由 dispatcher.applyProxy 决定）。
	if rt == http.DefaultTransport {
		t.Fatalf("standardRoundTripper 不应直接返回 http.DefaultTransport (会被 env proxy 劫持)")
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("standard fallback 应是 *http.Transport, got %T", rt)
	}
	if tr.Proxy != nil {
		t.Fatalf("standard fallback Proxy 必须是 nil, 否则 env proxy 仍生效")
	}
}

// TestFactory_StandardRoundTripperIgnoresEnvProxy 守 P1-A 修复：
// 设 HTTP_PROXY env 后 standard fallback transport 仍必须 Proxy=nil。
// Mutation：把 standardRoundTripper 改回直接 return http.DefaultTransport
// 时本用例应红（DefaultTransport.Proxy = ProxyFromEnvironment != nil）。
func TestFactory_StandardRoundTripperIgnoresEnvProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://bad-proxy.invalid:9999")
	t.Setenv("HTTPS_PROXY", "http://bad-proxy.invalid:9999")
	t.Setenv("ALL_PROXY", "http://bad-proxy.invalid:9999")

	f := NewFactory()
	rt := f.standardRoundTripper()
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("standard fallback 应是 *http.Transport, got %T", rt)
	}
	if tr.Proxy != nil {
		t.Fatalf("HTTP_PROXY env 存在时 standard fallback.Proxy 必须为 nil 才能保账号隔离, got %T", tr.Proxy)
	}

	// 同一 Factory 二次调用应单例化（connection pool 复用）。
	rt2 := f.standardRoundTripper()
	if rt != rt2 {
		t.Fatalf("standard fallback transport 必须单例化，否则 connection pool 失效")
	}
}

// 默认 mimicry template 缺失 → fail-closed error,
// 不回退 Anthropic Phase A 默认模板。
func TestFactory_For_MimicryWithoutRegistry_FailClosedByDefault(t *testing.T) {
	f := NewFactory()
	_, err := f.For(ProviderOpenAI, TransportModeMimicryChatGPT)
	if err == nil {
		t.Fatal("template registry 未注入应 fail-closed, 不应回退 Phase A")
	}
}

func TestFactory_For_MimicryWithoutRegistryUsesPhaseADefault(t *testing.T) {
	// Phase A fallback 现在默认 fail-closed, 需 opt-in env
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	f := NewFactory()
	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("mimicry mode 应返回 uTLS RoundTripper: %v", err)
	}
	if rt == nil {
		t.Fatal("mimicry mode 返回 nil RoundTripper")
	}
	if _, ok := rt.(*http.Transport); ok {
		t.Fatal("mimicry RoundTripper 不应暴露 *http.Transport，避免 proxy 路径旁路 uTLS")
	}
	rt2, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	if rt != rt2 {
		t.Fatal("默认 mimicry RoundTripper 应复用，避免每次请求新建连接池")
	}
}

func TestFactory_For_MimicryEmptySidecarSocketKeepsUtlsCompatibility(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	f := NewFactory()
	f.SidecarSocketPath = ""

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("empty sidecar socket should preserve uTLS compatibility path: %v", err)
	}
	if rt == nil {
		t.Fatal("mimicry mode returned nil RoundTripper")
	}
	if _, ok := rt.(*http.Transport); ok {
		t.Fatal("empty sidecar socket should use uTLS wrapper, not sidecar *http.Transport")
	}
}

func TestFactory_For_MimicrySidecarMissingSocketFailsClosed(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	missingSocket := "/tmp/huakai-missing-sidecar.sock"
	f := NewFactory()
	f.SidecarSocketPath = missingSocket
	f.sidecarProbe = func(_ context.Context, socketPath string, mode mimicry.TransportMode) error {
		if socketPath != missingSocket {
			t.Fatalf("probe socketPath=%q want %q", socketPath, missingSocket)
		}
		if mode != mimicry.ModeMimicryClaudeCode {
			t.Fatalf("probe mode=%q want %q", mode, mimicry.ModeMimicryClaudeCode)
		}
		return errors.New("mimicry sidecar: dial unix socket " + socketPath + ": missing sidecar socket")
	}

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)

	if err == nil {
		t.Fatalf("missing sidecar socket must fail closed instead of falling back to rt=%T", rt)
	}
	if !strings.Contains(err.Error(), "sidecar") || !strings.Contains(err.Error(), missingSocket) {
		t.Fatalf("error should identify sidecar socket failure, got %v", err)
	}
}

func TestFactory_For_MimicrySidecarSocketUsesSidecarRoundTripper(t *testing.T) {
	socketPath := "/tmp/huakai-tls-sidecar.sock"
	probeCalls := 0
	probeSawDeadline := false
	f := NewFactory()
	f.SidecarSocketPath = socketPath
	f.sidecarProbeTimeout = 500 * time.Millisecond
	f.sidecarProbe = func(ctx context.Context, gotSocketPath string, mode mimicry.TransportMode) error {
		probeCalls++
		if gotSocketPath != socketPath {
			t.Fatalf("probe socketPath=%q want %q", gotSocketPath, socketPath)
		}
		if mode != mimicry.ModeMimicryClaudeCode {
			t.Fatalf("probe mode=%q want %q", mode, mimicry.ModeMimicryClaudeCode)
		}
		_, probeSawDeadline = ctx.Deadline()
		return nil
	}

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("valid fake sidecar should produce sidecar RoundTripper: %v", err)
	}
	if _, ok := rt.(*http.Transport); !ok {
		t.Fatalf("sidecar branch should return *http.Transport from NewSidecarRoundTripperForMode, got %T", rt)
	}
	if probeCalls != 1 {
		t.Fatalf("probe calls=%d want 1", probeCalls)
	}
	if !probeSawDeadline {
		t.Fatal("sidecar probe should receive a bounded context")
	}

	rt2, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("cached sidecar RoundTripper should not re-probe: %v", err)
	}
	if rt != rt2 {
		t.Fatal("sidecar RoundTripper should be cached per mode")
	}
	if probeCalls != 1 {
		t.Fatalf("cached sidecar RoundTripper should not re-probe; calls=%d", probeCalls)
	}
}

func TestFactory_For_MimicrySidecarNoAckTimesOutFailClosed(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	f := NewFactory()
	f.SidecarSocketPath = "/tmp/huakai-nonresponsive-sidecar.sock"
	f.sidecarProbeTimeout = 100 * time.Millisecond
	f.sidecarProbe = func(ctx context.Context, _ string, _ mimicry.TransportMode) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(750 * time.Millisecond):
			return errors.New("probe context did not time out")
		}
	}
	started := time.Now()

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)

	if err == nil {
		t.Fatalf("nonresponsive sidecar must fail closed instead of falling back to rt=%T", rt)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("sidecar probe should honor bounded timeout; elapsed=%s err=%v", elapsed, err)
	}
	if !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("error should identify sidecar failure, got %v", err)
	}
}

func TestFactory_For_MimicryStubUsesPhaseADefault(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	registry := mimicry.NewTemplateRegistry()
	stub := &mimicry.ClientHelloTemplate{
		ModeName:    "openai_codex_cli",
		CollectedAt: "2026-05-14T00:00:00Z",
		TargetHost:  "chatgpt.com",
	}
	if err := registry.Register(mimicry.TransportMode(TransportModeMimicryChatGPT), stub); err != nil {
		t.Fatal(err)
	}
	rt, err := NewFactory(registry).For(ProviderOpenAI, TransportModeMimicryChatGPT)
	if err != nil {
		t.Fatalf("stub template 应使用 Phase A 默认 uTLS RoundTripper: %v", err)
	}
	if rt == http.DefaultTransport {
		t.Fatal("stub template 不应回落 standard，应使用 Phase A 默认模板")
	}
}

func TestFactory_For_MimicryUsesUtlsClientHello(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_PHASE_A_FALLBACK", "true")
	helloCh := make(chan []byte, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("本地 loopback 监听不可用，跳过需要 mock TLS server 的 transport 测试: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		header := make([]byte, 5)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		body := make([]byte, int(header[3])<<8|int(header[4]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		helloCh <- append(header, body...)
	}()

	rt, err := NewFactory().For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: rt, Timeout: 2 * time.Second}
	_, _ = client.Get("https://" + ln.Addr().String() + "/")

	select {
	case hello := <-helloCh:
		if !bytes.HasPrefix(hello, []byte{0x16, 0x03}) {
			t.Fatalf("不是 TLS handshake record: %x", hello[:min(5, len(hello))])
		}
		if !bytes.Contains(hello, []byte{0xfe, 0x0d, 0x00, 0xba}) {
			t.Fatalf("ClientHello 未包含 Phase A ECH GREASE 扩展，疑似未走 uTLS spec")
		}
		if !bytes.Contains(hello, []byte{0x13, 0x01, 0x13, 0x02, 0x13, 0x03}) {
			t.Fatalf("ClientHello 未包含模板 TLS 1.3 cipher suite 前缀")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mock TLS server 未收到 ClientHello")
	}
}

func TestFactory_For_DiagnosticsStillFailLoud(t *testing.T) {
	_, err := NewFactory().For(ProviderOpenAI, TransportModeDiagnosticsOnly)
	if !errors.Is(err, ErrTransportNotImplemented) {
		t.Fatalf("diagnostics 未注入应继续 fail-loud，得到 %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
