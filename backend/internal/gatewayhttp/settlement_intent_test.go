package gatewayhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	l2cache "github.com/BloomingProsperity/HUAKAI/internal/cache"
	"github.com/BloomingProsperity/HUAKAI/internal/payloadhash"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
)

type recordingSettlementIntentStore struct {
	insertErr      error
	operationErrs  map[string]error
	operationCalls map[string]int
	returnZeroID   bool
	insertCalls    int
	created        settlementintent.CreateParams
	events         []string
	version        int32
	firstByteAt    time.Time
	actualCost     decimal.Decimal
	settledAt      time.Time
}

func (s *recordingSettlementIntentStore) Insert(ctx context.Context, in settlementintent.CreateParams) (int64, error) {
	s.insertCalls++
	s.created = in
	if s.insertErr != nil {
		return 0, s.insertErr
	}
	if err := s.operationError(ctx, "insert_pending"); err != nil {
		return 0, err
	}
	if s.returnZeroID {
		return 0, nil
	}
	s.events = append(s.events, "pending")
	s.version = 0
	return 71001, nil
}

func (s *recordingSettlementIntentStore) MarkDelivering(ctx context.Context, id int64, version int32, firstByteAt time.Time) (int32, error) {
	if err := s.operationError(ctx, "mark_delivering"); err != nil {
		return 0, err
	}
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	s.firstByteAt = firstByteAt
	return s.advance("delivering"), nil
}

func (s *recordingSettlementIntentStore) MarkSettling(ctx context.Context, id int64, version int32, actualCost decimal.Decimal) (int32, error) {
	if err := s.operationError(ctx, "mark_settling"); err != nil {
		return 0, err
	}
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	s.actualCost = actualCost
	return s.advance("settling"), nil
}

func (s *recordingSettlementIntentStore) MarkSettled(ctx context.Context, id int64, version int32, actualCost decimal.Decimal, settledAt time.Time) (int32, error) {
	if err := s.operationError(ctx, "mark_settled"); err != nil {
		return 0, err
	}
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	s.actualCost = actualCost
	s.settledAt = settledAt
	return s.advance("settled"), nil
}

func (s *recordingSettlementIntentStore) MarkAborted(ctx context.Context, id int64, version int32) (int32, error) {
	if err := s.operationError(ctx, "mark_aborted"); err != nil {
		return 0, err
	}
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	return s.advance("aborted"), nil
}

func (s *recordingSettlementIntentStore) MarkFailed(ctx context.Context, id int64, version int32, actualCost decimal.Decimal) (int32, error) {
	if err := s.operationError(ctx, "mark_failed"); err != nil {
		return 0, err
	}
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	s.actualCost = actualCost
	return s.advance("failed"), nil
}

func (s *recordingSettlementIntentStore) checkVersion(id int64, version int32) error {
	if id != 71001 {
		return fmt.Errorf("intent id=%d want 71001", id)
	}
	if version != s.version {
		return fmt.Errorf("intent version=%d want %d", version, s.version)
	}
	return nil
}

func (s *recordingSettlementIntentStore) operationError(_ context.Context, operation string) error {
	if s.operationCalls == nil {
		s.operationCalls = make(map[string]int)
	}
	s.operationCalls[operation]++
	return s.operationErrs[operation]
}

func (s *recordingSettlementIntentStore) advance(status string) int32 {
	s.events = append(s.events, status)
	s.version++
	return s.version
}

// TestSettlementIntentSuccessfulRequestLifecycle 守住成功请求的 pending、真实交付和终态证据。
func TestSettlementIntentSuccessfulRequestLifecycle(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{}
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettlementIntents = intentStore
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: []byte(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`

	w := &timedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	serveSettlementIntentRequest(t, d, w, body, nil)
	rec := w.ResponseRecorder

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->settled" {
		t.Fatalf("intent lifecycle=%s want pending->delivering->settled", got)
	}
	if intentStore.firstByteAt.IsZero() {
		t.Fatal("delivering 必须写入非空 first_byte_at")
	}
	if w.firstWriteCompletedAt.IsZero() || intentStore.firstByteAt.Before(w.firstWriteCompletedAt) {
		t.Fatalf("first_byte_at=%s 早于业务写完成=%s", intentStore.firstByteAt, w.firstWriteCompletedAt)
	}
	if intentStore.settledAt.IsZero() {
		t.Fatal("settled 必须写入非空 settled_at")
	}
	wantCost := decimal.RequireFromString("0.008")
	if !intentStore.actualCost.Equal(wantCost) {
		t.Fatalf("intent actual_cost=%s want %s", intentStore.actualCost, wantCost)
	}
	if len(settler.calls) != 1 || !settler.calls[0].ActualCost.Equal(intentStore.actualCost) {
		t.Fatalf("主结算与意图金额不一致: settle=%+v intent=%s", settler.calls, intentStore.actualCost)
	}
	if intentStore.created.TenantID != validIdentity().TenantID ||
		intentStore.created.APIKeyID != validIdentity().APIKeyID ||
		intentStore.created.ClaimID != 999 ||
		intentStore.created.AttemptSeq != 1 ||
		intentStore.created.RequestID == "" ||
		intentStore.created.LogicalRequestID == "" ||
		intentStore.created.RequestFingerprint != payloadhash.Sum([]byte(body)) {
		t.Fatalf("pending 意图字段不完整: %+v", intentStore.created)
	}
}

// TestSettlementIntentInsertFailureFailsOpen 守住旁路降级语义，意图写失败不改变交付和主结算。
func TestSettlementIntentInsertFailureFailsOpen(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{insertErr: errors.New("注入的意图数据库错误")}
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettlementIntents = intentStore
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: []byte(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("intent 写失败不得阻断交付: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if intentStore.insertCalls != 1 {
		t.Fatalf("intent insert calls=%d want 1", intentStore.insertCalls)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("intent 写失败后主结算 calls=%d want 1", len(settler.calls))
	}
	if len(intentStore.events) != 0 {
		t.Fatalf("insert 失败后不得伪造状态迁移: %v", intentStore.events)
	}
}

// TestSettlementIntentSettleFailureBecomesSettling 守住已交付但 Tx2 失败的恢复态。
func TestSettlementIntentSettleFailureBecomesSettling(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{}
	settler := &postDeliveryFakeSettler{settleErr: errors.New("注入的 Tx2 错误")}
	recovery := &postDeliverySpyEnqueuer{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettleRecoveryDLQ = recovery
	d.SettlementIntents = intentStore
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: []byte(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("已交付后 settle 失败不得改写响应: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if recovery.calls != 1 {
		t.Fatalf("recovery enqueue calls=%d want 1", recovery.calls)
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->settling" {
		t.Fatalf("intent lifecycle=%s want pending->delivering->settling", got)
	}
	if wantCost := decimal.RequireFromString("0.008"); !intentStore.actualCost.Equal(wantCost) {
		t.Fatalf("settling actual_cost=%s want %s", intentStore.actualCost, wantCost)
	}
}

// TestSettlementIntentAbortMarksAborted 守住交付前主钱路 Abort 成功后的终态。
func TestSettlementIntentAbortMarksAborted(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{}
	settler := &recordingSettler{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{err: errors.New("注入的上游失败")}
	d.Settler = settler
	d.SettlementIntents = intentStore

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("测试夹具未触发失败路径: body=%s", rec.Body.String())
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1", len(settler.aborts))
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->aborted" {
		t.Fatalf("intent lifecycle=%s want pending->aborted", got)
	}
}

// TestSettlementIntentCacheHitLifecycle 守住 Reserve 后命中 L2 的零成本完整交付终态。
func TestSettlementIntentCacheHitLifecycle(t *testing.T) {
	enableHCSFDispatchForTest(t)
	cache := l2cache.NewMemoryStore(1<<20, time.Minute)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ResponseCache = cache
	first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("填充 cache 失败: status=%d body=%s", first.Code, first.Body.String())
	}

	intentStore := &recordingSettlementIntentStore{}
	settler := &recordingSettler{}
	secondDeps := clientAdapterDeps(t)
	secondDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	secondDeps.ResponseCache = cache
	secondDeps.Settler = settler
	secondDeps.SettlementIntents = intentStore
	second := invokeHandlerPath(t, secondDeps, "/v1/chat/completions", body)

	if second.Code != http.StatusOK || second.Header().Get("X-HUAKAI-Cache-L2") != "hit" {
		t.Fatalf("cache hit status/header=%d/%q body=%s", second.Code, second.Header().Get("X-HUAKAI-Cache-L2"), second.Body.String())
	}
	if len(settler.cacheHitCommits) != 1 {
		t.Fatalf("cache-hit commit calls=%d want 1", len(settler.cacheHitCommits))
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->settled" {
		t.Fatalf("cache-hit intent lifecycle=%s want pending->delivering->settled", got)
	}
	if !intentStore.actualCost.IsZero() {
		t.Fatalf("cache-hit intent actual_cost=%s want 0", intentStore.actualCost)
	}
}

// TestSettlementIntentUsesAuthoritativeReserveAttempt 守住意图身份直接采用账本 Reserve 返回值。
func TestSettlementIntentUsesAuthoritativeReserveAttempt(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{}
	d := clientAdapterDeps(t)
	d.ClaimGate = fixedAttemptClaimGate{claimID: 999, attemptSeq: 7}
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.SettlementIntents = intentStore

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if intentStore.created.AttemptSeq != 7 {
		t.Fatalf("intent attempt_seq=%d want ReserveResult authority 7", intentStore.created.AttemptSeq)
	}
}

// TestSettlementIntentDoubleFailureBecomesFailedWithActualCost 守住结算与恢复同时失败时的
// 终态和金额证据，恢复组件存在本身不能冒充成功入队。
func TestSettlementIntentDoubleFailureBecomesFailedWithActualCost(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{}
	settler := &postDeliveryFakeSettler{settleErr: errors.New("结算失败")}
	recovery := &postDeliverySpyEnqueuer{retErr: errors.New("恢复队列失败")}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.Settler = settler
	d.SettleRecoveryDLQ = recovery
	d.SettlementIntents = intentStore
	d.RateTables = &rateTableSourceStub{table: billing.RateTable{
		Version:     "test-policy",
		PricingData: []byte(`{"models":{"gpt-4o":{"input_micro_usd":1000,"output_micro_usd":2000}}}`),
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("双失败发生在交付后，status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if recovery.calls != 1 {
		t.Fatalf("recovery calls=%d want 1", recovery.calls)
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->failed" {
		t.Fatalf("intent lifecycle=%s want pending->delivering->failed", got)
	}
	wantCost := decimal.RequireFromString("0.008")
	if !intentStore.actualCost.Equal(wantCost) {
		t.Fatalf("failed actual_cost=%s want %s", intentStore.actualCost, wantCost)
	}
}

// TestSettlementIntentCacheHitWriteFailureHasNoDeliveryState 守住缓存响应只有整帧无错写出后
// 才能产生 delivering/settled 证据，账本提交与客户端交付分别表达。
func TestSettlementIntentCacheHitWriteFailureHasNoDeliveryState(t *testing.T) {
	enableHCSFDispatchForTest(t)
	cache := l2cache.NewMemoryStore(1<<20, time.Minute)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ResponseCache = cache
	first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", body)
	if first.Code != http.StatusOK {
		t.Fatalf("填充 cache 失败: status=%d body=%s", first.Code, first.Body.String())
	}

	intentStore := &recordingSettlementIntentStore{}
	settler := &recordingSettler{}
	secondDeps := clientAdapterDeps(t)
	secondDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	secondDeps.ResponseCache = cache
	secondDeps.Settler = settler
	secondDeps.SettlementIntents = intentStore
	w := &partialWriteResponseWriter{header: make(http.Header), limit: 1, err: io.ErrClosedPipe}
	serveSettlementIntentRequest(t, secondDeps, w, body, nil)

	if len(settler.cacheHitCommits) != 1 {
		t.Fatalf("cache-hit commit calls=%d want 1", len(settler.cacheHitCommits))
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending" {
		t.Fatalf("短写后的 intent lifecycle=%s want pending", got)
	}
	if intentStore.operationCalls["mark_delivering"] != 0 || intentStore.operationCalls["mark_settled"] != 0 {
		t.Fatalf("短写后不得调用交付终态: calls=%v", intentStore.operationCalls)
	}
}

// TestSettlementIntentPostAcquireCacheHitWriteFailureHasNoDeliveryState 守住已取得账号的
// 缓存结算分支同样以整帧写证据推进意图，不把账本成功误写成客户端交付成功。
func TestSettlementIntentPostAcquireCacheHitWriteFailureHasNoDeliveryState(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "development")
	ctx := context.Background()
	intentStore := &recordingSettlementIntentStore{}
	tracker := settlementintent.NewTracker(intentStore)
	tracker.InsertPending(ctx, validIdentity().TenantID, "req-cache-account", "logical-cache-account", 77994, 1, validIdentity().APIKeyID, "payload-cache-account", decimal.Zero)

	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "cache-account-response",
		Model:      "gpt-4o",
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "cached"}},
		StopReason: proto.CanonicalStopEndTurn,
	}
	envelope, ok := encodeL2CacheEnvelope(env)
	if !ok {
		t.Fatal("缓存 envelope 编码失败")
	}
	settler := &recordingSettler{}
	deps := clientAdapterDeps(t)
	deps.Settler = settler
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := &partialWriteResponseWriter{header: make(http.Header), limit: 1, err: io.ErrClosedPipe}
	served := serveL2CacheHit(ctx, w, req, deps, l2CacheHitInput{
		Entry: l2cache.Entry{
			Key:      "cache-account-key",
			TenantID: validIdentity().TenantID,
			Body:     []byte(`{"id":"cache-account-response"}`),
			Envelope: envelope,
		},
		Ident:            validIdentity(),
		ClientProtocol:   proto.ClientProtocolOpenAIChat,
		ProtocolFamily:   "openai_chat",
		RouteID:          "cache-account-route",
		RequestID:        "req-cache-account",
		AccountID:        1,
		AcquisitionToken: uuid.New(),
		PoolID:           "42",
		UpstreamModelID:  "gpt-4o",
		RequestedModel:   "gpt-4o",
		Provider:         "openai",
		RequestStartedAt: time.Now().UTC(),
		ReserveResult:    &billing.ReserveResult{ClaimID: 77994, AttemptSeq: 1},
		PayloadHash:      "payload-cache-account",
		AttemptSeq:       1,
		SettlementIntent: tracker,
	})

	if !served {
		t.Fatal("有效缓存条目未被处理")
	}
	if len(settler.calls) != 1 || len(settler.aborts) != 0 {
		t.Fatalf("cache settle/abort=%d/%d want 1/0", len(settler.calls), len(settler.aborts))
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending" {
		t.Fatalf("短写后的 intent lifecycle=%s want pending", got)
	}
}

// TestStreamingHandlerSettlementIntentLifecycle 从 stream=true 的 HTTP 入口验证首帧接线、
// 正常终态、恢复态和首帧写失败分流。
func TestStreamingHandlerSettlementIntentLifecycle(t *testing.T) {
	t.Run("settled", func(t *testing.T) {
		intentStore := &recordingSettlementIntentStore{}
		deps := streamingReplayDeps(t, 77991, false, openAIStreamingFixture(), nil)
		deps.SettlementIntents = intentStore
		w := &timedResponseWriter{ResponseRecorder: httptest.NewRecorder()}

		serveSettlementIntentRequest(t, deps, w, openAIStreamingRequestBody(), nil)

		if w.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%s", w.Code, w.Body.String())
		}
		if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->settled" {
			t.Fatalf("stream intent lifecycle=%s want pending->delivering->settled", got)
		}
		if w.firstWriteCompletedAt.IsZero() || intentStore.firstByteAt.Before(w.firstWriteCompletedAt) {
			t.Fatalf("stream first_byte_at=%s 早于首帧写完成=%s", intentStore.firstByteAt, w.firstWriteCompletedAt)
		}
	})

	t.Run("settling", func(t *testing.T) {
		intentStore := &recordingSettlementIntentStore{}
		deps := streamingReplayDeps(t, 77992, false, openAIStreamingFixture(), nil)
		deps.Settler = &postDeliveryFakeSettler{settleErr: errors.New("结算失败")}
		deps.SettleRecoveryDLQ = &postDeliverySpyEnqueuer{}
		deps.SettlementIntents = intentStore

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())

		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
		}
		if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->settling" {
			t.Fatalf("stream intent lifecycle=%s want pending->delivering->settling", got)
		}
	})

	t.Run("first_frame_write_failure", func(t *testing.T) {
		intentStore := &recordingSettlementIntentStore{}
		settler := &recordingSettler{}
		deps := streamingReplayDeps(t, 77993, false, openAIStreamingFixture(), nil)
		deps.Settler = settler
		deps.SettlementIntents = intentStore
		w := &streamScriptedWriter{results: []streamWriteResult{{written: 1, err: io.ErrClosedPipe}}}

		serveSettlementIntentRequest(t, deps, w, openAIStreamingRequestBody(), nil)

		if len(settler.calls) != 0 || len(settler.aborts) != 1 {
			t.Fatalf("首帧写失败 settle/abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
		}
		for _, state := range intentStore.events {
			if state == "delivering" || state == "settled" || state == "settling" {
				t.Fatalf("首帧写失败不得出现交付态: %v", intentStore.events)
			}
		}
	})
}

// TestStreamingDeliveryDoesNotWaitForSlowIntentStore 守住首帧后的旁路数据库停顿不延迟
// 后续业务帧，终态仍等待旁路尝试结束以保持乐观锁顺序。
func TestStreamingDeliveryDoesNotWaitForSlowIntentStore(t *testing.T) {
	logs := captureSlogForTest(t)
	store := &waitingDeliveringSettlementIntentStore{
		recordingSettlementIntentStore: &recordingSettlementIntentStore{},
	}
	settler := &recordingSettler{}
	deps := streamingReplayDeps(t, 77995, false, openAIStreamingFixture(), nil)
	deps.Settler = settler
	deps.SettlementIntents = store
	w := &writeTimelineResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	startedAt := time.Now()

	serveSettlementIntentRequest(t, deps, w, openAIStreamingRequestBody(), nil)

	if w.Code != http.StatusOK || len(settler.calls) != 1 {
		t.Fatalf("status/settles=%d/%d want 200/1", w.Code, len(settler.calls))
	}
	if len(w.completedAt) < 2 {
		t.Fatalf("业务写次数=%d want >=2", len(w.completedAt))
	}
	if gap := w.completedAt[len(w.completedAt)-1].Sub(w.completedAt[0]); gap >= 75*time.Millisecond {
		t.Fatalf("慢意图 Store 延迟了后续流式帧: 首尾写间隔=%s", gap)
	}
	if elapsed := time.Since(startedAt); elapsed < 90*time.Millisecond {
		t.Fatalf("夹具未实际等待意图短超时: 总耗时=%s", elapsed)
	}
	if got := strings.Join(store.events, "->"); got != "pending->settled" {
		t.Fatalf("慢 delivering 后 intent lifecycle=%s want pending->settled", got)
	}
	assertSettlementIntentWarningSanitized(t, logs.String(), "mark_delivering")
}

// TestSettlementIntentPreDeliveryWritesRespectCancellation 守住交付前旁路写使用请求取消
// 和短预算，数据库停顿不得无限拖住主响应。
func TestSettlementIntentPreDeliveryWritesRespectCancellation(t *testing.T) {
	tests := []struct {
		name       string
		cancelSoon bool
	}{
		{name: "short_timeout"},
		{name: "request_cancel", cancelSoon: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enableHCSFDispatchForTest(t)
			logs := captureSlogForTest(t)
			store := &waitingInsertSettlementIntentStore{
				recordingSettlementIntentStore: &recordingSettlementIntentStore{},
				started:                        make(chan struct{}),
			}
			settler := &recordingSettler{}
			deps := clientAdapterDeps(t)
			deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
			deps.Settler = settler
			deps.SettlementIntents = store
			body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			startedAt := time.Now()
			go func() {
				serveSettlementIntentRequest(t, deps, rec, body, ctx)
				close(done)
			}()

			select {
			case <-store.started:
			case <-time.After(time.Second):
				t.Fatal("意图 Insert 未开始")
			}
			var canceledAt time.Time
			if tc.cancelSoon {
				canceledAt = time.Now()
				cancel()
			}
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("旁路意图写拖住主请求")
			}
			if elapsed := time.Since(startedAt); elapsed >= 500*time.Millisecond {
				t.Fatalf("请求耗时=%s，旁路写未受短预算约束", elapsed)
			}
			if tc.cancelSoon && time.Since(canceledAt) >= 75*time.Millisecond {
				t.Fatalf("请求取消后旁路写仍等待 %s", time.Since(canceledAt))
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
			}
			if len(settler.calls) != 1 || len(settler.aborts) != 0 {
				t.Fatalf("主结算/Abort=%d/%d want 1/0", len(settler.calls), len(settler.aborts))
			}
			if store.deadline.IsZero() {
				t.Fatal("交付前写上下文缺少短 deadline")
			}
			assertSettlementIntentWarningSanitized(t, logs.String(), "insert_pending")
		})
	}
}

// TestSettlementIntentDeliveringRespectsCancellation 守住首帧写出后若请求已取消，
// delivering 旁路立即放弃，而终态仍使用独立有界上下文完成。
func TestSettlementIntentDeliveringRespectsCancellation(t *testing.T) {
	enableHCSFDispatchForTest(t)
	logs := captureSlogForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	baseStore := &recordingSettlementIntentStore{}
	store := &cancelAfterInsertSettlementIntentStore{
		recordingSettlementIntentStore: baseStore,
		cancel:                         cancel,
	}
	settler := &recordingSettler{}
	deps := clientAdapterDeps(t)
	deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	deps.Settler = settler
	deps.SettlementIntents = store
	rec := httptest.NewRecorder()

	serveSettlementIntentRequest(t, deps, rec, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`, ctx)

	if rec.Code != http.StatusOK || len(settler.calls) != 1 {
		t.Fatalf("status/settles=%d/%d want 200/1", rec.Code, len(settler.calls))
	}
	if baseStore.operationCalls["mark_delivering"] != 0 {
		t.Fatalf("取消后 delivering store calls=%d want 0", baseStore.operationCalls["mark_delivering"])
	}
	if baseStore.operationCalls["mark_settled"] != 1 {
		t.Fatalf("取消后 terminal store calls=%d want 1", baseStore.operationCalls["mark_settled"])
	}
	if got := strings.Join(baseStore.events, "->"); got != "pending->settled" {
		t.Fatalf("取消后 intent lifecycle=%s want pending->settled", got)
	}
	assertSettlementIntentWarningSanitized(t, logs.String(), "mark_delivering")
}

// TestSettlementIntentOperationsFailOpen 覆盖每个状态写的普通错误和超时错误，旁路失败
// 只能留下脱敏 warning，不能改变原有结算或 Abort 决策。
func TestSettlementIntentOperationsFailOpen(t *testing.T) {
	operations := []string{
		"insert_pending",
		"mark_delivering",
		"mark_settling",
		"mark_settled",
		"mark_aborted",
		"mark_failed",
	}
	modes := []struct {
		name string
		err  error
	}{
		{name: "error", err: errors.New("原始数据库错误 sk-private-key")},
		{name: "timeout", err: context.DeadlineExceeded},
	}

	for _, operation := range operations {
		for _, mode := range modes {
			t.Run(operation+"_"+mode.name, func(t *testing.T) {
				enableHCSFDispatchForTest(t)
				logs := captureSlogForTest(t)
				store := &recordingSettlementIntentStore{operationErrs: map[string]error{operation: mode.err}}
				settler := &failingSettleSettler{}
				recovery := &postDeliverySpyEnqueuer{}
				deps := clientAdapterDeps(t)
				deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
				deps.Settler = settler
				deps.SettleRecoveryDLQ = recovery
				deps.SettlementIntents = store

				switch operation {
				case "mark_settling":
					settler.err = errors.New("主结算失败")
				case "mark_failed":
					settler.err = errors.New("主结算失败")
					recovery.retErr = errors.New("恢复队列失败")
				case "mark_aborted":
					deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{err: errors.New("上游失败")}
				}

				rec := invokeHandlerPath(t, deps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

				if operation == "mark_aborted" {
					if rec.Code == http.StatusOK {
						t.Fatalf("上游失败的主响应不得因意图旁路改成 200: body=%s", rec.Body.String())
					}
					if len(settler.calls) != 0 || len(settler.aborts) != 1 {
						t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
					}
				} else {
					if rec.Code != http.StatusOK {
						t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
					}
					if len(settler.calls) != 1 || len(settler.aborts) != 0 {
						t.Fatalf("主结算/Abort=%d/%d want 1/0", len(settler.calls), len(settler.aborts))
					}
				}
				if store.operationCalls[operation] != 1 {
					t.Fatalf("operation %s calls=%d want 1", operation, store.operationCalls[operation])
				}
				assertSettlementIntentWarningSanitized(t, logs.String(), operation)
			})
		}
	}
}

// TestSettlementIntentStoreBoundaryFaultsFailOpen 守住六种旁路写在真实停顿或 panic
// 下仍不改变流式、非流式请求的交付结果和主钱路调用。
func TestSettlementIntentStoreBoundaryFaultsFailOpen(t *testing.T) {
	paths := []struct {
		name   string
		stream bool
	}{
		{name: "non_stream"},
		{name: "stream", stream: true},
	}
	operations := []string{
		"insert_pending",
		"mark_delivering",
		"mark_settling",
		"mark_settled",
		"mark_aborted",
		"mark_failed",
	}
	modes := []settlementIntentFaultMode{
		settlementIntentFaultBlock,
		settlementIntentFaultPanic,
	}

	for _, path := range paths {
		for _, operation := range operations {
			for _, mode := range modes {
				t.Run(path.name+"/"+operation+"/"+string(mode), func(t *testing.T) {
					logs := captureConcurrentSlogForTest(t)
					store := newSettlementIntentFaultStore(operation, mode)
					defer store.releaseAndWait(t)
					deps, body, settler := settlementIntentFaultRequestDeps(t, path.stream, operation, store)

					rec, elapsed := runSettlementIntentFaultRequest(t, deps, body, store)

					if operation == "mark_aborted" {
						if rec.Code == http.StatusOK {
							t.Fatalf("上游失败路径被旁路故障改成成功响应: body=%s", rec.Body.String())
						}
						if len(settler.calls) != 0 || len(settler.aborts) != 1 {
							t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
						}
					} else {
						if rec.Code != http.StatusOK {
							t.Fatalf("旁路故障阻断交付: status=%d body=%s", rec.Code, rec.Body.String())
						}
						if len(settler.calls) != 1 || len(settler.aborts) != 0 {
							t.Fatalf("主结算/Abort=%d/%d want 1/0", len(settler.calls), len(settler.aborts))
						}
					}
					if calls := store.faultCalls.Load(); calls != 1 {
						t.Fatalf("operation %s calls=%d want 1", operation, calls)
					}
					if upper := settlementIntentFaultElapsedUpper(operation, mode); elapsed >= upper {
						t.Fatalf("旁路故障使请求超过硬上限: operation=%s mode=%s elapsed=%s upper=%s", operation, mode, elapsed, upper)
					}
					assertSettlementIntentWarningSanitized(t, logs.String(), operation)
				})
			}
		}
	}
}

// TestSettlementIntentDeliveringCannotDelayMoneyPath 守住客户端已收到业务数据后，
// 主结算先于受阻的 delivering 旁路完成，流式和非流式遵守同一优先级。
func TestSettlementIntentDeliveringCannotDelayMoneyPath(t *testing.T) {
	paths := []struct {
		name   string
		stream bool
	}{
		{name: "non_stream"},
		{name: "stream", stream: true},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			store := newSettlementIntentFaultStore("mark_delivering", settlementIntentFaultBlock)
			defer store.releaseAndWait(t)
			settler := newSettlementIntentSignalSettler()
			deps, body := settlementIntentSignalRequestDeps(t, path.stream, store, settler)
			rec := httptest.NewRecorder()
			done := serveSettlementIntentRequestAsync(deps, rec, body)

			select {
			case <-store.started:
			case <-time.After(time.Second):
				t.Fatal("delivering Store 未进入停顿点")
			}
			select {
			case <-settler.settleStarted:
			case <-time.After(250 * time.Millisecond):
				t.Fatal("主结算在等待旁路 delivering")
			}

			store.release()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("旁路恢复后请求仍未返回")
			}
			if rec.Code != http.StatusOK || len(settler.calls) != 1 {
				t.Fatalf("status/settles=%d/%d want 200/1", rec.Code, len(settler.calls))
			}
		})
	}
}

// TestSettlementIntentQuotaDenyClosesInsertedIntent 守住账本 Reserve 成功而配额拒绝时，
// intent 具有完整身份并随主 Abort 进入终态，不留下 pending 孤儿或零身份告警。
func TestSettlementIntentQuotaDenyClosesInsertedIntent(t *testing.T) {
	enableHCSFDispatchForTest(t)
	logs := captureSlogForTest(t)
	store := &recordingSettlementIntentStore{}
	settler := &recordingSettler{}
	dispatcher := &mockCanonicalBufferedDispatcher{}
	deps := clientAdapterDeps(t)
	deps.ClaimGate = &recordingClaimGate{claimID: 99021}
	deps.QuotaReserver = &recordingQuotaReserver{err: &quota.DenyError{Decision: quota.Decision{
		Kind:   quota.DecisionDeny,
		Code:   "quota_limit_exceeded",
		Reason: "测试配额拒绝",
	}}}
	deps.Settler = settler
	deps.CanonicalDispatcher = dispatcher
	deps.SettlementIntents = store

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429 body=%s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 0 || len(settler.calls) != 0 || len(settler.aborts) != 1 {
		t.Fatalf("dispatch/settle/abort=%d/%d/%d want 0/0/1", dispatcher.calls, len(settler.calls), len(settler.aborts))
	}
	if store.insertCalls != 1 || strings.Join(store.events, "->") != "pending->aborted" {
		t.Fatalf("quota deny intent insert/events=%d/%v want 1/pending->aborted", store.insertCalls, store.events)
	}
	if store.created.TenantID == 0 || store.created.RequestID == "" || store.created.ClaimID != 99021 {
		t.Fatalf("quota deny intent 身份不完整: %+v", store.created)
	}
	if strings.Contains(logs.String(), "结算意图旁路写失败") {
		t.Fatalf("quota deny 正常生命周期不应产生旁路告警: %s", logs.String())
	}
}

// TestStreamingSettlementIntentDisabledHasNoTransitions 守住 stream=true 默认关闭时，
// 响应与主结算照常完成，且旁路不产生状态或告警。
func TestStreamingSettlementIntentDisabledHasNoTransitions(t *testing.T) {
	logs := captureSlogForTest(t)
	settler := &recordingSettler{}
	deps := streamingReplayDeps(t, 99022, false, openAIStreamingFixture(), nil)
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())

	if rec.Code != http.StatusOK || len(settler.calls) != 1 || len(settler.aborts) != 0 {
		t.Fatalf("status/settle/abort=%d/%d/%d want 200/1/0", rec.Code, len(settler.calls), len(settler.aborts))
	}
	if strings.Contains(logs.String(), "结算意图旁路写失败") {
		t.Fatalf("默认关闭不应产生旁路状态告警: %s", logs.String())
	}
}

// TestSettlementIntentMissingStoreAndZeroIDFailOpen 区分默认关闭与启用态接线故障，
// 并守住零标识不会伪造状态迁移。
func TestSettlementIntentMissingStoreAndZeroIDFailOpen(t *testing.T) {
	t.Run("enabled_nil_store", func(t *testing.T) {
		enableHCSFDispatchForTest(t)
		logs := captureSlogForTest(t)
		settler := &recordingSettler{}
		deps := clientAdapterDeps(t)
		deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		deps.Settler = settler
		deps.SettlementIntentEnabled = true
		deps.SettlementIntents = nil

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

		if rec.Code != http.StatusOK || len(settler.calls) != 1 {
			t.Fatalf("enabled nil store status/settles=%d/%d want 200/1", rec.Code, len(settler.calls))
		}
		assertSettlementIntentWarningSanitized(t, logs.String(), "insert_pending")
	})

	t.Run("disabled_nil_store", func(t *testing.T) {
		enableHCSFDispatchForTest(t)
		logs := captureSlogForTest(t)
		settler := &recordingSettler{}
		deps := clientAdapterDeps(t)
		deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		deps.Settler = settler

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

		if rec.Code != http.StatusOK || len(settler.calls) != 1 {
			t.Fatalf("disabled nil store status/settles=%d/%d want 200/1", rec.Code, len(settler.calls))
		}
		if strings.Contains(logs.String(), "结算意图旁路写失败") {
			t.Fatalf("默认关闭不应产生意图告警: %s", logs.String())
		}
	})

	t.Run("zero_id", func(t *testing.T) {
		enableHCSFDispatchForTest(t)
		logs := captureSlogForTest(t)
		store := &recordingSettlementIntentStore{returnZeroID: true}
		settler := &recordingSettler{}
		deps := clientAdapterDeps(t)
		deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		deps.Settler = settler
		deps.SettlementIntents = store

		rec := invokeHandlerPath(t, deps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

		if rec.Code != http.StatusOK || len(settler.calls) != 1 {
			t.Fatalf("zero id status/settles=%d/%d want 200/1", rec.Code, len(settler.calls))
		}
		if len(store.events) != 0 {
			t.Fatalf("zero id 不得伪造状态迁移: %v", store.events)
		}
		assertSettlementIntentWarningSanitized(t, logs.String(), "insert_pending")
		assertSettlementIntentWarningSanitized(t, logs.String(), "mark_delivering")
	})
}

// TestSettlementIntentTestCommentsDescribeInvariants 守住本切片测试注释只描述业务不变量
// 和失败场景，不混入开发过程叙述。
func TestSettlementIntentTestCommentsDescribeInvariants(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	backendRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	paths := []string{
		"internal/adminquotahttp/routes_test.go",
		"internal/settlementintent/tracker_test.go",
		"internal/gatewayhttp/settlement_intent_test.go",
		"internal/gatewayhttp/post_delivery_recovery_test.go",
		"internal/gateway/forwarder_settlement_intent_test.go",
		"internal/db/settlement_intents_integration_test.go",
		"cmd/gateway/settlement_intent_wiring_test.go",
	}
	for _, relativePath := range paths {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(backendRoot, relativePath), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 %s: %v", relativePath, err)
		}
		for _, group := range parsed.Comments {
			comment := group.Text()
			for _, forbidden := range []string{"变异", "删除", "会变红", "任务号", "Owner", "借鉴项目"} {
				if strings.Contains(comment, forbidden) {
					t.Fatalf("%s 的测试注释含开发过程词 %q: %s", relativePath, forbidden, comment)
				}
			}
		}
	}
}

type fixedAttemptClaimGate struct {
	claimID    int64
	attemptSeq int32
}

func (g fixedAttemptClaimGate) Reserve(context.Context, billing.ReserveRequest) (*billing.ReserveResult, error) {
	return &billing.ReserveResult{ClaimID: g.claimID, AttemptSeq: g.attemptSeq}, nil
}

type timedResponseWriter struct {
	*httptest.ResponseRecorder
	firstWriteCompletedAt time.Time
}

type writeTimelineResponseWriter struct {
	*httptest.ResponseRecorder
	completedAt []time.Time
}

func (w *writeTimelineResponseWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(body)
	if n > 0 {
		w.completedAt = append(w.completedAt, time.Now().UTC())
	}
	return n, err
}

func (w *timedResponseWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(body)
	if w.firstWriteCompletedAt.IsZero() && n > 0 {
		w.firstWriteCompletedAt = time.Now().UTC()
	}
	return n, err
}

type waitingInsertSettlementIntentStore struct {
	*recordingSettlementIntentStore
	started  chan struct{}
	deadline time.Time
}

type waitingDeliveringSettlementIntentStore struct {
	*recordingSettlementIntentStore
}

func (s *waitingDeliveringSettlementIntentStore) MarkDelivering(ctx context.Context, _ int64, _ int32, _ time.Time) (int32, error) {
	if s.operationCalls == nil {
		s.operationCalls = make(map[string]int)
	}
	s.operationCalls["mark_delivering"]++
	<-ctx.Done()
	return 0, ctx.Err()
}

type cancelAfterInsertSettlementIntentStore struct {
	*recordingSettlementIntentStore
	cancel context.CancelFunc
}

type settlementIntentFaultMode string

const (
	settlementIntentFaultBlock settlementIntentFaultMode = "block_ignoring_context"
	settlementIntentFaultPanic settlementIntentFaultMode = "panic"
)

type settlementIntentFaultStore struct {
	base          *recordingSettlementIntentStore
	operation     string
	mode          settlementIntentFaultMode
	faultCalls    atomic.Int32
	started       chan struct{}
	releaseBlock  chan struct{}
	completed     chan struct{}
	startedOnce   sync.Once
	releaseOnce   sync.Once
	completedOnce sync.Once
}

func newSettlementIntentFaultStore(operation string, mode settlementIntentFaultMode) *settlementIntentFaultStore {
	return &settlementIntentFaultStore{
		base:         &recordingSettlementIntentStore{},
		operation:    operation,
		mode:         mode,
		started:      make(chan struct{}),
		releaseBlock: make(chan struct{}),
		completed:    make(chan struct{}),
	}
}

func (s *settlementIntentFaultStore) fault(operation string) bool {
	if operation != s.operation {
		return false
	}
	s.faultCalls.Add(1)
	s.startedOnce.Do(func() { close(s.started) })
	defer s.completedOnce.Do(func() { close(s.completed) })
	if s.mode == settlementIntentFaultPanic {
		panic("panic-secret sk-private-key")
	}
	<-s.releaseBlock
	return true
}

func (s *settlementIntentFaultStore) Insert(ctx context.Context, in settlementintent.CreateParams) (int64, error) {
	if s.fault("insert_pending") {
		return 71001, nil
	}
	return s.base.Insert(ctx, in)
}

func (s *settlementIntentFaultStore) MarkDelivering(ctx context.Context, id int64, version int32, firstByteAt time.Time) (int32, error) {
	if s.fault("mark_delivering") {
		return version + 1, nil
	}
	return s.base.MarkDelivering(ctx, id, version, firstByteAt)
}

func (s *settlementIntentFaultStore) MarkSettling(ctx context.Context, id int64, version int32, actualCost decimal.Decimal) (int32, error) {
	if s.fault("mark_settling") {
		return version + 1, nil
	}
	return s.base.MarkSettling(ctx, id, version, actualCost)
}

func (s *settlementIntentFaultStore) MarkSettled(ctx context.Context, id int64, version int32, actualCost decimal.Decimal, settledAt time.Time) (int32, error) {
	if s.fault("mark_settled") {
		return version + 1, nil
	}
	return s.base.MarkSettled(ctx, id, version, actualCost, settledAt)
}

func (s *settlementIntentFaultStore) MarkAborted(ctx context.Context, id int64, version int32) (int32, error) {
	if s.fault("mark_aborted") {
		return version + 1, nil
	}
	return s.base.MarkAborted(ctx, id, version)
}

func (s *settlementIntentFaultStore) MarkFailed(ctx context.Context, id int64, version int32, actualCost decimal.Decimal) (int32, error) {
	if s.fault("mark_failed") {
		return version + 1, nil
	}
	return s.base.MarkFailed(ctx, id, version, actualCost)
}

func (s *settlementIntentFaultStore) release() {
	s.releaseOnce.Do(func() { close(s.releaseBlock) })
}

func (s *settlementIntentFaultStore) releaseAndWait(t *testing.T) {
	t.Helper()
	if s.faultCalls.Load() == 0 {
		return
	}
	s.release()
	select {
	case <-s.completed:
	case <-time.After(time.Second):
		t.Errorf("旁路 Store 故障夹具未退出: operation=%s mode=%s", s.operation, s.mode)
	}
	// Store 返回后只剩结果投递和状态快照核对，给异步回收留出调度机会。
	time.Sleep(10 * time.Millisecond)
}

type settlementIntentSignalSettler struct {
	failingSettleSettler
	settleStarted chan struct{}
	settleOnce    sync.Once
}

func newSettlementIntentSignalSettler() *settlementIntentSignalSettler {
	return &settlementIntentSignalSettler{settleStarted: make(chan struct{})}
}

func (s *settlementIntentSignalSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.settleOnce.Do(func() { close(s.settleStarted) })
	return s.failingSettleSettler.Settle(ctx, req)
}

type synchronizedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureConcurrentSlogForTest(t *testing.T) *synchronizedLogBuffer {
	t.Helper()
	logs := &synchronizedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func settlementIntentFaultRequestDeps(t *testing.T, stream bool, operation string, store settlementintent.Store) (ChatHandlerDeps, string, *failingSettleSettler) {
	t.Helper()
	enableHCSFDispatchForTest(t)
	settler := &failingSettleSettler{}
	recovery := &postDeliverySpyEnqueuer{}
	var deps ChatHandlerDeps
	var body string
	if stream {
		deps = streamingReplayDeps(t, 99031, false, openAIStreamingFixture(), nil)
		body = openAIStreamingRequestBody()
	} else {
		deps = clientAdapterDeps(t)
		deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
		body = `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	}
	deps.Settler = settler
	deps.SettleRecoveryDLQ = recovery
	deps.SettlementIntents = store

	switch operation {
	case "mark_settling":
		settler.err = errors.New("主结算失败")
	case "mark_failed":
		settler.err = errors.New("主结算失败")
		recovery.retErr = errors.New("恢复队列失败")
	case "mark_aborted":
		if stream {
			deps.Dispatcher.HTTPClient = &erroringStreamingDoer{err: errors.New("上游读取失败")}
		} else {
			deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{err: errors.New("上游失败")}
		}
	}
	return deps, body, settler
}

func settlementIntentSignalRequestDeps(t *testing.T, stream bool, store settlementintent.Store, settler billing.Settler) (ChatHandlerDeps, string) {
	t.Helper()
	enableHCSFDispatchForTest(t)
	if stream {
		deps := streamingReplayDeps(t, 99032, false, openAIStreamingFixture(), nil)
		deps.Settler = settler
		deps.SettlementIntents = store
		return deps, openAIStreamingRequestBody()
	}
	deps := clientAdapterDeps(t)
	deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	deps.Settler = settler
	deps.SettlementIntents = store
	return deps, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
}

func settlementIntentFaultElapsedUpper(operation string, mode settlementIntentFaultMode) time.Duration {
	if mode == settlementIntentFaultPanic {
		return time.Second
	}
	if operation == "insert_pending" || operation == "mark_delivering" {
		return 1500 * time.Millisecond
	}
	return 3 * time.Second
}

func runSettlementIntentFaultRequest(t *testing.T, deps ChatHandlerDeps, body string, store *settlementIntentFaultStore) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	rec := httptest.NewRecorder()
	startedAt := time.Now()
	done := serveSettlementIntentRequestAsync(deps, rec, body)
	select {
	case <-done:
		return rec, time.Since(startedAt)
	case <-time.After(4 * time.Second):
		store.release()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatalf("旁路 Store 忽略 ctx 后请求未在独立硬上限内返回: operation=%s mode=%s", store.operation, store.mode)
		return nil, 0
	}
}

func serveSettlementIntentRequestAsync(deps ChatHandlerDeps, rec *httptest.ResponseRecorder, body string) <-chan struct{} {
	done := make(chan struct{})
	handler := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	go func() {
		defer close(done)
		handler.ServeHTTP(rec, req)
	}()
	return done
}

func (s *cancelAfterInsertSettlementIntentStore) Insert(ctx context.Context, in settlementintent.CreateParams) (int64, error) {
	id, err := s.recordingSettlementIntentStore.Insert(ctx, in)
	if err == nil && s.cancel != nil {
		s.cancel()
	}
	return id, err
}

func (s *waitingInsertSettlementIntentStore) Insert(ctx context.Context, in settlementintent.CreateParams) (int64, error) {
	s.insertCalls++
	s.created = in
	if deadline, ok := ctx.Deadline(); ok {
		s.deadline = deadline
	}
	close(s.started)
	<-ctx.Done()
	return 0, ctx.Err()
}

func serveSettlementIntentRequest(t *testing.T, deps ChatHandlerDeps, w http.ResponseWriter, body string, ctx context.Context) {
	t.Helper()
	h := NewChatCompletionsHandler(deps)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	h.ServeHTTP(w, req)
}

func assertSettlementIntentWarningSanitized(t *testing.T, logs, operation string) {
	t.Helper()
	for _, want := range []string{"结算意图旁路写失败", operation, "tenant_id", "request_id", "claim_id", "error_type"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("意图 warning 缺少 %q: %s", want, logs)
		}
	}
	for _, forbidden := range []string{"原始数据库错误", "panic-secret", "sk-private-key"} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("意图 warning 泄漏 %q: %s", forbidden, logs)
		}
	}
}
