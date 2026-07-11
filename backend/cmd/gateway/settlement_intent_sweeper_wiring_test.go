package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

// TestBuildSettlementIntentSweeperHonorsFeatureFlag 守住默认关闭时不构造 worker。
func TestBuildSettlementIntentSweeperHonorsFeatureFlag(t *testing.T) {
	queries := dbbilling.New(nil)
	if got := buildSettlementIntentSweeper(queries, false); got != nil {
		t.Fatalf("disabled sweeper=%T want nil", got)
	}
	if got := buildSettlementIntentSweeper(queries, true); got == nil {
		t.Fatal("enabled sweeper 不得为 nil")
	}
}

// TestBuildGatewayRuntimeStartsSettlementIntentSweeperBehindFlag 守住生产装配使用真实配置，
// 且只在工厂返回非 nil 时启动 worker。
func TestBuildGatewayRuntimeStartsSettlementIntentSweeperBehindFlag(t *testing.T) {
	parsed := parseGatewayWiringForSweeperTest(t)
	factoryUsesConfig := false
	guardedStart := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			factory, ok := n.Fun.(*ast.Ident)
			if !ok || factory.Name != "buildSettlementIntentSweeper" || len(n.Args) != 2 {
				return true
			}
			flag, ok := n.Args[1].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			cfg, cfgOK := flag.X.(*ast.Ident)
			factoryUsesConfig = factoryUsesConfig || (cfgOK && cfg.Name == "cfg" && flag.Sel.Name == "SettlementIntentEnabled")
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
		return !(factoryUsesConfig && guardedStart)
	})
	if !factoryUsesConfig {
		t.Fatal("buildGatewayRuntime 未以 cfg.SettlementIntentEnabled 构造 sweeper")
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
