package dlq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeReplayStore 是注入用的 recordStore 实现:ClaimByID 返回预置 record,MarkFailed 返回预置错误,
// 用于驱动 Service.Replay 的 "handler 失败 + 状态写失败" 分支(无需真实 Postgres)。
type fakeReplayStore struct {
	rec           *Record
	markFailedErr error
	markFailedHit int
	lastReason    string
	lastDecision  RetryDecision
}

func (f *fakeReplayStore) Enqueue(context.Context, Event) (int64, error)      { return 0, nil }
func (f *fakeReplayStore) List(context.Context, ListFilter) ([]Record, error) { return nil, nil }
func (f *fakeReplayStore) MarkDelivered(context.Context, Record) error        { return nil }
func (f *fakeReplayStore) Claim(context.Context, Lane, string, time.Duration) (*Record, error) {
	return f.rec, nil
}
func (f *fakeReplayStore) ClaimByID(context.Context, int64, int64, string, time.Duration) (*Record, error) {
	return f.rec, nil
}
func (f *fakeReplayStore) MarkFailed(_ context.Context, _ Record, reason string, decision RetryDecision) error {
	f.markFailedHit++
	f.lastReason = reason
	f.lastDecision = decision
	return f.markFailedErr
}

func TestServiceReplayRedactsSecretFromPersistedFailureReason(t *testing.T) {
	store := &fakeReplayStore{rec: &Record{EventKind: "k"}}
	secretErr := errors.New("upstream failed with Authorization: Bearer sk-secret-material")
	s := newReplayService(store, func(context.Context, Record) error { return secretErr })

	if _, err := s.Replay(context.Background(), 7, 1, "operator-1"); !errors.Is(err, secretErr) {
		t.Fatalf("调用方仍应收到原始错误链: %v", err)
	}
	if store.lastReason != "[REDACTED]" {
		t.Fatalf("持久化失败原因=%q，期望脱敏占位", store.lastReason)
	}
}

func TestSafeFailureReasonTruncatesAtUTF8Boundary(t *testing.T) {
	reason := strings.Repeat("中", 342)
	got := safeFailureReason(reason)
	if !utf8.ValidString(got) {
		t.Fatalf("截断结果不是合法 UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, " [TRUNCATED]") {
		t.Fatalf("截断结果缺少标记: %q", got)
	}
	if strings.TrimSuffix(got, " [TRUNCATED]") != strings.Repeat("中", 341) {
		t.Fatalf("截断位置错误: %q", got)
	}
}

var (
	errReplayHandlerBoom = errors.New("replay handler boom")
	errReplayMarkFailed  = errors.New("mark_failed db write failed")
)

func newReplayService(store recordStore, handler Handler) *Service {
	return &Service{
		store:    store,
		handlers: map[EventKind]Handler{"k": handler},
		policy:   DefaultRetryPolicy(),
		now:      func() time.Time { return time.Unix(0, 0).UTC() },
	}
}

// TestServiceReplaySurfacesMarkFailedError 守:当手动 replay 的 handler 失败、且
// 随后的 MarkFailed 状态写入也失败时,Replay 必须把持久化错误暴露出来
// (与 worker 路径 ProcessClaim 一致),而不是用 `_ =` 丢弃。否则该 DLQ 行会
// 带着过期的手动 lease / retry-count 停留在 inflight,运营者只会看到 handler 错误,
// 永远不知道 recovery 系统连自己的失败状态都没能落盘。
//
// 变异检查:把 Replay 改回 `_ = s.store.MarkFailed(...); return rec, err`;此时返回的
// 错误就只剩 handler 错误,于是 errors.Is(err, errReplayMarkFailed) 为 false → 红。
func TestServiceReplaySurfacesMarkFailedError(t *testing.T) {
	store := &fakeReplayStore{rec: &Record{EventKind: "k"}, markFailedErr: errReplayMarkFailed}
	s := newReplayService(store, func(context.Context, Record) error { return errReplayHandlerBoom })

	_, err := s.Replay(context.Background(), 7, 1, "operator-1")
	if err == nil {
		t.Fatal("Replay must return an error when MarkFailed fails after a handler failure")
	}
	if !errors.Is(err, errReplayMarkFailed) {
		t.Fatalf("Replay must surface the MarkFailed persistence error; got %v", err)
	}
	// errors.Join 同时保留 handler 错误上下文(比 worker 路径只返回 markErr 更全)。
	if !errors.Is(err, errReplayHandlerBoom) {
		t.Fatalf("Replay should also preserve the handler error context; got %v", err)
	}
	if store.markFailedHit != 1 {
		t.Fatalf("MarkFailed should be attempted exactly once; got %d", store.markFailedHit)
	}
}

// TestServiceReplayHandlerFailMarkFailedOK 确认未改变的路径:当 MarkFailed 成功时,
// Replay 只返回 handler 错误(本次修复只在 MarkFailed 也失败的分支上新增行为,
// 所以常见的 handler 失败场景不能回退)。
func TestServiceReplayHandlerFailMarkFailedOK(t *testing.T) {
	store := &fakeReplayStore{rec: &Record{EventKind: "k"}, markFailedErr: nil}
	s := newReplayService(store, func(context.Context, Record) error { return errReplayHandlerBoom })

	_, err := s.Replay(context.Background(), 7, 1, "operator-1")
	if !errors.Is(err, errReplayHandlerBoom) {
		t.Fatalf("handler error must be surfaced when MarkFailed succeeds; got %v", err)
	}
	if store.markFailedHit != 1 {
		t.Fatalf("MarkFailed should be attempted exactly once; got %d", store.markFailedHit)
	}
}

// TestServiceReplayPoisonQuarantineToggle 守毒消息隔离的默认行为 + env/option 逃生阀(Owner
// 硬条件:默认行为翻转须保留 env 开关)。默认(未禁用):errors.Is(ErrUnretryable) 的毒消息在
// attempt1 直接 quarantine。禁用逃生阀:回退纯 NextFailure(attempt1 -> pending),不 quarantine,
// 旧行为可逆。
// Mutation: 删 failureDecision 的 if 分支(永远 NextFailureForErr)→ 禁用半段红;
// 反转(永远 NextFailure)→ 默认半段红。
func TestServiceReplayPoisonQuarantineToggle(t *testing.T) {
	poison := fmt.Errorf("decode payload: %w", ErrUnretryable)
	mk := func(disabled bool) (*fakeReplayStore, *Service) {
		store := &fakeReplayStore{rec: &Record{EventKind: "k"}}
		s := &Service{
			store:                    store,
			handlers:                 map[EventKind]Handler{"k": func(context.Context, Record) error { return poison }},
			policy:                   DefaultRetryPolicy(),
			now:                      func() time.Time { return time.Unix(0, 0).UTC() },
			poisonQuarantineDisabled: disabled,
		}
		return store, s
	}

	store, s := mk(false)
	if _, err := s.Replay(context.Background(), 7, 1, "op"); err == nil {
		t.Fatal("Replay must surface the poison handler error")
	}
	if store.lastDecision.Status != StatusQuarantined {
		t.Fatalf("default (enabled): poison must quarantine on attempt 1, got %s", store.lastDecision.Status)
	}

	store2, s2 := mk(true)
	if _, err := s2.Replay(context.Background(), 7, 1, "op"); err == nil {
		t.Fatal("Replay must still surface the handler error when escape hatch disables quarantine")
	}
	if store2.lastDecision.Status == StatusQuarantined {
		t.Fatalf("escape hatch disabled: must NOT quarantine")
	}
	if store2.lastDecision.Status != StatusPending {
		t.Fatalf("escape hatch disabled: attempt1 poison must fall back to pending, got %s", store2.lastDecision.Status)
	}
}

// TestServiceReplayNoHandlerQuarantines 守 replay 路径:没有注册 handler 是部署结构缺口,
// 继续重试同一记录不会恢复,必须直接进 quarantined,同时把 ErrNoHandler 暴露给操作者。
// Mutation: 去掉 NextFailureForErr 的 ErrNoHandler 短路 → lastDecision.Status=pending → 红。
func TestServiceReplayNoHandlerQuarantines(t *testing.T) {
	store := &fakeReplayStore{rec: &Record{EventKind: "missing_kind"}}
	s := &Service{
		store:    store,
		handlers: map[EventKind]Handler{},
		policy:   DefaultRetryPolicy(),
		now:      func() time.Time { return time.Unix(0, 0).UTC() },
	}

	_, err := s.Replay(context.Background(), 7, 1, "op")
	if !errors.Is(err, ErrNoHandler) {
		t.Fatalf("Replay error=%v want ErrNoHandler", err)
	}
	if store.lastDecision.Status != StatusQuarantined {
		t.Fatalf("no handler must quarantine on replay, got %s", store.lastDecision.Status)
	}
}

// TestPoisonQuarantineDisabledFromEnv 守 env 逃生阀解析:默认启用,显式 false/0/off/no 禁用,
// 无法识别值 fail-safe 回到默认启用。Mutation: 删任一 disable 分支 → 对应用例红。
func TestPoisonQuarantineDisabledFromEnv(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{raw: "", want: false},
		{raw: "false", want: true},
		{raw: "0", want: true},
		{raw: "off", want: true},
		{raw: "no", want: true},
		{raw: "n", want: true},
		{raw: "f", want: true},
		{raw: "true", want: false},
		{raw: "1", want: false},
		{raw: "garbage", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("HUAKAI_DLQ_QUARANTINE_POISON", tc.raw)
			if got := poisonQuarantineDisabledFromEnv(); got != tc.want {
				t.Fatalf("raw=%q disabled=%v want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestNewServiceWithPoisonQuarantineDisabledOption 守显式注入开关能覆盖 env 默认,
// 且选项在 env 解析之后生效(两个方向都覆盖):env 默认启用时 option 可强制禁用,
// env 已禁用时 option 可强制启用。Mutation: 把 WithPoisonQuarantineDisabled 改成 no-op
// (不写字段)→ 两条断言都红,因为 env 默认会赢。
func TestNewServiceWithPoisonQuarantineDisabledOption(t *testing.T) {
	// env 为空 → poisonQuarantineDisabledFromEnv 默认 false(启用隔离);
	// option 显式禁用必须覆盖成 true。
	t.Setenv("HUAKAI_DLQ_QUARANTINE_POISON", "")
	if s := NewService(nil, WithPoisonQuarantineDisabled(true)); !s.poisonQuarantineDisabled {
		t.Fatal("WithPoisonQuarantineDisabled(true) 必须覆盖 env 默认(启用),把开关置为禁用")
	}

	// env 设为禁用值 → 默认 true;option 显式启用必须把它覆盖回 false,
	// 这也证明 option 在 env 之后应用(否则 env 会赢)。
	t.Setenv("HUAKAI_DLQ_QUARANTINE_POISON", "off")
	if s := NewService(nil, WithPoisonQuarantineDisabled(false)); s.poisonQuarantineDisabled {
		t.Fatal("WithPoisonQuarantineDisabled(false) 必须覆盖 env 的禁用,把开关置回启用")
	}
}
