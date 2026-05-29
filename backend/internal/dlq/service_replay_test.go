package dlq

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeReplayStore 是注入用的 recordStore 实现:ClaimByID 返回预置 record,MarkFailed 返回预置错误,
// 用于驱动 Service.Replay 的 "handler 失败 + 状态写失败" 分支(无需真实 Postgres)。
type fakeReplayStore struct {
	rec           *Record
	markFailedErr error
	markFailedHit int
}

func (f *fakeReplayStore) Enqueue(context.Context, Event) (int64, error)       { return 0, nil }
func (f *fakeReplayStore) List(context.Context, ListFilter) ([]Record, error)  { return nil, nil }
func (f *fakeReplayStore) MarkDelivered(context.Context, Record) error         { return nil }
func (f *fakeReplayStore) Claim(context.Context, Lane, string, time.Duration) (*Record, error) {
	return f.rec, nil
}
func (f *fakeReplayStore) ClaimByID(context.Context, int64, string, time.Duration) (*Record, error) {
	return f.rec, nil
}
func (f *fakeReplayStore) MarkFailed(context.Context, Record, string, RetryDecision) error {
	f.markFailedHit++
	return f.markFailedErr
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

// TestServiceReplaySurfacesMarkFailedError guards S2-096: when a manual replay's handler fails AND
// the subsequent MarkFailed state write ALSO fails, Replay must SURFACE the persistence error
// (matching the worker path ProcessClaim) instead of discarding it with `_ =`. Otherwise the DLQ
// row can stay inflight with a stale manual lease / retry-count and the operator only ever sees the
// handler error, never learning the recovery system failed to persist its own failure state.
//
// Mutation check: revert Replay to `_ = s.store.MarkFailed(...); return rec, err`; the returned
// error is then only the handler error, so errors.Is(err, errReplayMarkFailed) is false → red.
func TestServiceReplaySurfacesMarkFailedError(t *testing.T) {
	store := &fakeReplayStore{rec: &Record{EventKind: "k"}, markFailedErr: errReplayMarkFailed}
	s := newReplayService(store, func(context.Context, Record) error { return errReplayHandlerBoom })

	_, err := s.Replay(context.Background(), 1, "operator-1")
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

// TestServiceReplayHandlerFailMarkFailedOK confirms the unchanged path: when MarkFailed succeeds,
// Replay returns just the handler error (the fix only adds behavior on the MarkFailed-also-failed
// branch, so the common handler-failure case must not regress).
func TestServiceReplayHandlerFailMarkFailedOK(t *testing.T) {
	store := &fakeReplayStore{rec: &Record{EventKind: "k"}, markFailedErr: nil}
	s := newReplayService(store, func(context.Context, Record) error { return errReplayHandlerBoom })

	_, err := s.Replay(context.Background(), 1, "operator-1")
	if !errors.Is(err, errReplayHandlerBoom) {
		t.Fatalf("handler error must be surfaced when MarkFailed succeeds; got %v", err)
	}
	if store.markFailedHit != 1 {
		t.Fatalf("MarkFailed should be attempted exactly once; got %d", store.markFailedHit)
	}
}
