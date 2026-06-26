package platformsettings_test

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/modelfallback"
	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestValidateModelFallbackChains_RejectsUnknownBucket(t *testing.T) {
	// 变异:保留通用的 validateJSONObjectValue 分派,则这个对象形态的配置
	// 会被接受,而不是在写入时显式报错。
	value := `{"enabled":true,"max_depth":2,"foo":{"gpt-4o":["gpt-4o-mini"]}}`
	_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, value)
	if !errors.Is(err, platformsettings.ErrInvalidValue) {
		t.Fatalf("ValidateValue unknown bucket err=%v want ErrInvalidValue", err)
	}
}

func TestValidateModelFallbackChains_RejectsNonStringArrayChain(t *testing.T) {
	// 变异:跳过 chain 的类型断言,则畸形的 chain 载荷会通过,
	// 让运行时规范化静默抹掉本应生效的 fallback。
	cases := []struct {
		name  string
		value string
	}{
		{name: "number", value: `{"enabled":true,"general":{"gpt-4o":42}}`},
		{name: "object", value: `{"enabled":true,"general":{"gpt-4o":{"next":"gpt-4o-mini"}}}`},
		{name: "empty_array", value: `{"enabled":true,"general":{"gpt-4o":[]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}

func TestValidateModelFallbackChains_RejectsCycleOrSelfRef(t *testing.T) {
	// 变异:跳过 bucket 内的环检测,则 admin 保存会接受只能回退到
	// 已尝试过的 model 的 chain。
	cases := []struct {
		name  string
		value string
	}{
		{name: "self_ref", value: `{"enabled":true,"general":{"gpt-4o":["gpt-4o","gpt-4o-mini"]}}`},
		{name: "two_node_cycle", value: `{"enabled":true,"general":{"model-a":["model-b"],"model-b":["model-a"]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}

func TestValidateModelFallbackChains_RejectsEmptyModelName(t *testing.T) {
	// 变异:缺少 trim 或缺少空名检查,会让运行时规范化丢弃已配置的
	// source 或 fallback 目标。
	cases := []struct {
		name  string
		value string
	}{
		{name: "empty_source", value: `{"enabled":true,"general":{"  ":["gpt-4o-mini"]}}`},
		{name: "empty_target", value: `{"enabled":true,"general":{"gpt-4o":["  "]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}

func TestValidateModelFallbackChains_AcceptsValid(t *testing.T) {
	// 变异:过严的校验会拒绝受支持的 per-error-class bucket,
	// 或破坏 modelfallback.ParseConfig 所用的运行时解析器形态。
	value := `{"enabled":true,"max_depth":3,"general":{"gpt-4o":["gpt-4o-mini","gpt-4.1-mini"]},"context_window":{"gpt-4o":["gpt-4.1"]},"content_policy":{"*":["policy-safe-model"]}}`
	got, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, value)
	if err != nil {
		t.Fatalf("ValidateValue valid config: %v", err)
	}
	if got != value {
		t.Fatalf("normalized=%q want original config", got)
	}
	resolver, err := modelfallback.ParseConfig(got)
	if err != nil {
		t.Fatalf("ParseConfig after ValidateValue: %v", err)
	}
	if !resolver.Enabled() || resolver.MaxDepth() != 3 {
		t.Fatalf("resolver enabled=%v max_depth=%d want true/3", resolver.Enabled(), resolver.MaxDepth())
	}
	if next := resolver.Resolve("gpt-4o", modelfallback.General, nil); next != "gpt-4o-mini" {
		t.Fatalf("general fallback=%q want gpt-4o-mini", next)
	}
	if next := resolver.Resolve("gpt-4o", modelfallback.ContextWindowExceeded, nil); next != "gpt-4.1" {
		t.Fatalf("context fallback=%q want gpt-4.1", next)
	}
	if next := resolver.Resolve("any-model", modelfallback.ContentPolicy, nil); next != "policy-safe-model" {
		t.Fatalf("content-policy wildcard fallback=%q want policy-safe-model", next)
	}
}

func TestValidateModelFallbackChains_DepthBounds(t *testing.T) {
	// 变异:跳过 max_depth 的边界检查,则超大或负的 fallback 深度会被存下,
	// 使重试行为依赖运行时的默认值/容错处理。
	cases := []struct {
		name  string
		value string
	}{
		{name: "zero", value: `{"enabled":true,"max_depth":0,"general":{"gpt-4o":["gpt-4o-mini"]}}`},
		{name: "negative", value: `{"enabled":true,"max_depth":-1,"general":{"gpt-4o":["gpt-4o-mini"]}}`},
		{name: "too_large", value: `{"enabled":true,"max_depth":11,"general":{"gpt-4o":["gpt-4o-mini"]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := platformsettings.ValidateValue(platformsettings.KeyModelFallbackChains, tc.value)
			if !errors.Is(err, platformsettings.ErrInvalidValue) {
				t.Fatalf("ValidateValue %s err=%v want ErrInvalidValue", tc.name, err)
			}
		})
	}
}
