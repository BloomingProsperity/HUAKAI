package gateway

import (
	"bytes"
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// TestForwarderCapturesFirstByteAndLastEventWallClock 守护 TTFT 断链修复的 forwarder 侧:
// 流式转发只要向客户端 flush 过内容,draft 必须带上【绝对墙钟】首字时刻 FirstByteAt 与流末
// LastEventAt,结算据此写 usage_records,使 TTFT=first_byte_at-requested_at 与 TPS 可算。
// 此前 forwarder 只量了相对 ms 的 FirstTokenLatencyMillis 却无绝对时刻、settler 也从不写,
// 导致列恒 NULL、TTFT/TPS 指标恒 0(监控盲区)。
//
// 判别性:删 forwarder.go 首字分支的 draft.FirstByteAt=time.Now() → FirstByteAt 零值 → 红;
// 删 finishDraft 的 d.LastEventAt 赋值 → LastEventAt 零值 → 红。
func TestForwarderCapturesFirstByteAndLastEventWallClock(t *testing.T) {
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family: "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{
			&proto.CanonicalEvent{Type: "content_block_delta", Delta: &proto.CanonicalContentDelta{Text: "hi"}},
		}},
	}

	before := time.Now().UTC()
	rec := httptest.NewRecorder()
	draft, err := f.Forward(
		context.Background(),
		bytes.NewReader([]byte("event: chunk\ndata: {\"delta\":\"hi\"}\n\n")),
		rec,
		anthropicForwardRequest(1, 100),
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	after := time.Now().UTC()

	if draft.FirstByteAt.IsZero() {
		t.Fatal("draft.FirstByteAt 未设:TTFT 数据源断链(首字绝对时刻未捕获)")
	}
	if draft.FirstByteAt.Before(before) || draft.FirstByteAt.After(after) {
		t.Fatalf("FirstByteAt=%v 不在 [%v,%v] 区间内(应为本次转发的墙钟时刻)", draft.FirstByteAt, before, after)
	}
	if draft.LastEventAt.IsZero() {
		t.Fatal("draft.LastEventAt 未设:TPS 数据源断链(流末时刻未捕获)")
	}
	// TPS 窗口非负:last_event_at 必须 >= first_byte_at(否则 avg_tps 分母为负)。
	if draft.LastEventAt.Before(draft.FirstByteAt) {
		t.Fatalf("LastEventAt=%v 早于 FirstByteAt=%v(TPS 分母为负)", draft.LastEventAt, draft.FirstByteAt)
	}
}

// TestForwarderNoFirstByteWhenNothingEmitted 守护成对语义:无内容产出(0 事件)时,
// FirstByteAt/LastEventAt 都留零值(→settler 写 NULL→被 perf SQL 排除),不产生
// first_byte NULL 而 last_event 非 NULL 的半截数据。
// 判别性:若 finishDraft 无条件设 LastEventAt(去掉 !FirstByteAt.IsZero() 守卫)→ 此处红。
func TestForwarderNoFirstByteWhenNothingEmitted(t *testing.T) {
	f := newForwarder()
	f.ProtocolAdapters = &stubSingleAdapterRegistry{
		family:  "anthropic_messages",
		adapter: &forwarderClientAdapterUpstreamStub{events: []any{}}, // 0 canonical → 不写客户端
	}

	rec := httptest.NewRecorder()
	draft, err := f.Forward(
		context.Background(),
		bytes.NewReader([]byte("event: ping\ndata: {}\n\n")),
		rec,
		anthropicForwardRequest(1, 100),
	)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if !draft.FirstByteAt.IsZero() {
		t.Fatalf("无产出却设了 FirstByteAt=%v(应留零值→NULL)", draft.FirstByteAt)
	}
	if !draft.LastEventAt.IsZero() {
		t.Fatalf("无首字却设了 LastEventAt=%v(会造成 first_byte NULL/last_event 非 NULL 半截数据)", draft.LastEventAt)
	}
}
