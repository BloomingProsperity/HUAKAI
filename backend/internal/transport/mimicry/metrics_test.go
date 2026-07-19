package mimicry

import (
	"context"
	"expvar"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// egressDialCount 读某一 result 的出口拨号计数(同包直接读包级 expvar map)。expvar 是进程
// 全局、与其它测试(sidecar_log_test)共享,故所有断言取增量。
func egressDialCount(result string) int64 {
	v := egressDialTotal.Get(result)
	iv, ok := v.(*expvar.Int)
	if !ok || iv == nil {
		return 0
	}
	return iv.Value()
}

// TestEgressDialMetricOK:一次成功建立隧道应且仅应把 result=ok +1,其余 result 不动。
// 判别性:删 observer.established 里的 recordEgressDialResult(ok) → okDelta=0 → 红;
// 若把成功计到别的 result 桶(如误记 dial_fail)→ ok 增量 0 且该桶被污染 → 红。
func TestEgressDialMetricOK(t *testing.T) {
	beforeOK := egressDialCount(egressDialResultOK)
	beforeRejected := egressDialCount(egressDialResultRejected)
	beforeDialFail := egressDialCount(egressDialResultDialFail)

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }
	defer func() { sidecarDialContext = oldDial }()
	go fakeSidecarReadControlWriteAck(t, serverConn, []byte(`{"version":3,"ok":true}`), nil)

	client := NewSidecarClient(sidecarTestSocket).WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	conn, err := client.DialTLS(context.Background(), "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)
	if err != nil {
		t.Fatalf("DialTLS: %v", err)
	}
	conn.Close()

	if delta := egressDialCount(egressDialResultOK) - beforeOK; delta != 1 {
		t.Fatalf("result=ok 增量=%d 应为 1(成功建立未计数,或计到了错误 result 桶)", delta)
	}
	// 成功路径绝不能污染失败桶——否则出口成功率被虚高/虚低。
	if delta := egressDialCount(egressDialResultRejected) - beforeRejected; delta != 0 {
		t.Fatalf("成功拨号却让 result=rejected 增量=%d,应为 0", delta)
	}
	if delta := egressDialCount(egressDialResultDialFail) - beforeDialFail; delta != 0 {
		t.Fatalf("成功拨号却让 result=dial_fail 增量=%d,应为 0", delta)
	}
}

// TestEgressDialMetricRejected:sidecar 受理连接但回 ok:false 时,应且仅应把 result=rejected
// +1(= A1 rejected 日志 / error_class=sidecar_profile_unavailable 同事件)。
// 判别性:删 observer.rejected 的 recordEgressDialResult(rejected) → rejectedDelta=0 → 红。
func TestEgressDialMetricRejected(t *testing.T) {
	beforeRejected := egressDialCount(egressDialResultRejected)
	beforeOK := egressDialCount(egressDialResultOK)

	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }
	defer func() { sidecarDialContext = oldDial }()
	go fakeSidecarReadControlWriteAck(t, serverConn, []byte(`{"version":3,"ok":false,"error":{"code":"profile_unknown","message":"unknown profile foo"}}`), nil)

	client := NewSidecarClient(sidecarTestSocket).WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := client.DialTLS(context.Background(), "api.example.com", 443, "some-profile", nil, nil)
	if err == nil {
		t.Fatal("sidecar 拒绝时 DialTLS 应返回 err")
	}

	if delta := egressDialCount(egressDialResultRejected) - beforeRejected; delta != 1 {
		t.Fatalf("result=rejected 增量=%d 应为 1(拒绝事件未计数)", delta)
	}
	// 拒绝不是成功——绝不能污染 ok 桶。
	if delta := egressDialCount(egressDialResultOK) - beforeOK; delta != 0 {
		t.Fatalf("拒绝却让 result=ok 增量=%d,应为 0", delta)
	}
}

// TestEgressDialMetricDialFail:拨 unix socket 失败(sidecar 进程不在)时,应且仅应把
// result=dial_fail +1(= A1 dial 日志 / error_class=sidecar_unavailable 同事件,fail-closed 降级点)。
// 判别性:删 observer.failed 的 recordEgressDialResult / 或 egressDialResultForPhase 映射错
// (把 dial 映成 ok)→ dial_fail 增量 0 → 红。
func TestEgressDialMetricDialFail(t *testing.T) {
	beforeDialFail := egressDialCount(egressDialResultDialFail)
	beforeOK := egressDialCount(egressDialResultOK)

	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, io.ErrUnexpectedEOF // 模拟 sidecar socket 不可用
	}
	defer func() { sidecarDialContext = oldDial }()

	client := NewSidecarClient("/run/huakai/missing.sock").WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := client.DialTLS(ctx, "127.0.0.1", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)
	if err == nil {
		t.Fatal("拨号失败时 DialTLS 应 fail-closed 返回 err")
	}

	if delta := egressDialCount(egressDialResultDialFail) - beforeDialFail; delta != 1 {
		t.Fatalf("result=dial_fail 增量=%d 应为 1(拨号失败降级点未计数,或 phase→result 映射错)", delta)
	}
	if delta := egressDialCount(egressDialResultOK) - beforeOK; delta != 0 {
		t.Fatalf("拨号失败却让 result=ok 增量=%d,应为 0", delta)
	}
}

// TestEgressDialResultForPhaseMapping 钉死 phase→result 映射:三个失败 phase 各自落到独立
// 的 result 桶,不能混。这样"哪一层失败"在日志(phase)与指标(result)两侧划分一致。
// 判别性:把 egressDialResultForPhase 里任一 case 改错(如 read_ack 也返回 write_fail)→ 红。
func TestEgressDialResultForPhaseMapping(t *testing.T) {
	cases := map[string]string{
		sidecarPhaseDial:         egressDialResultDialFail,
		sidecarPhaseWriteControl: egressDialResultWriteFail,
		sidecarPhaseReadAck:      egressDialResultReadFail,
	}
	for phase, want := range cases {
		if got := egressDialResultForPhase(phase); got != want {
			t.Errorf("egressDialResultForPhase(%q)=%q want %q", phase, got, want)
		}
	}
	// 三个失败桶必须两两不同,否则无法从指标区分卡在哪一层。
	if egressDialResultDialFail == egressDialResultWriteFail ||
		egressDialResultWriteFail == egressDialResultReadFail ||
		egressDialResultDialFail == egressDialResultReadFail {
		t.Fatal("失败 result 桶不互异,指标无法区分故障层")
	}
}
