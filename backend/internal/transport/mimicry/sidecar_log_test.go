package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// sidecarCaptureHandler 收集 slog 记录,供断言 go↔rust 出口边界日志字段与级别。
type sidecarCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *sidecarCaptureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *sidecarCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *sidecarCaptureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *sidecarCaptureHandler) WithGroup(string) slog.Handler      { return h }

// byPhase 返回第一条 phase 匹配的记录及其 attr map;找不到返回 ok=false。
func (h *sidecarCaptureHandler) byPhase(phase string) (slog.Record, map[string]any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		attrs := map[string]any{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		if attrs["phase"] == phase {
			return r, attrs, true
		}
	}
	return slog.Record{}, nil, false
}

// fakeSidecarReadControlWriteAck 扮演 sidecar:读一帧控制帧,回一帧给定 ack。captured 非 nil
// 时把解出的控制帧回传,供断言"帧里带的 correlation_id"与"Go 日志里的 correlation_id"一致
// (证跨边界关联真的过河,而非孤岛)。
func fakeSidecarReadControlWriteAck(t *testing.T, conn net.Conn, ack []byte, captured chan<- sidecarControlRequest) {
	t.Helper()
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return
	}
	body := make([]byte, binary.LittleEndian.Uint32(prefix[:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}
	if captured != nil {
		var req sidecarControlRequest
		_ = json.Unmarshal(body, &req)
		captured <- req
	}
	writeSidecarTestFrame(t, conn, ack)
}

// TestSidecarDialObserverLogsEstablished:成功建立隧道时,出口边界应打一条 Debug
// established 日志,带 component/phase/target_host/control_frame_bytes。
// 判别性:删 DialTLS 末尾的 obs.established(ctx, frameBytes) → 此处 byPhase 找不到 → 红。
func TestSidecarDialObserverLogsEstablished(t *testing.T) {
	cap := &sidecarCaptureHandler{}
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }
	defer func() { sidecarDialContext = oldDial }()

	captured := make(chan sidecarControlRequest, 1)
	go fakeSidecarReadControlWriteAck(t, serverConn, []byte(`{"version":3,"ok":true}`), captured)

	client := NewSidecarClient(sidecarTestSocket).WithLogger(slog.New(cap))
	conn, err := client.DialTLS(context.Background(), "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)
	if err != nil {
		t.Fatalf("DialTLS: %v", err)
	}
	conn.Close()
	wireReq := <-captured

	rec, attrs, ok := cap.byPhase(sidecarPhaseEstablished)
	if !ok {
		t.Fatal("缺 established 日志:出口隧道建立事件未记录(删 obs.established 即此处红)")
	}
	if rec.Level != slog.LevelDebug {
		t.Errorf("established 应为 Debug 级(热路径不刷屏),实得 %v", rec.Level)
	}
	if attrs["component"] != egressSidecarComponent {
		t.Errorf("component=%v,应为 %q", attrs["component"], egressSidecarComponent)
	}
	if attrs["target_host"] != "api.anthropic.com" {
		t.Errorf("target_host=%v,应为 api.anthropic.com", attrs["target_host"])
	}
	if fb, _ := attrs["control_frame_bytes"].(int64); fb <= 0 {
		t.Errorf("control_frame_bytes 应 >0(帧传输层观测量),实得 %v", attrs["control_frame_bytes"])
	}
	// 跨边界关联判别:日志里的 correlation_id 必须非空,且与过河的控制帧里的
	// correlation_id 完全一致——证 go↔rust 两侧能用同一 id 关联(删 CorrelationID 赋值即红)。
	logCorr, _ := attrs["correlation_id"].(string)
	if logCorr == "" {
		t.Error("日志缺 correlation_id:跨边界关联断链")
	}
	if wireReq.CorrelationID == "" {
		t.Error("控制帧未带 correlation_id:关联 id 没过河到 Rust 侧")
	}
	if logCorr != wireReq.CorrelationID {
		t.Errorf("日志 correlation_id=%q 与帧 correlation_id=%q 不一致,两侧无法关联", logCorr, wireReq.CorrelationID)
	}
}

// TestSidecarDialObserverLogsRejected:sidecar 受理连接但回 ok:false 时,应打一条 Warn
// rejected 日志,error_class=sidecar_profile_unavailable,reject_reason 带上游原因。
// 判别性:删 obs.rejected(ctx, ack.Error) → 找不到 rejected 记录 → 红。
func TestSidecarDialObserverLogsRejected(t *testing.T) {
	cap := &sidecarCaptureHandler{}
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }
	defer func() { sidecarDialContext = oldDial }()

	go fakeSidecarReadControlWriteAck(t, serverConn, []byte(`{"version":3,"ok":false,"error":{"code":"profile_unknown","message":"unknown profile foo"}}`), nil)

	client := NewSidecarClient(sidecarTestSocket).WithLogger(slog.New(cap))
	_, err := client.DialTLS(context.Background(), "api.example.com", 443, "some-profile", nil, nil)
	if err == nil {
		t.Fatal("sidecar 拒绝时 DialTLS 应返回 err")
	}

	rec, attrs, ok := cap.byPhase(sidecarPhaseRejected)
	if !ok {
		t.Fatal("缺 rejected 日志:sidecar 拒绝事件未记录(删 obs.rejected 即此处红)")
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("rejected 应为 Warn 级(出口拒服务需可见),实得 %v", rec.Level)
	}
	if attrs["error_class"] != sidecarErrClassProfile {
		t.Errorf("error_class=%v,应为 %q", attrs["error_class"], sidecarErrClassProfile)
	}
	if attrs["error_code"] != SidecarErrorProfileUnknown {
		t.Errorf("error_code=%v,应为 %q", attrs["error_code"], SidecarErrorProfileUnknown)
	}
	if reason, _ := attrs["reject_reason"].(string); !strings.Contains(reason, "unknown profile foo") {
		t.Errorf("reject_reason=%v,应带上游拒绝原因", attrs["reject_reason"])
	}
}

// TestSidecarDialObserverLogsDialFailed:拨 unix socket 失败(sidecar 进程不在)时,应打
// 一条 Warn dial 日志,error_class=sidecar_unavailable——这是 fail-closed 降级点,必须可见。
// 判别性:删拨号失败分支的 obs.failed(...) → 找不到 dial 记录 → 红。
func TestSidecarDialObserverLogsDialFailed(t *testing.T) {
	cap := &sidecarCaptureHandler{}
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("missing sidecar socket")
	}
	defer func() { sidecarDialContext = oldDial }()

	client := NewSidecarClient("/run/huakai/missing.sock").WithLogger(slog.New(cap))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := client.DialTLS(ctx, "127.0.0.1", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)
	if err == nil {
		t.Fatal("拨号失败时 DialTLS 应 fail-closed 返回 err")
	}

	rec, attrs, ok := cap.byPhase(sidecarPhaseDial)
	if !ok {
		t.Fatal("缺 dial 失败日志:出口降级点未记录(删 obs.failed 即此处红)")
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("dial 失败应为 Warn 级,实得 %v", rec.Level)
	}
	if attrs["error_class"] != sidecarErrClassUnavailable {
		t.Errorf("error_class=%v,应为 %q", attrs["error_class"], sidecarErrClassUnavailable)
	}
	// 出口边界绝不记代理凭据:proxied 只应是布尔。
	if _, isBool := attrs["proxied"].(bool); !isBool {
		t.Errorf("proxied 应为布尔(不泄代理细节),实得 %T", attrs["proxied"])
	}
}
