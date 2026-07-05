package transport

import (
	"bytes"
	"context"
	"errors"
	"expvar"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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
// 不回退 Anthropic 默认模板。
func TestFactory_For_MimicryWithoutRegistry_FailClosedByDefault(t *testing.T) {
	f := NewFactory()
	_, err := f.For(ProviderOpenAI, TransportModeMimicryChatGPT)
	if err == nil {
		t.Fatal("template registry 未注入应 fail-closed, 不应回退 Phase A")
	}
}

func TestFactory_For_MimicryWithoutRegistryUsesPhaseADefault(t *testing.T) {
	// legacy fallback 默认 fail-closed, 需 opt-in env
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
	f.sidecarProbe = func(_ context.Context, socketPath string, mode mimicry.TransportMode, profileID string) error {
		if socketPath != missingSocket {
			t.Fatalf("probe socketPath=%q want %q", socketPath, missingSocket)
		}
		if mode != mimicry.ModeMimicryClaudeCode {
			t.Fatalf("probe mode=%q want %q", mode, mimicry.ModeMimicryClaudeCode)
		}
		if profileID != mimicry.SidecarProfileAnthropicCLIMimicryV1 {
			t.Fatalf("probe profileID=%q want %q", profileID, mimicry.SidecarProfileAnthropicCLIMimicryV1)
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
	if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarUnavailable {
		t.Fatalf("error class=%q want %q", got, TransportErrorClassSidecarUnavailable)
	}
}

func TestFactory_For_MimicrySidecarSocketUsesSidecarRoundTripper(t *testing.T) {
	socketPath := "/tmp/huakai-tls-sidecar.sock"
	probeCalls := 0
	probeSawDeadline := false
	f := NewFactory()
	f.SidecarSocketPath = socketPath
	f.sidecarProbeTimeout = 500 * time.Millisecond
	f.sidecarProbe = func(ctx context.Context, gotSocketPath string, mode mimicry.TransportMode, profileID string) error {
		probeCalls++
		if gotSocketPath != socketPath {
			t.Fatalf("probe socketPath=%q want %q", gotSocketPath, socketPath)
		}
		if mode != mimicry.ModeMimicryClaudeCode {
			t.Fatalf("probe mode=%q want %q", mode, mimicry.ModeMimicryClaudeCode)
		}
		if profileID != mimicry.SidecarProfileAnthropicCLIMimicryV1 {
			t.Fatalf("probe profileID=%q want %q", profileID, mimicry.SidecarProfileAnthropicCLIMimicryV1)
		}
		_, probeSawDeadline = ctx.Deadline()
		return nil
	}

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("valid fake sidecar should produce sidecar RoundTripper: %v", err)
	}
	// sidecar 分支返回带 SidecarProfileID() 标记的 wrapper(内嵌 *http.Transport),转发层
	// (gateway.applyTLSProfile)据此短路 per-account DB profile 替换。这条断言是 ②-2b 的
	// 真实接线守卫:必须验【生产构造器的输出真满足标记接口】,而非手搓 marker 的假绿。
	if _, ok := rt.(interface{ SidecarProfileID() string }); !ok {
		t.Fatalf("sidecar branch should return a sidecar-marked RoundTripper (impl SidecarProfileID), got %T", rt)
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
	f.sidecarProbe = func(ctx context.Context, _ string, _ mimicry.TransportMode, _ string) error {
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
	if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarUnavailable {
		t.Fatalf("error class=%q want %q", got, TransportErrorClassSidecarUnavailable)
	}
}

func TestFactory_For_MimicrySidecarFailureCacheCoalescesConcurrentProbeTimeouts(t *testing.T) {
	f := NewFactory()
	f.SidecarSocketPath = "/tmp/huakai-stuck-sidecar.sock"
	f.sidecarProbeTimeout = 30 * time.Millisecond
	f.sidecarFailureCacheTTL = time.Second
	var probeCalls atomic.Int64
	f.sidecarProbe = func(ctx context.Context, _ string, _ mimicry.TransportMode, _ string) error {
		probeCalls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}

	const concurrent = 10
	start := make(chan struct{})
	errs := make(chan error, concurrent)
	var wg sync.WaitGroup
	wg.Add(concurrent)
	started := time.Now()
	for i := 0; i < concurrent; i++ {
		go func() {
			defer wg.Done()
			<-start
			rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
			if err == nil {
				errs <- fmt.Errorf("sidecar 挂起时不应返回 rt=%T", rt)
				return
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err == nil {
			t.Fatal("sidecar 挂起时所有并发请求都应失败")
		}
		if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarUnavailable {
			t.Fatalf("error class=%q want %q", got, TransportErrorClassSidecarUnavailable)
		}
	}
	if got := probeCalls.Load(); got != 1 {
		t.Fatalf("挂起 sidecar 的并发探测次数=%d want 1；失败负缓存未命中会导致锁内串行探测", got)
	}
	if elapsed := time.Since(started); elapsed >= f.sidecarProbeTimeout*time.Duration(concurrent)/2 {
		t.Fatalf("并发挂起 sidecar 总耗时=%s，接近线性串行探测；timeout=%s N=%d", elapsed, f.sidecarProbeTimeout, concurrent)
	}
}

func TestFactory_For_MimicrySidecarUnavailableFallbackFlagOffDoesNotDegrade(t *testing.T) {
	native := &stubRoundTripper{}
	f := NewFactory()
	f.SetMimicry(native)
	f.SidecarSocketPath = "/tmp/huakai-sidecar-down.sock"
	f.SidecarFallbackEnabled = false
	f.sidecarProbe = func(context.Context, string, mimicry.TransportMode, string) error {
		return errors.New("connection refused")
	}

	// A2/S2-1:probe 阶段的出口失败必须计入拨号计数——这是默认 fail-closed 下 sidecar 宕机的
	// 主路径(DialTLS 从不运行)。connection refused 归 sidecar_unavailable → dial_fail 桶。
	beforeDialFail := egressDialFailCount()

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)

	if err == nil {
		t.Fatalf("sidecar unavailable with fallback off must fail closed, got rt=%T", rt)
	}
	if rt == native {
		t.Fatal("fallback off must not return injected Go-native mimicry transport")
	}
	if got := f.SidecarFallbackCount(); got != 0 {
		t.Fatalf("fallback metric=%d want 0 when fallback flag is off", got)
	}
	if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarUnavailable {
		t.Fatalf("error class=%q want %q", got, TransportErrorClassSidecarUnavailable)
	}
	// 判别性:删 sidecarRoundTripper probe 失败分支的 mimicry.RecordEgressProbeFailure → 增量 0 → 红。
	// 证 probe 期出口宕机不再是指标死账(补齐"出口成功率"分母的关联缺口)。
	if delta := egressDialFailCount() - beforeDialFail; delta != 1 {
		t.Fatalf("egress_sidecar_dial_total{dial_fail} 增量=%d 应为 1(probe 期 sidecar 宕机未计入拨号失败,出口成功率分母漏账)", delta)
	}
}

// egressDialFailCount 读 bridge 面 dial 计数的 dial_fail 桶(经 expvar 全局 map,跨包读)。
// probe 失败在 mimicry 包计数,这里用 expvar 名字读回,顺带证跨包 expvar 契约。
func egressDialFailCount() int64 {
	m, ok := expvar.Get("egress_sidecar_dial_total").(*expvar.Map)
	if !ok || m == nil {
		return 0
	}
	iv, ok := m.Get("dial_fail").(*expvar.Int)
	if !ok || iv == nil {
		return 0
	}
	return iv.Value()
}

func TestFactory_For_MimicrySidecarUnavailableFallbackFlagOnUsesNativeAndCountsMetric(t *testing.T) {
	native := &stubRoundTripper{}
	f := NewFactory()
	f.SetMimicry(native)
	f.SidecarSocketPath = "/tmp/huakai-sidecar-down.sock"
	f.SidecarFallbackEnabled = true
	f.sidecarProbe = func(context.Context, string, mimicry.TransportMode, string) error {
		return errors.New("connection refused")
	}

	// A2:抓 bridge 面 expvar 的增量。expvar 是进程全局、与其它测试共享,故取增量而非绝对值。
	// unavailable 类应 +1,与内存原子计数 SidecarFallbackCount 同步递增。
	beforeUnavailable := egressFallbackClassCount(TransportErrorClassSidecarUnavailable)

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)

	if err != nil {
		t.Fatalf("fallback flag on should return Go-native mimicry transport, got %v", err)
	}
	if rt != native {
		t.Fatalf("fallback rt=%T want injected native %T", rt, native)
	}
	if got := f.SidecarFallbackCount(); got != 1 {
		t.Fatalf("fallback metric=%d want 1", got)
	}
	// 判别性:删 recordSidecarFallback 里的 recordEgressFallbackMetric(class) → 此增量为 0 → 红。
	// 且 reason_class 必须落在 sidecar_unavailable 桶(probe connection refused),写错分类桶也红。
	if delta := egressFallbackClassCount(TransportErrorClassSidecarUnavailable) - beforeUnavailable; delta != 1 {
		t.Fatalf("egress_sidecar_fallback_total{sidecar_unavailable} 增量=%d 应为 1(降级事件未桥进 expvar,或写错 reason_class 桶)", delta)
	}
}

// egressFallbackClassCount 读 bridge 面 expvar 里某一 reason_class 的降级计数(同包可直接读
// 包级 var)。用于断言"内存原子计数"与"expvar 桥接面"在同一真实降级路径上同步 +1。
func egressFallbackClassCount(class TransportErrorClass) int64 {
	v := egressSidecarFallbackTotal.Get(string(class))
	iv, ok := v.(*expvar.Int)
	if !ok || iv == nil {
		return 0
	}
	return iv.Value()
}

func TestFactory_For_MimicrySidecarRuntimeFailureFallbacksToNative(t *testing.T) {
	native := &responseRoundTripper{statusCode: http.StatusNoContent}
	f := NewFactory()
	f.SetMimicry(native)
	f.SidecarSocketPath = "/tmp/huakai-sidecar-restarted.sock"
	f.SidecarFallbackEnabled = true
	f.sidecarByMode = map[TransportMode]http.RoundTripper{
		TransportModeMimicryClaudeCode: errorRoundTripper{err: errors.New("mimicry sidecar: dial unix socket /tmp/huakai-sidecar-restarted.sock: connection refused")},
	}

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := rt.RoundTrip(req)

	if err != nil {
		t.Fatalf("runtime sidecar failure should fallback to native RoundTripper: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("fallback response status=%d want %d", resp.StatusCode, http.StatusNoContent)
	}
	if native.calls != 1 {
		t.Fatalf("native fallback calls=%d want 1", native.calls)
	}
	if got := f.SidecarFallbackCount(); got != 1 {
		t.Fatalf("fallback metric=%d want 1", got)
	}
}

func TestFactory_For_MimicrySidecarUpstreamErrorDoesNotFallback(t *testing.T) {
	native := &responseRoundTripper{statusCode: http.StatusNoContent}
	f := NewFactory()
	f.SetMimicry(native)
	f.SidecarSocketPath = "/tmp/huakai-sidecar.sock"
	f.SidecarFallbackEnabled = true
	f.sidecarByMode = map[TransportMode]http.RoundTripper{
		TransportModeMimicryClaudeCode: errorRoundTripper{err: errors.New("mimicry sidecar: upstream tcp error: no route to host")},
	}
	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := rt.RoundTrip(req)

	if err == nil {
		resp.Body.Close()
		t.Fatal("upstream error should not be masked by sidecar fallback")
	}
	if native.calls != 0 {
		t.Fatalf("native fallback calls=%d want 0 for upstream error", native.calls)
	}
	if got := f.SidecarFallbackCount(); got != 0 {
		t.Fatalf("fallback metric=%d want 0 for upstream error", got)
	}
}

func TestFactory_For_MimicrySidecarProbeProfileFailureHasStableClass(t *testing.T) {
	f := NewFactory()
	f.SidecarSocketPath = "/tmp/huakai-sidecar-profile-missing.sock"
	f.sidecarProbe = func(_ context.Context, _ string, _ mimicry.TransportMode, profileID string) error {
		if profileID != mimicry.SidecarProfileAnthropicCLIMimicryV1 {
			t.Fatalf("probe profileID=%q want %q", profileID, mimicry.SidecarProfileAnthropicCLIMimicryV1)
		}
		return fmt.Errorf("%w: %s", mimicry.ErrSidecarProfileUnavailable, profileID)
	}

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)

	if err == nil {
		t.Fatalf("profile probe failure must fail closed, got rt=%T", rt)
	}
	if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarProfileUnavailable {
		t.Fatalf("error class=%q want %q", got, TransportErrorClassSidecarProfileUnavailable)
	}
}

func TestFactory_For_MimicryMandatorySidecarModeNeverFallsBack(t *testing.T) {
	native := &stubRoundTripper{}
	f := NewFactory()
	f.SetMimicry(native)
	f.SidecarSocketPath = "/tmp/huakai-sidecar-down.sock"
	f.SidecarFallbackEnabled = true
	f.SetMandatorySidecarMode(TransportModeMimicryClaudeCode, true)
	f.sidecarProbe = func(context.Context, string, mimicry.TransportMode, string) error {
		return errors.New("connection refused")
	}

	rt, err := f.For(ProviderAnthropic, TransportModeMimicryClaudeCode)

	if err == nil {
		t.Fatalf("mandatory sidecar mode must fail fast instead of degrading, got rt=%T", rt)
	}
	if rt == native {
		t.Fatal("mandatory sidecar mode returned Go-native fallback")
	}
	if got := f.SidecarFallbackCount(); got != 0 {
		t.Fatalf("mandatory failure must not increment fallback metric, got %d", got)
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

type stubRoundTripper struct{}

func (s *stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("stub round tripper should not be executed by factory tests")
}

type errorRoundTripper struct {
	err error
}

func (rt errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, rt.err
}

type responseRoundTripper struct {
	statusCode int
	calls      int
}

func (rt *responseRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls++
	return &http.Response{
		StatusCode: rt.statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

// MUTATION: standardRoundTripper 不再显式覆盖 MaxIdleConnsPerHost → 回 Go
// 默认 2 → 红(DM-17:网关负载下出站连接复用近失效,反复 TLS 握手)。
func TestStandardRoundTripperPoolTuning(t *testing.T) {
	f := &Factory{}
	rt, err := f.For("anthropic", TransportModeStandard)
	if err != nil {
		t.Fatalf("For(standard): %v", err)
	}
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("standard RoundTripper 应是 *http.Transport, got %T", rt)
	}
	if tr.MaxIdleConnsPerHost != 64 || tr.MaxIdleConns != 256 {
		t.Fatalf("pool tuning: per-host=%d total=%d, want 64/256", tr.MaxIdleConnsPerHost, tr.MaxIdleConns)
	}
	if tr.Proxy != nil {
		t.Fatal("standard 路径必须 Proxy=nil(代理只能走 dispatcher.applyProxy)")
	}
}
