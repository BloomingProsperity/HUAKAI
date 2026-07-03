package channelhealth

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturedLog 收集一条 slog 记录的级别、消息与属性(键→值字符串),供判别性断言。
type capturedLog struct {
	Level slog.Level
	Msg   string
	Attrs map[string]string
}

// collectingHandler 是测试用 slog.Handler,把每条记录收进切片(不落 stdout)。
type collectingHandler struct {
	mu   *sync.Mutex
	recs *[]capturedLog
}

func newCollectingHandler() (*collectingHandler, func() []capturedLog) {
	var mu sync.Mutex
	recs := []capturedLog{}
	h := &collectingHandler{mu: &mu, recs: &recs}
	snapshot := func() []capturedLog {
		mu.Lock()
		defer mu.Unlock()
		out := make([]capturedLog, len(recs))
		copy(out, recs)
		return out
	}
	return h, snapshot
}

func (h *collectingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *collectingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]string{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, capturedLog{Level: r.Level, Msg: r.Message, Attrs: attrs})
	h.mu.Unlock()
	return nil
}

func (h *collectingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *collectingHandler) WithGroup(string) slog.Handler       { return h }

// TestChannelHealth_TransitionLogEmitsStructuredSlog 守卫缺口③观测盲区的补丁:
// 每次真实健康状态转换恰打一条 stdout 结构化 slog,级别按目标状态分级(禁用/冷却=Warn、恢复=Info),
// 字段含 provider_account_id/previous_state/new_state 且值正确,且绝不泄漏凭证材料。
//
// 变异守卫:①删 emitTransitionEvents 里的 logTransition 调用 → 日志数 0≠3 变红;
// ②把 transitionLogLevel 的 disabled 分支改成 Info → 转换2 的 Warn 断言变红;
// ③日志里带上 RawUpstreamText → 脱敏断言变红。
func TestChannelHealth_TransitionLogEmitsStructuredSlog(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	clock := &fixedClock{now: time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)}
	handler, snapshot := newCollectingHandler()
	svc := NewService(store, testPolicy(), clock, WithLogger(slog.New(handler)))
	key := testKey()

	// 转换1:active→cooling_down(错误率击穿)。前两次不足样本量不转换、不打日志,第三次才转换。
	for i := 0; i < 3; i++ {
		clock.Add(time.Millisecond)
		if _, err := svc.ApplySignal(ctx, Signal{Key: key, Class: SignalChannelError}); err != nil {
			t.Fatalf("ApplySignal channel_error: %v", err)
		}
	}
	// 转换2:cooling_down→disabled(ban 信号)。刻意带含假 token 的原始上游文本,验证脱敏。
	const fakeSecret = "hk_live_supersecrettoken_should_never_be_logged"
	clock.Add(time.Millisecond)
	if _, err := svc.ApplySignal(ctx, Signal{
		Key: key, Class: SignalTokenRevoked, RawUpstreamText: "revoked token " + fakeSecret,
	}); err != nil {
		t.Fatalf("ApplySignal token_revoked: %v", err)
	}
	// 转换3:disabled→active(运营 ForceActive,带 actor)。
	if _, err := svc.ForceActive(ctx, key, "42", "break glass"); err != nil {
		t.Fatalf("ForceActive: %v", err)
	}

	recs := snapshot()
	if len(recs) != 3 {
		t.Fatalf("期望每次真实转换恰一条 slog(共3条),实得 %d 条:%+v", len(recs), recs)
	}

	// 转换1:active→cooling_down 打 Warn。
	assertTransitionLog(t, recs[0], slog.LevelWarn, "active", "cooling_down", key.ProviderAccountID)
	// 转换2:cooling_down→disabled 打 Warn。
	assertTransitionLog(t, recs[1], slog.LevelWarn, "cooling_down", "disabled", key.ProviderAccountID)
	// 转换3:disabled→active(恢复)打 Info,带 actor_id 与 recovered 事件类型。
	assertTransitionLog(t, recs[2], slog.LevelInfo, "disabled", "active", key.ProviderAccountID)
	if recs[2].Attrs["event_type"] != string(EventRecovered) {
		t.Fatalf("恢复日志 event_type=%q want %q", recs[2].Attrs["event_type"], EventRecovered)
	}
	if recs[2].Attrs["actor_id"] != "42" {
		t.Fatalf("ForceActive 日志缺 actor_id=42,实得 %q", recs[2].Attrs["actor_id"])
	}

	// 脱敏 + component:任一条日志的任一字段都不得含凭证形态串,且 component 恒为本包标识。
	for i, rec := range recs {
		blob, _ := json.Marshal(rec.Attrs)
		if strings.Contains(string(blob), fakeSecret) || strings.Contains(string(blob), "supersecret") {
			t.Fatalf("第%d条日志泄漏凭证材料:%s", i, blob)
		}
		if rec.Attrs["component"] != logComponent {
			t.Fatalf("第%d条日志 component=%q want %q", i, rec.Attrs["component"], logComponent)
		}
		if rec.Attrs["vendor"] != key.Vendor {
			t.Fatalf("第%d条日志 vendor=%q want %q", i, rec.Attrs["vendor"], key.Vendor)
		}
	}
}

func assertTransitionLog(t *testing.T, rec capturedLog, wantLevel slog.Level, wantPrev, wantNew string, wantAcct int64) {
	t.Helper()
	if rec.Level != wantLevel {
		t.Fatalf("级别=%v want %v(转换 %s→%s):%+v", rec.Level, wantLevel, wantPrev, wantNew, rec)
	}
	if rec.Attrs["previous_state"] != wantPrev {
		t.Fatalf("previous_state=%q want %q", rec.Attrs["previous_state"], wantPrev)
	}
	if rec.Attrs["new_state"] != wantNew {
		t.Fatalf("new_state=%q want %q", rec.Attrs["new_state"], wantNew)
	}
	if rec.Attrs["provider_account_id"] != strconv.FormatInt(wantAcct, 10) {
		t.Fatalf("provider_account_id=%q want %d", rec.Attrs["provider_account_id"], wantAcct)
	}
}
