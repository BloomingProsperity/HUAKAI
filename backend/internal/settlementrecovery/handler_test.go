package settlementrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

type spySettler struct {
	calls    int
	lastReq  billing.SettleRequest
	retErr   error
	retValue *billing.SettleResult
}

func (s *spySettler) Settle(_ context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	s.calls++
	s.lastReq = req
	if s.retErr != nil {
		return nil, s.retErr
	}
	if s.retValue != nil {
		return s.retValue, nil
	}
	return &billing.SettleResult{}, nil
}

func (s *spySettler) Abort(_ context.Context, _, _ int64, _, _ string, _ int64, _ json.RawMessage) error {
	return errors.New("Abort not used in tests")
}

func (s *spySettler) CommitCacheHit(_ context.Context, _ billing.SettleRequest) error {
	return errors.New("CommitCacheHit not used in tests")
}

func (s *spySettler) Refund(_ context.Context, _ billing.RefundRequest) (*billing.RefundResult, error) {
	return nil, errors.New("Refund not used in tests")
}

type stubProof struct {
	committed bool
	err       error
	calls     int
}

func (p *stubProof) IsCommitted(_ context.Context, _, _ int64) (bool, error) {
	p.calls++
	return p.committed, p.err
}

func encodedFixture(t *testing.T) dlq.Record {
	t.Helper()
	event := fixtureCompletionEvent(t)
	payload := FromCompletionEvent(SourceStream, event)
	raw, err := payload.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return dlq.Record{
		EventKind: dlq.EventKindPostDeliverySettlement,
		Payload:   raw,
		TenantID:  payload.Settle.TenantID,
	}
}

// TestHandle_SettleSuccessMarksDelivered Mutation: 把 Settle 调用删掉 →
// spy.calls=0 → 红。
func TestHandle_SettleSuccessMarksDelivered(t *testing.T) {
	settler := &spySettler{}
	proof := &stubProof{}
	h := &Handler{Settler: settler, Proof: proof}

	err := h.Handle(context.Background(), encodedFixture(t))
	if err != nil {
		t.Fatalf("Handle on success-settle: err=%v want=nil", err)
	}
	if settler.calls != 1 {
		t.Fatalf("Settler.Settle calls=%d want=1 (worker must invoke public Settler)", settler.calls)
	}
	if proof.calls != 0 {
		t.Fatalf("Proof should not be checked on Settle success, got %d calls", proof.calls)
	}
}

// TestHandle_SettleGenericErrPropagates Mutation: 把 err wrap 改成 return nil
// → 红(worker 会标 delivered 即使 settle 失败,money loss)。
func TestHandle_SettleGenericErrPropagates(t *testing.T) {
	settler := &spySettler{retErr: errors.New("pgx: connection refused")}
	proof := &stubProof{}
	h := &Handler{Settler: settler, Proof: proof}

	err := h.Handle(context.Background(), encodedFixture(t))
	if err == nil {
		t.Fatal("Handle must propagate generic Settle err so worker keeps retrying")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err=%q must wrap original Settle err", err.Error())
	}
	if proof.calls != 0 {
		t.Fatalf("Proof should not be checked for non-ErrClaimNotReserving, got %d calls", proof.calls)
	}
}

// TestHandle_ClaimNotReserving_ProofTrue_Idempotent 验证 三证齐时
// settle 视已成功,worker 标 delivered。
// Mutation: 把 committed==true 路径改成继续返 err → 红(worker 永远重试已 committed claim)。
func TestHandle_ClaimNotReserving_ProofTrue_Idempotent(t *testing.T) {
	settler := &spySettler{retErr: billing.ErrClaimNotReserving}
	proof := &stubProof{committed: true}
	h := &Handler{Settler: settler, Proof: proof}

	err := h.Handle(context.Background(), encodedFixture(t))
	if err != nil {
		t.Fatalf("Handle with three-witness proof=true must succeed (idempotent), got err=%v", err)
	}
	if proof.calls != 1 {
		t.Fatalf("Proof must be checked once on ErrClaimNotReserving, got %d calls", proof.calls)
	}
}

// TestHandle_ClaimNotReserving_ProofFalse_KeepsFailing 守 D5 三证缺一时
// 不视成功,worker 继续重试。这是 money path 安全闸:不允许 status=committed
// 但 usage/billing_event 缺的状态被默默放过。
// Mutation: 把 proof==false 路径改成 return nil → 红(假阳性 idempotent)。
func TestHandle_ClaimNotReserving_ProofFalse_KeepsFailing(t *testing.T) {
	settler := &spySettler{retErr: billing.ErrClaimNotReserving}
	proof := &stubProof{committed: false}
	h := &Handler{Settler: settler, Proof: proof}

	err := h.Handle(context.Background(), encodedFixture(t))
	if err == nil {
		t.Fatal("Handle with proof=false on ErrClaimNotReserving must return err (not silently mark delivered)")
	}
	if !strings.Contains(err.Error(), "not committed") {
		t.Fatalf("err=%q must reference proof failure", err.Error())
	}
}

// TestHandle_ClaimNotReserving_ProofErr_PropagatesErr 守 proof DB 查询出错时
// 不能视已成功,必须传播 err 让 worker 重试。
func TestHandle_ClaimNotReserving_ProofErr_PropagatesErr(t *testing.T) {
	settler := &spySettler{retErr: billing.ErrClaimNotReserving}
	proof := &stubProof{err: errors.New("pgx: timeout")}
	h := &Handler{Settler: settler, Proof: proof}

	err := h.Handle(context.Background(), encodedFixture(t))
	if err == nil {
		t.Fatal("Handle must propagate proof query err")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err=%q must wrap proof err", err.Error())
	}
}

// TestHandle_NilSettler_ReturnsErrSettlerNil Mutation: 删 settler==nil check →
// 红(panic 而不是 sentinel)。
func TestHandle_NilSettler_ReturnsErrSettlerNil(t *testing.T) {
	h := &Handler{Settler: nil, Proof: &stubProof{}}
	err := h.Handle(context.Background(), encodedFixture(t))
	if !errors.Is(err, ErrSettlerNil) {
		t.Fatalf("err=%v want=ErrSettlerNil", err)
	}
}

// TestHandle_NilProof_ReturnsErrProofNil Mutation: 删 proof==nil check → 红。
func TestHandle_NilProof_ReturnsErrProofNil(t *testing.T) {
	h := &Handler{Settler: &spySettler{}, Proof: nil}
	err := h.Handle(context.Background(), encodedFixture(t))
	if !errors.Is(err, ErrProofNil) {
		t.Fatalf("err=%v want=ErrProofNil", err)
	}
}

// TestHandle_WrongEventKind_Rejects 守 worker 误把别的 kind 路由进来时不
// 默默 swallow,而是返错让 worker 把这行标 quarantined 或 operator_review。
func TestHandle_WrongEventKind_Rejects(t *testing.T) {
	rec := encodedFixture(t)
	rec.EventKind = dlq.EventKindUsageRecord // 故意错路由
	h := &Handler{Settler: &spySettler{}, Proof: &stubProof{}}
	err := h.Handle(context.Background(), rec)
	if err == nil {
		t.Fatal("Handle must reject wrong event_kind (routing safety)")
	}
}

// TestHandle_DecodeFail_ReturnsErr 守 corrupted payload 不静默成功 — 让
// worker 多次失败后转 quarantined。
func TestHandle_DecodeFail_ReturnsErr(t *testing.T) {
	rec := dlq.Record{
		EventKind: dlq.EventKindPostDeliverySettlement,
		Payload:   []byte(`{not json`),
	}
	h := &Handler{Settler: &spySettler{}, Proof: &stubProof{}}
	err := h.Handle(context.Background(), rec)
	if err == nil {
		t.Fatal("Handle must surface decode errors instead of silently marking delivered")
	}
}
