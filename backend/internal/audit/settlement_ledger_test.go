package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestSettlementLedgerSettlerBuildsSixHopChainAfterCommit(t *testing.T) {
	ctx := context.Background()
	ledger, err := auditledger.NewMemoryLedger(testAuditSigner(t, 71))
	if err != nil {
		t.Fatalf("创建日志账本: %v", err)
	}
	settler := NewSettlementLedgerSettler(&recordingBillingSettler{}, ledger, nil, nil)
	requestedAt := time.Date(2026, 7, 21, 1, 2, 3, 0, time.UTC)
	requestID := "req-settlement-ledger-six-hop"
	if _, err := settler.Settle(ctx, billing.SettleRequest{
		TenantID:              7,
		AccountID:             9001,
		AcquisitionToken:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		RequestedAt:           requestedAt,
		RequestedModel:        "public-image",
		UpstreamModel:         "vendor-image-v2",
		Provider:              "xai",
		AuditRequestID:        requestID,
		AuditRouteID:          "registry:7:8:primary",
		AuditPoolGroupID:      42,
		AuditProviderEndpoint: "/v1/images/generations",
	}); err != nil {
		t.Fatalf("结算: %v", err)
	}

	entry, err := ledger.GetByRequestID(ctx, requestID)
	if err != nil {
		t.Fatalf("读取日志链: %v", err)
	}
	if entry.TenantID != 7 || len(entry.HopChain) != 6 {
		t.Fatalf("日志链租户/跳数=%d/%d，期望 7/6", entry.TenantID, len(entry.HopChain))
	}
	wantHops := []proto.HopHop{
		proto.HopIngress,
		proto.HopRouter,
		proto.HopPool,
		proto.HopAccount,
		proto.HopProvider,
		proto.HopResponse,
	}
	for i, want := range wantHops {
		if entry.HopChain[i].Hop != want || entry.HopChain[i].RequestID != requestID {
			t.Fatalf("第 %d 跳=%+v，期望 hop=%q request_id=%q", i, entry.HopChain[i], want, requestID)
		}
	}
	if entry.HopChain[1].RouteID != "registry:7:8:primary" || entry.HopChain[2].PoolID != "42" {
		t.Fatalf("路由/池事实=%q/%q", entry.HopChain[1].RouteID, entry.HopChain[2].PoolID)
	}
	if entry.HopChain[3].AccountIDHash == "" || entry.HopChain[4].Provider != "xai" || entry.HopChain[4].Endpoint != "/v1/images/generations" {
		t.Fatalf("账号/上游事实不完整: account=%q provider=%q endpoint=%q", entry.HopChain[3].AccountIDHash, entry.HopChain[4].Provider, entry.HopChain[4].Endpoint)
	}
}

func TestSettlementLedgerSettlerDefersAppendFailureWithoutFailingMoneyCommit(t *testing.T) {
	appendErr := errors.New("ledger unavailable")
	ledger := &failingSettlementLedger{appendErr: appendErr}
	recovery := &recordingSettlementLedgerRecovery{}
	var reported error
	settler := NewSettlementLedgerSettler(&recordingBillingSettler{}, ledger, recovery, func(_ context.Context, _ string, err error) {
		reported = err
	})

	result, err := settler.Settle(context.Background(), billing.SettleRequest{
		TenantID:       8,
		ClaimID:        88,
		AuditRequestID: "req-settlement-ledger-deferred",
	})
	if err != nil || result == nil {
		t.Fatalf("账务提交不应被日志失败回滚: result=%+v err=%v", result, err)
	}
	if recovery.event.EventKind != dlq.EventKindAuditLedgerEntry || recovery.event.TenantID != 8 {
		t.Fatalf("恢复事件不完整: %+v", recovery.event)
	}
	if reported == nil {
		t.Fatal("日志延迟写入必须进入可观测错误回调")
	}
}

type failingSettlementLedger struct {
	appendErr error
}

func (l *failingSettlementLedger) Append(context.Context, auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, l.appendErr
}

func (l *failingSettlementLedger) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *failingSettlementLedger) GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l *failingSettlementLedger) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return [32]byte{}, nil
}

func (l *failingSettlementLedger) Size(context.Context) int { return 0 }

type recordingSettlementLedgerRecovery struct {
	event dlq.Event
}

func (r *recordingSettlementLedgerRecovery) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	r.event = event
	return 901, nil
}
