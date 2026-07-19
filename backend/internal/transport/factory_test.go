package transport

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

const testSidecarSocket = "/home/ubuntu/.cache/huakai-codex/test-sidecar.sock"

func TestFactoryStandardTransportIsolatedFromEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://bad-proxy.invalid:9999")
	t.Setenv("HTTPS_PROXY", "http://bad-proxy.invalid:9999")
	factory := NewFactory()

	rt, err := factory.For(ProviderOpenAI, TransportModeStandard)
	if err != nil {
		t.Fatalf("取 standard transport：%v", err)
	}
	transport, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("standard transport 类型=%T，期望 *http.Transport", rt)
	}
	if transport.Proxy != nil {
		t.Fatal("standard transport 不得读取环境代理")
	}
	if transport.MaxIdleConnsPerHost != 64 || transport.MaxIdleConns != 256 {
		t.Fatalf("连接池参数=%d/%d，期望 64/256", transport.MaxIdleConnsPerHost, transport.MaxIdleConns)
	}
	second, err := factory.For(ProviderOpenAI, TransportModeStandard)
	if err != nil || second != rt {
		t.Fatalf("standard transport 必须复用连接池，second=%T err=%v", second, err)
	}
}

func TestFactoryMimicryRequiresSidecarSocket(t *testing.T) {
	factory := NewFactory()
	before := egressDialCount("dial_fail")

	rt, err := factory.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err == nil || rt != nil {
		t.Fatalf("空 socket 必须 fail-closed，rt=%T err=%v", rt, err)
	}
	if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarUnavailable {
		t.Fatalf("错误分类=%q，期望 %q", got, TransportErrorClassSidecarUnavailable)
	}
	if delta := egressDialCount("dial_fail") - before; delta != 1 {
		t.Fatalf("空 socket 拒绝计数增量=%d，期望 1", delta)
	}
}

func TestFactoryMimicryIgnoresRetiredDowngradeSwitch(t *testing.T) {
	// 旧变量即使残留在部署环境里，也不能再把账号身份降级成标准 TLS。
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "false")
	factory := readyTestFactory()

	rt, err := factory.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("取 Rust sidecar transport：%v", err)
	}
	if _, ok := rt.(interface{ SidecarProfileID() string }); !ok {
		t.Fatalf("旧降级变量导致返回非 sidecar transport：%T", rt)
	}
	if _, ok := rt.(*http.Transport); ok {
		t.Fatal("mimicry mode 不能降级成 standard transport")
	}
}

func TestFactoryMimicryProbesAndCachesSidecarTransport(t *testing.T) {
	factory := NewFactory()
	factory.SidecarSocketPath = testSidecarSocket
	factory.sidecarProbeTimeout = 500 * time.Millisecond
	var calls atomic.Int64
	var sawDeadline atomic.Bool
	factory.sidecarProbe = func(ctx context.Context, socket string, mode mimicry.TransportMode, profileID string) error {
		calls.Add(1)
		if socket != testSidecarSocket || mode != mimicry.ModeMimicryClaudeCode || profileID != mimicry.SidecarProfileAnthropicCLIMimicryV1 {
			return fmt.Errorf("探测参数错误：socket=%s mode=%s profile=%s", socket, mode, profileID)
		}
		_, ok := ctx.Deadline()
		sawDeadline.Store(ok)
		return nil
	}

	first, err := factory.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("首次取 sidecar transport：%v", err)
	}
	second, err := factory.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("再次取 sidecar transport：%v", err)
	}
	if first != second || calls.Load() != 1 || !sawDeadline.Load() {
		t.Fatalf("缓存或有界探测失效：same=%v calls=%d deadline=%v", first == second, calls.Load(), sawDeadline.Load())
	}
}

func TestFactoryMimicryUnavailableNeverFallsBackAtRuntime(t *testing.T) {
	factory := readyTestFactory()
	rt, err := factory.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
	if err != nil {
		t.Fatalf("构造 sidecar transport：%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatalf("sidecar 不可用时不应收到响应：%s", resp.Status)
	}
	if err == nil || !strings.Contains(err.Error(), testSidecarSocket) {
		t.Fatalf("sidecar 不可用必须返回 socket 错误，得到 %v", err)
	}
}

func TestFactoryMimicryFailureCacheCoalescesConcurrentProbes(t *testing.T) {
	factory := NewFactory()
	factory.SidecarSocketPath = testSidecarSocket
	factory.sidecarProbeTimeout = 30 * time.Millisecond
	factory.sidecarFailureCacheTTL = time.Second
	var calls atomic.Int64
	factory.sidecarProbe = func(ctx context.Context, _ string, _ mimicry.TransportMode, _ string) error {
		calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}

	const concurrent = 10
	start := make(chan struct{})
	errCh := make(chan error, concurrent)
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for range concurrent {
		go func() {
			defer wg.Done()
			<-start
			_, err := factory.For(ProviderAnthropic, TransportModeMimicryClaudeCode)
			errCh <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if got := TransportErrorClassOf(err); got != TransportErrorClassSidecarUnavailable {
			t.Fatalf("并发失败分类=%q，期望 sidecar_unavailable；err=%v", got, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("并发失败探测次数=%d，期望 1", calls.Load())
	}
}

func TestFactorySafeEquivalentProfilesAreRoutable(t *testing.T) {
	cases := []struct {
		provider ProviderCode
		mode     TransportMode
		profile  string
	}{
		{ProviderAntigravity, TransportModeMimicryAntigravity, mimicry.SidecarProfileAntigravitySafeV1},
		{ProviderCursor, TransportModeMimicryCursor, mimicry.SidecarProfileCursorSafeV1},
		{ProviderCopilot, TransportModeMimicryCopilot, mimicry.SidecarProfileCopilotSafeV1},
		{ProviderWindsurf, TransportModeMimicryWindsurf, mimicry.SidecarProfileWindsurfSafeV1},
	}
	for _, tc := range cases {
		factory := readyTestFactory()
		rt, err := factory.For(tc.provider, tc.mode)
		if err != nil {
			t.Fatalf("provider=%s mode=%s 构造失败：%v", tc.provider, tc.mode, err)
		}
		profiled, ok := rt.(interface{ SidecarProfileID() string })
		gotProfile := ""
		if ok {
			gotProfile = profiled.SidecarProfileID()
		}
		if !ok || gotProfile != tc.profile {
			t.Fatalf("provider=%s mode=%s profile=%q，期望 %q", tc.provider, tc.mode, gotProfile, tc.profile)
		}
	}
}

func TestFactoryDiagnosticsStillFailsLoud(t *testing.T) {
	_, err := NewFactory().For(ProviderOpenAI, TransportModeDiagnosticsOnly)
	if !errors.Is(err, ErrTransportNotImplemented) {
		t.Fatalf("未注入 diagnostics 应明确失败，得到 %v", err)
	}
}

func readyTestFactory() *Factory {
	factory := NewFactory()
	factory.SidecarSocketPath = testSidecarSocket
	factory.sidecarProbe = func(context.Context, string, mimicry.TransportMode, string) error { return nil }
	return factory
}

func egressDialCount(result string) int64 {
	metric, ok := expvar.Get("egress_sidecar_dial_total").(*expvar.Map)
	if !ok || metric == nil {
		return 0
	}
	value, ok := metric.Get(result).(*expvar.Int)
	if !ok || value == nil {
		return 0
	}
	return value.Value()
}
