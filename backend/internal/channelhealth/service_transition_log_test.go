package channelhealth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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
func (h *collectingHandler) WithGroup(string) slog.Handler      { return h }

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

// auditFailStore 让 AppendAudit 恒失败,并把自身当事务 store,复现「转换审计写库失败」路径。
type auditFailStore struct {
	Store
}

func (auditFailStore) AppendAudit(context.Context, AuditEvent) error {
	return errors.New("simulated audit append failure")
}

func (s auditFailStore) WithTx(ctx context.Context, fn func(Store) error) error {
	return fn(s) // 事务 store 用自身,fn 内 AppendAudit 走失败版
}

// commitFailStore 让 fn 正常跑完(转换/审计成功、pending 攒好)后,WithTx 返回错误,
// 复现「Serializable Commit 抛 40001 → 整事务回滚」——正是 401 风暴/退池并发下的常见冲突。
type commitFailStore struct {
	Store
}

type serializationRetryStore struct {
	Store
	failures int
	calls    int
}

func (s *serializationRetryStore) WithTx(ctx context.Context, fn func(Store) error) error {
	s.calls++
	if s.calls <= s.failures {
		return &pgconn.PgError{Code: "40001", Message: "serialization failure"}
	}
	return fn(s.Store)
}

func (s commitFailStore) WithTx(ctx context.Context, fn func(Store) error) error {
	_ = fn(s.Store)                                            // 让转换/审计/pending 登记全部发生
	return errors.New("simulated serialization_failure 40001") // 但提交失败
}

// TestChannelHealth_TransitionLogNotEmittedOnAuditFailure 守时序不变量:转换审计写库失败时,
// 不得打出运维日志(否则运营看到 DB 权威审计查无此事的幽灵转换)。
// 变异守卫:把 emitTransitionEvents 里的 recordTransitionLog 移到审计循环之前 → 审计失败仍打日志 → 本测试红。
func TestChannelHealth_TransitionLogNotEmittedOnAuditFailure(t *testing.T) {
	ctx := context.Background()
	clock := &fixedClock{now: time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)}
	handler, snapshot := newCollectingHandler()
	svc := NewService(auditFailStore{Store: NewMemoryStore()}, testPolicy(), clock, WithLogger(slog.New(handler)))
	key := testKey()

	// ForceCooldown 单次即触发 active→cooling_down 转换,其审计写入命中失败版本。
	_, err := svc.ForceCooldown(ctx, key, clock.now.Add(time.Hour), "test")
	if err == nil {
		t.Fatal("审计失败时 ForceCooldown 应返回错误")
	}
	if recs := snapshot(); len(recs) != 0 {
		t.Fatalf("审计失败时不得打运维日志,实得 %d 条:%+v", len(recs), recs)
	}
}

// TestChannelHealth_TransitionLogNotEmittedOnCommitRollback 守本片核心修复(审查 S2):
// 事务在转换/审计成功后于 Commit 阶段失败回滚时,不得留下与 DB 权威审计矛盾的幽灵运维日志。
// 变异守卫:把 withMutation 的 `if err == nil` flush 守卫改成无条件 flush → 回滚也打日志 → 本测试红。
func TestChannelHealth_TransitionLogNotEmittedOnCommitRollback(t *testing.T) {
	ctx := context.Background()
	clock := &fixedClock{now: time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)}
	handler, snapshot := newCollectingHandler()
	svc := NewService(commitFailStore{Store: NewMemoryStore()}, testPolicy(), clock, WithLogger(slog.New(handler)))
	key := testKey()

	_, err := svc.ForceCooldown(ctx, key, clock.now.Add(time.Hour), "test")
	if err == nil {
		t.Fatal("Commit 失败时 ForceCooldown 应返回错误")
	}
	if recs := snapshot(); len(recs) != 0 {
		t.Fatalf("事务回滚时不得留幽灵运维日志,实得 %d 条:%+v", len(recs), recs)
	}
}

func TestChannelHealthMutationRetriesSerializationConflict(t *testing.T) {
	ctx := context.Background()
	clock := &fixedClock{now: time.Date(2026, 7, 3, 8, 0, 0, 0, time.UTC)}
	handler, snapshot := newCollectingHandler()
	store := &serializationRetryStore{Store: NewMemoryStore(), failures: 2}
	svc := NewService(store, testPolicy(), clock, WithLogger(slog.New(handler)))

	if _, err := svc.ForceCooldown(ctx, testKey(), clock.now.Add(time.Hour), "test"); err != nil {
		t.Fatalf("瞬时序列化冲突应重试成功: %v", err)
	}
	if store.calls != 3 {
		t.Fatalf("事务尝试次数=%d want 3", store.calls)
	}
	if got := len(snapshot()); got != 1 {
		t.Fatalf("仅成功事务应产生一条日志，得到 %d", got)
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
