package main

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestEffectiveHermesProposeDisabledWhenMutatingDisabled(t *testing.T) {
	// HERMES-IP-01:PROPOSE=true + MUTATING=false 不能让 LLM 继续看到可提议 mutating
	// 工具,否则 operator 确认时才撞 403。变异证伪:把 effectiveHermesProposeEnabled
	// 改回直接返回 proposeEnabled,本测试会得到 true 且后续目录会暴露 mutating 工具。
	core, logs := observer.New(zapcore.WarnLevel)
	got := effectiveHermesProposeEnabled(false, true, zap.New(core))
	if got {
		t.Fatal("mutating=false 时 propose 必须被启动期归一关闭")
	}
	if logs.Len() != 1 {
		t.Fatalf("冲突组合必须写 WARN 日志,got %d", logs.Len())
	}
	entry := logs.All()[0]
	if entry.ContextMap()["propose_knob"] != hermesLLMProposeEnabledEnv+"=true" {
		t.Fatalf("日志缺 propose knob 证据: %+v", entry.ContextMap())
	}
	if entry.ContextMap()["mutating_knob"] != hermesMutatingEnabledEnv+"=false" {
		t.Fatalf("日志缺 mutating knob 证据: %+v", entry.ContextMap())
	}
}

func TestEffectiveHermesProposeKeepsValidCombinations(t *testing.T) {
	if !effectiveHermesProposeEnabled(true, true, zap.NewNop()) {
		t.Fatal("mutating=true/propose=true 应保持 propose 开启")
	}
	if effectiveHermesProposeEnabled(true, false, zap.NewNop()) {
		t.Fatal("propose=false 必须保持关闭")
	}
}
