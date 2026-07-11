package settlementrecovery

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
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

func testHandler(settler billing.Settler, proof CommittedProof) *Handler {
	return &Handler{
		Settler: settler,
		Proof:   proof,
		AuditRefPolicy: &eventbus.AuditRefPolicy{
			ReleaseMode: eventbus.ReleaseModeTest,
		},
	}
}

// TestHandle_SettleSuccessMarksDelivered Mutation: 把 Settle 调用删掉 →
// spy.calls=0 → 红。
func TestHandle_SettleSuccessMarksDelivered(t *testing.T) {
	settler := &spySettler{}
	proof := &stubProof{}
	h := testHandler(settler, proof)

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

// TestHandle_MissingAuditEvidenceDoesNotSettle 守住恢复路径 fail-closed：
// 事件来源 payload 丢失 ledger+签名与 audit-DLQ 引用时，worker 不得盲扣费。
// 变异：删除 Handle 中结算前审计校验，会重新调用 Settle 并返回 nil，本测试变红。
func TestHandle_MissingAuditEvidenceDoesNotSettle(t *testing.T) {
	rec := encodedFixture(t)
	payload, err := Decode(rec.Payload)
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}
	payload.AuditLedgerDLQRef = ""
	payload.AuditLedgerID = ""
	payload.AuditSignatureFingerprint = ""
	rec.Payload, err = payload.Encode()
	if err != nil {
		t.Fatalf("Encode fixture: %v", err)
	}
	settler := &spySettler{}
	h := &Handler{
		Settler: settler,
		Proof:   &stubProof{},
		AuditRefPolicy: &eventbus.AuditRefPolicy{
			ReleaseMode: eventbus.ReleaseModeProduction,
		},
	}

	err = h.Handle(context.Background(), rec)
	if err == nil {
		t.Fatal("缺少审计证据时必须返回错误并保持恢复行 pending")
	}
	if settler.calls != 0 {
		t.Fatalf("Settler.Settle calls=%d want 0", settler.calls)
	}
	if errors.Is(err, dlq.ErrUnretryable) {
		t.Fatalf("缺审计引用可能由运维修复，不得标记为不可重试：%v", err)
	}
	if !errors.Is(err, eventbus.ErrAuditRefMissing) {
		t.Fatalf("err=%v want ErrAuditRefMissing", err)
	}
}

// TestHandle_PersistedAuditEvidenceSettles 验证 ledger ID 必须与签名指纹成对，
// 且有效持久证据能通过生产 policy 后继续调用 public Settler。
func TestHandle_PersistedAuditEvidenceSettles(t *testing.T) {
	rec := encodedFixture(t)
	payload, err := Decode(rec.Payload)
	if err != nil {
		t.Fatalf("Decode fixture: %v", err)
	}
	payload.AuditLedgerDLQRef = ""
	payload.AuditLedgerID = "ledger-persisted-1"
	payload.AuditSignatureFingerprint = "sig-fingerprint-1"
	rec.Payload, err = payload.Encode()
	if err != nil {
		t.Fatalf("Encode fixture: %v", err)
	}
	settler := &spySettler{}
	h := &Handler{
		Settler: settler,
		Proof:   &stubProof{},
		AuditRefPolicy: &eventbus.AuditRefPolicy{
			ReleaseMode: eventbus.ReleaseModeProduction,
		},
	}

	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if settler.calls != 1 {
		t.Fatalf("Settler.Settle calls=%d want 1", settler.calls)
	}
}

// TestHandle_NilAuditRefPolicyDoesNotSettleEventPayload 防止生产接线遗漏 policy
// 时退化成无校验直扣；直接 SettleRequest 来源不受此哨兵影响。
func TestHandle_NilAuditRefPolicyDoesNotSettleEventPayload(t *testing.T) {
	settler := &spySettler{}
	h := &Handler{Settler: settler, Proof: &stubProof{}}
	err := h.Handle(context.Background(), encodedFixture(t))
	if !errors.Is(err, ErrAuditRefPolicyNil) {
		t.Fatalf("err=%v want ErrAuditRefPolicyNil", err)
	}
	if settler.calls != 0 {
		t.Fatalf("Settler.Settle calls=%d want 0", settler.calls)
	}
}

// TestHandle_SettleGenericErrPropagates Mutation: 把 err wrap 改成 return nil
// → 红(worker 会标 delivered 即使 settle 失败,money loss)。
func TestHandle_SettleGenericErrPropagates(t *testing.T) {
	settler := &spySettler{retErr: errors.New("pgx: connection refused")}
	proof := &stubProof{}
	h := testHandler(settler, proof)

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
	h := testHandler(settler, proof)

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
	h := testHandler(settler, proof)

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
	h := testHandler(settler, proof)

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
	h := testHandler(nil, &stubProof{})
	err := h.Handle(context.Background(), encodedFixture(t))
	if !errors.Is(err, ErrSettlerNil) {
		t.Fatalf("err=%v want=ErrSettlerNil", err)
	}
}

// TestHandle_NilProof_ReturnsErrProofNil Mutation: 删 proof==nil check → 红。
func TestHandle_NilProof_ReturnsErrProofNil(t *testing.T) {
	h := testHandler(&spySettler{}, nil)
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
	h := testHandler(&spySettler{}, &stubProof{})
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
	h := testHandler(&spySettler{}, &stubProof{})
	err := h.Handle(context.Background(), rec)
	if err == nil {
		t.Fatal("Handle must surface decode errors instead of silently marking delivered")
	}
}

// TestHandle_DecodeFail_IsUnretryable 守 corrupted payload 被分类为结构性不可重试,
// worker 据此第 1 次即 quarantine 而非烧满重试预算。
// Mutation: 把 decode 分支的 errors.Join(err, dlq.ErrUnretryable) 改回裸 err →
// errors.Is 不再命中 → 红。
func TestHandle_DecodeFail_IsUnretryable(t *testing.T) {
	rec := dlq.Record{EventKind: dlq.EventKindPostDeliverySettlement, Payload: []byte(`{not json`)}
	h := testHandler(&spySettler{}, &stubProof{})
	err := h.Handle(context.Background(), rec)
	if !errors.Is(err, dlq.ErrUnretryable) {
		t.Fatalf("decode failure must classify as dlq.ErrUnretryable (poison), got err=%v", err)
	}
}

// TestHandle_ValidateFail_IsUnretryable 守 decode 成功但结构非法(claim_id=0)同样
// 被分类为不可重试。Mutation: 去掉 validate 分支的 ErrUnretryable wrap → 红。
func TestHandle_ValidateFail_IsUnretryable(t *testing.T) {
	event := fixtureCompletionEvent(t)
	payload := FromCompletionEvent(SourceStream, event)
	payload.Settle.ClaimID = 0 // 仍可 decode,但 Validate 因缺 claim_id 失败
	raw, err := payload.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec := dlq.Record{EventKind: dlq.EventKindPostDeliverySettlement, Payload: raw, TenantID: payload.Settle.TenantID}
	h := testHandler(&spySettler{}, &stubProof{})
	gotErr := h.Handle(context.Background(), rec)
	if !errors.Is(gotErr, dlq.ErrUnretryable) {
		t.Fatalf("validate failure must classify as dlq.ErrUnretryable, got err=%v", gotErr)
	}
}

// TestHandle_WrongEventKind_IsUnretryable 守误路由的事件类型被分类为不可重试。
// Mutation: 去掉 wrong-kind 分支的 ErrUnretryable wrap → 红。
func TestHandle_WrongEventKind_IsUnretryable(t *testing.T) {
	rec := encodedFixture(t)
	rec.EventKind = dlq.EventKindUsageRecord
	h := testHandler(&spySettler{}, &stubProof{})
	err := h.Handle(context.Background(), rec)
	if !errors.Is(err, dlq.ErrUnretryable) {
		t.Fatalf("wrong event_kind must classify as dlq.ErrUnretryable, got err=%v", err)
	}
}

// TestHandle_TransientSettleErr_NotUnretryable 是 money-safety 控制:瞬时 settle 错
// (如 DB 连接拒绝)绝不能被误判为不可重试 —— 否则一次瞬时抖动就把一个真实结算意图
// 立刻 quarantine、停止重试 = 漏结算/丢钱。必须保持可重试(errors.Is 不命中)。
// Mutation: 若把 generic settle 错也 wrap ErrUnretryable → 红。
func TestHandle_TransientSettleErr_NotUnretryable(t *testing.T) {
	settler := &spySettler{retErr: errors.New("pgx: connection refused")}
	h := testHandler(settler, &stubProof{})
	err := h.Handle(context.Background(), encodedFixture(t))
	if err == nil {
		t.Fatal("transient settle error must propagate so worker retries")
	}
	if errors.Is(err, dlq.ErrUnretryable) {
		t.Fatalf("transient settle error must STAY retryable (not poison), got unretryable: %v", err)
	}
}

// TestHandle_ClaimNotReserving_ProofFalse_NotUnretryable 同属 money-safety 控制:
// claim 非 reserving 且三证未齐时返错重试,但绝不能被分类为不可重试 —— 半提交/
// aborted 仍可能需 worker 重试或 operator force-settle,立即 quarantine 会过早终止。
func TestHandle_ClaimNotReserving_ProofFalse_NotUnretryable(t *testing.T) {
	settler := &spySettler{retErr: billing.ErrClaimNotReserving}
	h := testHandler(settler, &stubProof{committed: false})
	err := h.Handle(context.Background(), encodedFixture(t))
	if err == nil {
		t.Fatal("proof-false on ErrClaimNotReserving must return err")
	}
	if errors.Is(err, dlq.ErrUnretryable) {
		t.Fatalf("ErrClaimNotReserving+proof-false must STAY retryable, got unretryable: %v", err)
	}
}
