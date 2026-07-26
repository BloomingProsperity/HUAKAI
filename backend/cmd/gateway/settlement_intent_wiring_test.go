package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
)

// TestRelayHandlerDepsWireSettlementIntent 守住 routes 层向所有同步计费协议
// 同时传递同一个 Store 和启用态；Gemini 复用 chat/embeddings 管线。
func TestRelayHandlerDepsWireSettlementIntent(t *testing.T) {
	marker := settlementintent.NewPostgresStore(nil)
	d := &deps{
		cfg:               &Config{SettlementIntentEnabled: true},
		settlementIntents: marker,
	}

	checks := []struct {
		name    string
		store   settlementintent.Store
		enabled bool
	}{
		{name: "chat", store: chatHandlerDeps(d).SettlementIntents, enabled: chatHandlerDeps(d).SettlementIntentEnabled},
		{name: "completions", store: completionsHandlerDeps(d).SettlementIntents, enabled: completionsHandlerDeps(d).SettlementIntentEnabled},
		{name: "embeddings", store: embeddingsHandlerDeps(d).SettlementIntents, enabled: embeddingsHandlerDeps(d).SettlementIntentEnabled},
		{name: "rerank", store: rerankHandlerDeps(d).SettlementIntents, enabled: rerankHandlerDeps(d).SettlementIntentEnabled},
		{name: "images", store: imageHandlerDeps(d).SettlementIntents, enabled: imageHandlerDeps(d).SettlementIntentEnabled},
		{name: "audio", store: audioHandlerDeps(d).SettlementIntents, enabled: audioHandlerDeps(d).SettlementIntentEnabled},
	}
	for _, check := range checks {
		if check.store != marker || !check.enabled {
			t.Fatalf("%s settlement intent store/enabled=%T/%v，未复用生产接线", check.name, check.store, check.enabled)
		}
	}
}

// TestBuildSettlementIntentStoreHonorsFeatureFlag 守住生产工厂的 enabled/disabled 结果。
func TestBuildSettlementIntentStoreHonorsFeatureFlag(t *testing.T) {
	queries := dbbilling.New(nil)
	if got := buildSettlementIntentStore(queries, false); got != nil {
		t.Fatalf("disabled store=%T want nil", got)
	}
	if got := buildSettlementIntentStore(queries, true); got == nil {
		t.Fatal("enabled store 不得为 nil")
	}
}

// TestBuildGatewayRuntimeUsesSettlementIntentFactory 守住生产 runtime 以真实配置值调用
// 已验证的 Store 工厂，避免灰度开启后依赖仍为空。
func TestBuildGatewayRuntimeUsesSettlementIntentFactory(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试源文件")
	}
	wiringPath := filepath.Join(filepath.Dir(testFile), "wiring.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), wiringPath, nil, 0)
	if err != nil {
		t.Fatalf("解析 wiring.go: %v", err)
	}

	found := false
	ast.Inspect(parsed, func(node ast.Node) bool {
		kv, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "settlementIntents" {
			return true
		}
		call, ok := kv.Value.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		factory, ok := call.Fun.(*ast.Ident)
		if !ok || factory.Name != "buildSettlementIntentStore" {
			return true
		}
		flag, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok || flag.Sel.Name != "SettlementIntentEnabled" {
			return true
		}
		cfg, ok := flag.X.(*ast.Ident)
		found = ok && cfg.Name == "cfg"
		return !found
	})
	if !found {
		t.Fatal("buildGatewayRuntime 未用 cfg.SettlementIntentEnabled 构造 settlement intent store")
	}
}
