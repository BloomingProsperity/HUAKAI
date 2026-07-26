package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementrecovery"
)

type recordingSettlementRecoveryEnqueuer struct {
	event dlq.Event
	calls int
	err   error
}

func (q *recordingSettlementRecoveryEnqueuer) Enqueue(_ context.Context, event dlq.Event) (int64, error) {
	q.calls++
	q.event = event
	return 71, q.err
}

// TestBuildSettlementIntentSweeperHonorsFeatureFlag 守住显式关闭逃生口不构造 worker。
func TestBuildSettlementIntentSweeperHonorsFeatureFlag(t *testing.T) {
	queries := dbbilling.New(nil)
	enqueuer := &recordingSettlementRecoveryEnqueuer{}
	if got := buildSettlementIntentSweeper(queries, false, enqueuer); got != nil {
		t.Fatalf("disabled sweeper=%T want nil", got)
	}
	if got := buildSettlementIntentSweeper(queries, true, enqueuer); got == nil {
		t.Fatal("enabled sweeper 不得为 nil")
	}
}

// TestBuildSettlementIntentRecoveryPublisherUsesOfficialQueue 守住意图恢复复用
// post_delivery_settlement 的既有载荷、幂等键和正式队列，不另造旁路。
func TestBuildSettlementIntentRecoveryPublisherUsesOfficialQueue(t *testing.T) {
	enqueuer := &recordingSettlementRecoveryEnqueuer{}
	publisher := buildSettlementIntentRecoveryPublisher(enqueuer)
	payload := settlementrecovery.FromSettleRequest(
		settlementrecovery.SourceStream,
		"req-requeue-1",
		billing.SettleRequest{
			ClaimID:    91,
			TenantID:   17,
			ActualCost: decimal.RequireFromString("0.25"),
		},
	)
	raw, err := payload.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := publisher.PublishSettlementRecovery(context.Background(), raw, "database_unavailable"); err != nil {
		t.Fatalf("PublishSettlementRecovery: %v", err)
	}
	if enqueuer.calls != 1 {
		t.Fatalf("Enqueue 调用=%d want 1", enqueuer.calls)
	}
	if enqueuer.event.EventKind != dlq.EventKindPostDeliverySettlement ||
		enqueuer.event.TenantID != 17 ||
		enqueuer.event.ClaimID != 91 ||
		enqueuer.event.FailureReason != "database_unavailable" {
		t.Fatalf("恢复事件=%+v", enqueuer.event)
	}
	decoded, err := settlementrecovery.Decode(enqueuer.event.Payload)
	if err != nil || decoded.RequestID != "req-requeue-1" {
		t.Fatalf("恢复载荷 decoded=%+v err=%v", decoded, err)
	}

	if err := publisher.PublishSettlementRecovery(context.Background(), []byte(`{"bad":`), "x"); err == nil {
		t.Fatal("损坏载荷必须在入队前失败")
	}
	if enqueuer.calls != 1 {
		t.Fatalf("损坏载荷不得触发 Enqueue，calls=%d", enqueuer.calls)
	}
}

// TestBuildGatewayRuntimeStartsSettlementIntentSweeperBehindFlag 守住生产装配使用真实配置，
// 且只在工厂返回非 nil 时启动 worker。
func TestBuildGatewayRuntimeStartsSettlementIntentSweeperBehindFlag(t *testing.T) {
	parsed := parseGatewayWiringForSweeperTest(t)
	factoryUsesConfig := false
	factoryUsesRecovery := false
	guardedStart := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			factory, ok := n.Fun.(*ast.Ident)
			if !ok || factory.Name != "buildSettlementIntentSweeper" || len(n.Args) != 3 {
				return true
			}
			flag, ok := n.Args[1].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			cfg, cfgOK := flag.X.(*ast.Ident)
			factoryUsesConfig = factoryUsesConfig || (cfgOK && cfg.Name == "cfg" && flag.Sel.Name == "SettlementIntentEnabled")
			recovery, recoveryOK := n.Args[2].(*ast.Ident)
			factoryUsesRecovery = factoryUsesRecovery || (recoveryOK && recovery.Name == "dlqService")
		case *ast.IfStmt:
			if !isNonNilSweeperGuard(n.Cond) {
				return true
			}
			ast.Inspect(n.Body, func(child ast.Node) bool {
				call, ok := child.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				receiver, receiverOK := selector.X.(*ast.Ident)
				if ok && receiverOK && receiver.Name == "settlementIntentSweeper" && selector.Sel.Name == "Start" {
					guardedStart = true
				}
				return !guardedStart
			})
		}
		return !(factoryUsesConfig && factoryUsesRecovery && guardedStart)
	})
	if !factoryUsesConfig {
		t.Fatal("buildGatewayRuntime 未以 cfg.SettlementIntentEnabled 构造 sweeper")
	}
	if !factoryUsesRecovery {
		t.Fatal("buildGatewayRuntime 未把正式结算恢复队列注入 sweeper")
	}
	if !guardedStart {
		t.Fatal("buildGatewayRuntime 未在非 nil 守卫内启动 sweeper")
	}
}

// TestGatewayRuntimeCloseStopsSettlementIntentSweeper 守住数据库连接关闭前停止 worker。
func TestGatewayRuntimeCloseStopsSettlementIntentSweeper(t *testing.T) {
	stopped := 0
	rt := &gatewayRuntime{
		settlementIntentSweepStop: func() { stopped++ },
	}
	rt.close()
	if stopped != 1 {
		t.Fatalf("sweeper Stop 调用=%d want 1", stopped)
	}
}

func parseGatewayWiringForSweeperTest(t *testing.T) *ast.File {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	wiringPath := filepath.Join(filepath.Dir(testFile), "wiring.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), wiringPath, nil, 0)
	if err != nil {
		t.Fatalf("解析 wiring.go: %v", err)
	}
	return parsed
}

func isNonNilSweeperGuard(expr ast.Expr) bool {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok || binary.Op != token.NEQ {
		return false
	}
	identifier, ok := binary.X.(*ast.Ident)
	nilValue, nilOK := binary.Y.(*ast.Ident)
	return ok && nilOK && identifier.Name == "settlementIntentSweeper" && nilValue.Name == "nil"
}
