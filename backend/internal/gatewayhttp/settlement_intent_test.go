package gatewayhttp

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/payloadhash"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/quota"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

type recordingSettlementIntentStore struct {
	insertErr       error
	operationErrs   map[string]error
	operationCalls  map[string]int
	returnZeroID    bool
	insertCalls     int
	created         settlementintent.CreateParams
	events          []string
	version         int32
	firstByteAt     time.Time
	actualCost      decimal.Decimal
	settledAt       time.Time
	recoveryPayload json.RawMessage
	recoveryClass   string
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

func (s *recordingSettlementIntentStore) MarkRecoveryPending(
	ctx context.Context,
	id int64,
	version int32,
	actualCost decimal.Decimal,
	payload json.RawMessage,
	failureClass string,
) (int32, error) {
	if err := s.operationError(ctx, "mark_recovery_pending"); err != nil {
		return 0, err
	}
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	s.actualCost = actualCost
	s.recoveryPayload = append(json.RawMessage(nil), payload...)
	s.recoveryClass = failureClass
	return s.advance("failed"), nil
}

func (s *recordingSettlementIntentStore) ListStaleNonTerminalSettlementIntents(context.Context, time.Time, time.Time, int32) ([]settlementintent.StaleSettlementIntent, error) {
	return nil, nil
}

func (s *recordingSettlementIntentStore) MarkSettledIfStale(ctx context.Context, id int64, version int32, actualCost decimal.Decimal, settledAt time.Time) (int32, error) {
	return s.MarkSettled(ctx, id, version, actualCost, settledAt)
}

func (s *recordingSettlementIntentStore) MarkAbortedIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	return s.MarkAborted(ctx, id, version)
}

func (s *recordingSettlementIntentStore) MarkSupersededIfStale(_ context.Context, id int64, version int32) (int32, error) {
	if err := s.checkVersion(id, version); err != nil {
		return 0, err
	}
	return s.advance("superseded"), nil
}

func (s *recordingSettlementIntentStore) MarkSettlingIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	return s.MarkSettling(ctx, id, version, s.actualCost)
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
	if w.firstWriteCompletedAt.IsZero() || intentStore.firstByteAt.After(w.firstWriteCompletedAt) {
		t.Fatalf("first_byte_at=%s 晚于业务写完成=%s", intentStore.firstByteAt, w.firstWriteCompletedAt)
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

// TestSettlementIntentInsertFailureFailsClosed 守住交付前恢复证据写失败时不发起上游请求，
// 并释放已经创建的 claim，避免形成“响应已交付但无恢复事实”的资金黑洞。
func TestSettlementIntentInsertFailureFailsClosed(t *testing.T) {
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

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("intent 写失败 status=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
	if intentStore.insertCalls != 1 {
		t.Fatalf("intent insert calls=%d want 1", intentStore.insertCalls)
	}
	if len(settler.calls) != 0 || len(settler.aborts) != 1 {
		t.Fatalf("intent 写失败后 settle/abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
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
	if got := strings.Join(intentStore.events, "->"); got != "pending->settled" {
		t.Fatalf("cache-hit intent lifecycle=%s want pending->settled", got)
	}
	if !intentStore.actualCost.IsZero() {
		t.Fatalf("cache-hit intent actual_cost=%s want 0", intentStore.actualCost)
	}
}

// TestSettlementIntentCacheHitCommitFailureAbortsBeforeDelivery 守住未取得账号的
// L2 命中在零成本提交失败时不会被写成“已交付待恢复”；响应体尚未发送，必须先中止 claim。
func TestSettlementIntentCacheHitCommitFailureAbortsBeforeDelivery(t *testing.T) {
	enableHCSFDispatchForTest(t)
	cache := l2cache.NewMemoryStore(1<<20, time.Minute)
	body := `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`
	firstDeps := clientAdapterDeps(t)
	firstDeps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	firstDeps.ResponseCache = cache
	if first := invokeHandlerPath(t, firstDeps, "/v1/chat/completions", body); first.Code != http.StatusOK {
		t.Fatalf("填充 cache 失败: status=%d body=%s", first.Code, first.Body.String())
	}

	intentStore := &recordingSettlementIntentStore{}
	settler := &recordingSettler{cacheHitErr: errors.New("注入的 cache commit 失败")}
	deps := clientAdapterDeps(t)
	deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	deps.ResponseCache = cache
	deps.Settler = settler
	deps.SettlementIntents = intentStore

	rec := invokeHandlerPath(t, deps, "/v1/chat/completions", body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.cacheHitCommits) != 1 || len(settler.aborts) != 1 {
		t.Fatalf("commit/abort=%d/%d want 1/1", len(settler.cacheHitCommits), len(settler.aborts))
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->aborted" {
		t.Fatalf("intent lifecycle=%s want pending->aborted", got)
	}
	if intentStore.operationCalls["mark_failed"] != 0 {
		t.Fatalf("交付前失败不得写 failed: calls=%v", intentStore.operationCalls)
	}
}

// TestSettlementIntentPostAcquireCacheSettleFailureAbortsBeforeDelivery 守住已取得账号的
// 缓存命中同样只在响应体交付后才允许进入结算恢复；前置 settle 失败应中止 claim。
func TestSettlementIntentPostAcquireCacheSettleFailureAbortsBeforeDelivery(t *testing.T) {
	t.Setenv("HUAKAI_RELEASE_MODE", "development")
	ctx := context.Background()
	intentStore := &recordingSettlementIntentStore{}
	tracker := settlementintent.NewTracker(intentStore)
	tracker.InsertPending(ctx, validIdentity().TenantID, "req-cache-settle-fail", "logical-cache-settle-fail", 77995, 1, validIdentity().APIKeyID, "payload-cache-settle-fail", decimal.Zero)

	env := proto.NewEmptyEnvelope()
	env.BufferedResponse = &proto.CanonicalResponse{
		ID:         "cache-settle-fail-response",
		Model:      "gpt-4o",
		Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "cached"}},
		StopReason: proto.CanonicalStopEndTurn,
	}
	envelope, ok := encodeL2CacheEnvelope(env)
	if !ok {
		t.Fatal("缓存 envelope 编码失败")
	}
	settler := &recordingSettler{settleErr: errors.New("注入的 cache settle 失败")}
	deps := clientAdapterDeps(t)
	deps.Settler = settler
	rec := httptest.NewRecorder()
	served := serveL2CacheHit(ctx, rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), deps, l2CacheHitInput{
		Entry: l2cache.Entry{
			Key:      "cache-settle-fail-key",
			TenantID: validIdentity().TenantID,
			Body:     []byte(`{"id":"cache-settle-fail-response"}`),
			Envelope: envelope,
		},
		Ident:            validIdentity(),
		ClientProtocol:   proto.ClientProtocolOpenAIChat,
		ProtocolFamily:   "openai_chat",
		RouteID:          "cache-settle-fail-route",
		RequestID:        "req-cache-settle-fail",
		AccountID:        1,
		AcquisitionToken: uuid.New(),
		PoolID:           "42",
		UpstreamModelID:  "gpt-4o",
		RequestedModel:   "gpt-4o",
		Provider:         "openai",
		RequestStartedAt: time.Now().UTC(),
		ReserveResult:    &billing.ReserveResult{ClaimID: 77995, AttemptSeq: 1},
		PayloadHash:      "payload-cache-settle-fail",
		AttemptSeq:       1,
		SettlementIntent: tracker,
	})

	if !served || rec.Code != http.StatusInternalServerError {
		t.Fatalf("served/status=%v/%d want true/500 body=%s", served, rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 1 || len(settler.aborts) != 1 {
		t.Fatalf("settle/abort=%d/%d want 1/1", len(settler.calls), len(settler.aborts))
	}
	if got := strings.Join(intentStore.events, "->"); got != "pending->aborted" {
		t.Fatalf("intent lifecycle=%s want pending->aborted", got)
	}
	if intentStore.operationCalls["mark_failed"] != 0 {
		t.Fatalf("交付前失败不得写 failed: calls=%v", intentStore.operationCalls)
	}
}

// TestSettlementIntentUsesAuthoritativeReserveAttempt 守住意图身份直接采用账本 Reserve 返回值。
func TestSettlementIntentUsesAuthoritativeReserveAttempt(t *testing.T) {
	enableHCSFDispatchForTest(t)
	intentStore := &recordingSettlementIntentStore{}
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.ClaimGate = fixedAttemptClaimGate{claimID: 999, attemptSeq: 7}
	d.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{}
	d.SettlementIntents = intentStore
	d.Selector = selector

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if intentStore.created.AttemptSeq != 7 {
		t.Fatalf("intent attempt_seq=%d want ReserveResult authority 7", intentStore.created.AttemptSeq)
	}
	if len(selector.requests) != 1 || selector.requests[0].AttemptSeq != 7 {
		t.Fatalf("selector attempt_seq requests=%+v want one request with ReserveResult authority 7", selector.requests)
	}
}

// TestSettlementIntentDoubleFailurePersistsReplayEvidence 守住结算与恢复同时失败时的
// 金额和可重放证据，恢复组件存在本身不能冒充成功入队。
func TestSettlementIntentDoubleFailurePersistsReplayEvidence(t *testing.T) {
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
	recoveryPayload, err := settlementrecovery.Decode(intentStore.recoveryPayload)
	if err != nil {
		t.Fatalf("恢复载荷不可解码: %v", err)
	}
	if recoveryPayload.Source != settlementrecovery.SourceDirectSettle ||
		recoveryPayload.ToSettleRequest().ClaimID != intentStore.created.ClaimID ||
		intentStore.recoveryClass == "" {
		t.Fatalf(
			"恢复证据 source/claim/class=%q/%d/%q",
			recoveryPayload.Source,
			recoveryPayload.ToSettleRequest().ClaimID,
			intentStore.recoveryClass,
		)
	}
}

// TestSettlementIntentCacheHitWriteFailureKeepsCommittedMoneyState 守住缓存账本提交
// 先于客户端写入；即使 socket 短写，意图仍必须与已提交 claim 同步为 settled。
func TestSettlementIntentCacheHitWriteFailureKeepsCommittedMoneyState(t *testing.T) {
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
	if got := strings.Join(intentStore.events, "->"); got != "pending->settled" {
		t.Fatalf("短写后的 intent lifecycle=%s want pending->settled", got)
	}
	if intentStore.operationCalls["mark_delivering"] != 0 || intentStore.operationCalls["mark_settled"] != 1 {
		t.Fatalf("缓存提交后的状态调用不符: calls=%v", intentStore.operationCalls)
	}
}

// TestSettlementIntentPostAcquireCacheHitWriteFailureKeepsCommittedMoneyState 守住
// 已取得账号的缓存结算分支同样先收敛账本和意图，再尝试客户端写入。
func TestSettlementIntentPostAcquireCacheHitWriteFailureKeepsCommittedMoneyState(t *testing.T) {
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
	if got := strings.Join(intentStore.events, "->"); got != "pending->settled" {
		t.Fatalf("短写后的 intent lifecycle=%s want pending->settled", got)
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
		if w.firstWriteCompletedAt.IsZero() || intentStore.firstByteAt.After(w.firstWriteCompletedAt) {
			t.Fatalf("stream first_byte_at=%s 晚于首帧写完成=%s", intentStore.firstByteAt, w.firstWriteCompletedAt)
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
		if got := strings.Join(intentStore.events, "->"); got != "pending->delivering->aborted" {
			t.Fatalf("首帧写失败 intent lifecycle=%s want pending->delivering->aborted", got)
		}
	})
}

// TestStreamingDeliveryEvidenceTimeoutFailsBeforeBusinessFrame 守住交付证据数据库
// 停顿时快速拒绝，不能先发业务帧或进入主结算。
func TestStreamingDeliveryEvidenceTimeoutFailsBeforeBusinessFrame(t *testing.T) {
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

	if w.Code != http.StatusServiceUnavailable || len(settler.calls) != 0 || len(settler.aborts) != 1 {
		t.Fatalf("status/settles/aborts=%d/%d/%d want 503/0/1", w.Code, len(settler.calls), len(settler.aborts))
	}
	if len(w.completedAt) != 1 {
		// 只有最终 JSON 错误可以写入；业务 SSE 不得越过交付硬门。
		t.Fatalf("响应写次数=%d want 1", len(w.completedAt))
	}
	if elapsed := time.Since(startedAt); elapsed < 90*time.Millisecond {
		t.Fatalf("夹具未实际等待意图短超时: 总耗时=%s", elapsed)
	}
	if got := strings.Join(store.events, "->"); got != "pending->aborted" {
		t.Fatalf("慢 delivering 后 intent lifecycle=%s want pending->aborted", got)
	}
	assertSettlementIntentWarningSanitized(t, logs.String(), "mark_delivering")
}

// TestSettlementIntentPreDeliveryWritesRespectCancellation 守住交付前证据写使用请求取消
// 和短预算；数据库停顿必须快速拒绝并释放 claim，不能无限等待或继续发网。
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
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d want 503 body=%s", rec.Code, rec.Body.String())
			}
			if len(settler.calls) != 0 || len(settler.aborts) != 1 {
				t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
			}
			if store.deadline.IsZero() {
				t.Fatal("交付前写上下文缺少短 deadline")
			}
			assertSettlementIntentWarningSanitized(t, logs.String(), "insert_pending")
		})
	}
}

// TestSettlementIntentDeliveringRespectsCancellation 守住请求在交付硬门前取消时，
// 不写业务响应、不结算，并使用独立上下文释放预留。
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

	if rec.Code != http.StatusServiceUnavailable || len(settler.calls) != 0 || len(settler.aborts) != 1 {
		t.Fatalf("status/settles/aborts=%d/%d/%d want 503/0/1", rec.Code, len(settler.calls), len(settler.aborts))
	}
	if baseStore.operationCalls["mark_delivering"] != 0 {
		t.Fatalf("取消后 delivering store calls=%d want 0", baseStore.operationCalls["mark_delivering"])
	}
	if baseStore.operationCalls["mark_aborted"] != 1 {
		t.Fatalf("取消后 terminal store calls=%d want 1", baseStore.operationCalls["mark_aborted"])
	}
	if got := strings.Join(baseStore.events, "->"); got != "pending->aborted" {
		t.Fatalf("取消后 intent lifecycle=%s want pending->aborted", got)
	}
	assertSettlementIntentWarningSanitized(t, logs.String(), "mark_delivering")
}

// TestSettlementIntentOperationFailures 区分交付前硬门与交付后的尽力状态推进：
// pending 写失败必须拒绝，后续状态写失败不得改写已交付响应或主结算结果。
func TestSettlementIntentOperationFailures(t *testing.T) {
	operations := []string{
		"insert_pending",
		"mark_delivering",
		"mark_settling",
		"mark_settled",
		"mark_aborted",
		"mark_recovery_pending",
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
				case "mark_recovery_pending":
					settler.err = errors.New("主结算失败")
					recovery.retErr = errors.New("恢复队列失败")
				case "mark_aborted":
					deps.CanonicalDispatcher = &mockCanonicalBufferedDispatcher{err: errors.New("上游失败")}
				}

				rec := invokeHandlerPath(t, deps, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)

				switch operation {
				case "insert_pending", "mark_delivering":
					if rec.Code != http.StatusServiceUnavailable {
						t.Fatalf("交付前证据失败 status=%d want 503 body=%s", rec.Code, rec.Body.String())
					}
					if len(settler.calls) != 0 || len(settler.aborts) != 1 {
						t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
					}
				case "mark_aborted":
					if rec.Code == http.StatusOK {
						t.Fatalf("上游失败的主响应不得因意图旁路改成 200: body=%s", rec.Body.String())
					}
					if len(settler.calls) != 0 || len(settler.aborts) != 1 {
						t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
					}
				default:
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

// TestSettlementIntentStoreBoundaryFaults 守住存储真实停顿或 panic 时的边界：
// pending 硬门快速拒绝，交付后状态故障不篡改已经发生的业务结果。
func TestSettlementIntentStoreBoundaryFaults(t *testing.T) {
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
		"mark_recovery_pending",
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

					switch operation {
					case "insert_pending", "mark_delivering":
						if rec.Code != http.StatusServiceUnavailable {
							t.Fatalf("交付前证据故障 status=%d want 503 body=%s", rec.Code, rec.Body.String())
						}
						if len(settler.calls) != 0 || len(settler.aborts) != 1 {
							t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
						}
					case "mark_aborted":
						if rec.Code == http.StatusOK {
							t.Fatalf("上游失败路径被旁路故障改成成功响应: body=%s", rec.Body.String())
						}
						if len(settler.calls) != 0 || len(settler.aborts) != 1 {
							t.Fatalf("主结算/Abort=%d/%d want 0/1", len(settler.calls), len(settler.aborts))
						}
					default:
						if rec.Code != http.StatusOK {
							t.Fatalf("终态状态故障改变已交付响应: status=%d body=%s", rec.Code, rec.Body.String())
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

// TestSettlementIntentDeliveringBlocksDeliveryAndMoneyPath 守住 delivering 持久化
// 未完成时，流式和非流式都不得写业务响应或启动主结算。
func TestSettlementIntentDeliveringBlocksDeliveryAndMoneyPath(t *testing.T) {
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
			health := &recordingChannelHealth{}
			deps.ChannelHealth = health
			rec := httptest.NewRecorder()
			done := serveSettlementIntentRequestAsync(deps, rec, body)

			select {
			case <-store.started:
			case <-time.After(time.Second):
				t.Fatal("delivering Store 未进入停顿点")
			}
			select {
			case <-settler.settleStarted:
				t.Fatal("交付证据未落库时启动了主结算")
			case <-time.After(50 * time.Millisecond):
			}

			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("交付硬门未在短预算内返回")
			}
			if rec.Code != http.StatusServiceUnavailable || len(settler.calls) != 0 || len(settler.aborts) != 1 {
				t.Fatalf("status/settles/aborts=%d/%d/%d want 503/0/1", rec.Code, len(settler.calls), len(settler.aborts))
			}
			for _, signal := range health.signals {
				if signal.Class != channelhealth.SignalSuccess {
					t.Fatalf("本地交付证据故障污染账号健康: signals=%+v", health.signals)
				}
			}
			if len(health.forceCooldowns) != 0 {
				t.Fatalf("本地交付证据故障触发账号冷却: %+v", health.forceCooldowns)
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
	if strings.Contains(logs.String(), "结算意图状态写失败") {
		t.Fatalf("quota deny 正常生命周期不应产生状态告警: %s", logs.String())
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
	if strings.Contains(logs.String(), "结算意图状态写失败") {
		t.Fatalf("默认关闭不应产生状态告警: %s", logs.String())
	}
}

// TestSettlementIntentMissingStoreAndZeroIDFailClosed 区分显式关闭与启用态接线故障，
// 并守住启用时无存储或零标识都在上游调用前释放 claim。
func TestSettlementIntentMissingStoreAndZeroIDFailClosed(t *testing.T) {
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

		if rec.Code != http.StatusServiceUnavailable || len(settler.calls) != 0 || len(settler.aborts) != 1 {
			t.Fatalf("enabled nil store status/settle/abort=%d/%d/%d want 503/0/1", rec.Code, len(settler.calls), len(settler.aborts))
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
		if strings.Contains(logs.String(), "结算意图状态写失败") {
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

		if rec.Code != http.StatusServiceUnavailable || len(settler.calls) != 0 || len(settler.aborts) != 1 {
			t.Fatalf("zero id status/settle/abort=%d/%d/%d want 503/0/1", rec.Code, len(settler.calls), len(settler.aborts))
		}
		if len(store.events) != 0 {
			t.Fatalf("zero id 不得伪造状态迁移: %v", store.events)
		}
		assertSettlementIntentWarningSanitized(t, logs.String(), "insert_pending")
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

func (s *settlementIntentFaultStore) MarkRecoveryPending(
	ctx context.Context,
	id int64,
	version int32,
	actualCost decimal.Decimal,
	payload json.RawMessage,
	failureClass string,
) (int32, error) {
	if s.fault("mark_recovery_pending") {
		return version + 1, nil
	}
	return s.base.MarkRecoveryPending(ctx, id, version, actualCost, payload, failureClass)
}

func (s *settlementIntentFaultStore) ListStaleNonTerminalSettlementIntents(ctx context.Context, staleCutoff, createdBefore time.Time, limit int32) ([]settlementintent.StaleSettlementIntent, error) {
	return s.base.ListStaleNonTerminalSettlementIntents(ctx, staleCutoff, createdBefore, limit)
}

func (s *settlementIntentFaultStore) MarkSettledIfStale(ctx context.Context, id int64, version int32, actualCost decimal.Decimal, settledAt time.Time) (int32, error) {
	return s.base.MarkSettledIfStale(ctx, id, version, actualCost, settledAt)
}

func (s *settlementIntentFaultStore) MarkAbortedIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	return s.base.MarkAbortedIfStale(ctx, id, version)
}

func (s *settlementIntentFaultStore) MarkSupersededIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	return s.base.MarkSupersededIfStale(ctx, id, version)
}

func (s *settlementIntentFaultStore) MarkSettlingIfStale(ctx context.Context, id int64, version int32) (int32, error) {
	return s.base.MarkSettlingIfStale(ctx, id, version)
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
	case "mark_recovery_pending":
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
	for _, want := range []string{"结算意图状态写失败", operation, "tenant_id", "request_id", "claim_id", "error_type"} {
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
