package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

// fakeResolved 造一个含**敏感自由文本**的 Resolved:SystemPrompt / SensitiveWords / ParamOverride
// 各埋一个哨兵串,用于验证投影绝不把它们 echo 出去(§14 区分性变异:若投影回退到露明文,no-leak 断言转红)。
func fakeResolved() registry.Resolved {
	rename := "upstream-real-model-x"
	rpm := int32(60)
	tpm := int32(90000)
	par := int32(5)
	return registry.Resolved{
		PublicAlias:      "claude-3-5-sonnet",
		CanonicalModelID: "anthropic.claude-3-5-sonnet",
		ProviderModelID:  "claude-3-5-sonnet-20241022",
		ContextWindow:    200000,
		PricingClass:     "standard",
		ProtocolFamily:   "anthropic_messages",
		RequestTimeoutMS: 60000,
		Capabilities:     []string{"vision", "tools"},
		PoolCandidates:   []int64{11, 12},
		SnapshotVersion:  "registry:7:3",
		BindingMetadata: []registry.BindingMetadata{
			{
				BindingID:               101,
				PoolGroupID:             11,
				Priority:                1,
				Weight:                  100,
				SelectionMode:           "strict_priority",
				ProviderModelIDOverride: &rename,
				RPMLimit:                &rpm,
				TPMLimit:                &tpm,
				MaxParallelRequests:     &par,
				FallbackClass:           "normal",
				ForceFormat:             true,
				SystemPrompt:            "SENTINEL-SYSPROMPT-must-not-leak",
				SystemPromptOverride:    true,
				SensitiveWords:          []string{"SENTINEL-WORD-must-not-leak"},
				ParamOverride:           map[string]json.RawMessage{"max_tokens": json.RawMessage(`"SENTINEL-PARAM-must-not-leak"`)},
				BodyParamStrips:         []string{"logprobs"},
			},
			{
				BindingID:     102,
				PoolGroupID:   12,
				Priority:      2,
				Weight:        50,
				SelectionMode: "priority_weighted",
				FallbackClass: "context_window",
			},
		},
	}
}

func TestModelResolveDiagnoseSpec(t *testing.T) {
	deps := ModelResolveDiagnoseDeps{
		Resolve: func(_ context.Context, alias string, tenantID int64) (registry.Resolved, error) {
			if tenantID != 7 {
				t.Fatalf("scope leaked: tenantID=%d want 7(必须用已鉴权 req.TenantID)", tenantID)
			}
			if alias != "claude-3-5-sonnet" {
				t.Fatalf("alias 透传错: %q", alias)
			}
			return fakeResolved(), nil
		},
	}
	spec := ModelResolveDiagnoseSpec(deps)

	r := req(7)
	r.Args["model"] = "claude-3-5-sonnet"
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if res.Summary["resolved"] != true {
		t.Fatalf("resolved 应为 true, got %v", res.Summary["resolved"])
	}
	if res.Summary["canonical_model"] != "anthropic.claude-3-5-sonnet" {
		t.Fatalf("canonical_model 错: %v", res.Summary["canonical_model"])
	}
	if res.Summary["provider_model"] != "claude-3-5-sonnet-20241022" {
		t.Fatalf("provider_model 错: %v", res.Summary["provider_model"])
	}
	if res.Summary["binding_count"].(int) != 2 {
		t.Fatalf("binding_count 应 2, got %v", res.Summary["binding_count"])
	}
	bindings := res.Summary["bindings"].([]map[string]any)
	if len(bindings) != 2 {
		t.Fatalf("bindings 应 2 条, got %d", len(bindings))
	}

	// 第一条 binding:路由结构正确投影。
	b0 := bindings[0]
	if b0["pool_group_id"].(int64) != 11 || b0["priority"].(int32) != 1 || b0["selection_mode"] != "strict_priority" {
		t.Fatalf("binding[0] 路由结构错: %v", b0)
	}
	if b0["rpm_limit"] != int32(60) || b0["tpm_limit"] != int32(90000) {
		t.Fatalf("binding[0] 限流投影错: %v", b0)
	}
	if b0["provider_model_rename"] != "upstream-real-model-x" {
		t.Fatalf("binding[0] provider_model_rename 应露出(模型标识符非密钥): %v", b0)
	}

	// 自由文本/业务逻辑配置:只露存在标记/计数,绝不露明文值。
	if b0["has_system_prompt"] != true {
		t.Fatalf("has_system_prompt 应 true(有 SystemPrompt): %v", b0)
	}
	if b0["sensitive_word_count"].(int) != 1 {
		t.Fatalf("sensitive_word_count 应 1, got %v", b0["sensitive_word_count"])
	}
	if b0["param_override_count"].(int) != 1 {
		t.Fatalf("param_override_count 应 1, got %v", b0["param_override_count"])
	}
	if b0["body_param_strip_count"].(int) != 1 {
		t.Fatalf("body_param_strip_count 应 1, got %v", b0["body_param_strip_count"])
	}
	// 投影绝不带明文键,也不带保守裁剪掉的纯 transform 语义标记 system_prompt_override。
	for _, leakKey := range []string{"system_prompt", "sensitive_words", "param_override", "body_param_strips", "status_code_mapping", "system_prompt_override"} {
		if _, has := b0[leakKey]; has {
			t.Fatalf("泄露明文/已裁剪配置键 %q: %v", leakKey, b0)
		}
	}

	// 第二条 binding:无 override 时 provider_model_rename 不出现(omitempty 语义)。
	b1 := bindings[1]
	if _, has := b1["provider_model_rename"]; has {
		t.Fatalf("binding[1] 无 override 不应有 provider_model_rename: %v", b1)
	}
	if b1["has_system_prompt"] != false {
		t.Fatalf("binding[1] 无 SystemPrompt,has_system_prompt 应 false: %v", b1)
	}

	// §14 核心 no-leak:整个 Summary 序列化后绝不含任何敏感哨兵串。
	// 若任何投影回退到 echo 自由文本(SystemPrompt/SensitiveWords/ParamOverride),此断言转红。
	blob, err := json.Marshal(res.Summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	for _, sentinel := range []string{"SENTINEL-SYSPROMPT", "SENTINEL-WORD", "SENTINEL-PARAM"} {
		if strings.Contains(string(blob), sentinel) {
			t.Fatalf("敏感自由文本泄露到工具输出: 命中哨兵 %q\n%s", sentinel, blob)
		}
	}
}

func TestModelResolveDiagnoseNilDep(t *testing.T) {
	r := req(7)
	r.Args["model"] = "claude-3-5-sonnet"
	_, err := ModelResolveDiagnoseSpec(ModelResolveDiagnoseDeps{}).Run(context.Background(), r)
	if !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("nil dep 应 ErrDependencyUnwired, got %v", err)
	}
}

func TestModelResolveDiagnoseMissingArg(t *testing.T) {
	deps := ModelResolveDiagnoseDeps{
		Resolve: func(_ context.Context, _ string, _ int64) (registry.Resolved, error) {
			t.Fatal("缺 model 参数时不应调用 Resolve")
			return registry.Resolved{}, nil
		},
	}
	_, err := ModelResolveDiagnoseSpec(deps).Run(context.Background(), req(7)) // Args 无 model
	if !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("缺 model 应 ErrInvalidArgs, got %v", err)
	}
}

// 解析缺失族(unknown/disabled/no-access)三态全部归一成 resolved=false + **同一** 非 PII 枚举
// "model_not_available",遵循 registry 的 D4 anti-enum 不变量。本测试三态喂不同 registry 错误却断言
// **同一** ErrorClass——若有人把三态重新拆成可区分枚举(泄露"存在但被禁 vs 不存在"信号),此测试转红。
func TestModelResolveDiagnoseUnresolved(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"unknown", registry.ErrUnknownModel},
		{"disabled", registry.ErrModelDisabled},
		{"no_access", registry.ErrTenantNoAccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := ModelResolveDiagnoseDeps{
				Resolve: func(_ context.Context, _ string, _ int64) (registry.Resolved, error) {
					return registry.Resolved{}, tc.err
				},
			}
			r := req(7)
			r.Args["model"] = "ghost-model"
			res, err := ModelResolveDiagnoseSpec(deps).Run(context.Background(), r)
			if err != nil {
				t.Fatalf("解析缺失不应上抛错误, got %v", err)
			}
			if res.Summary["resolved"] != false {
				t.Fatalf("resolved 应 false, got %v", res.Summary["resolved"])
			}
			// 三态必须返回同一对外枚举(anti-enum),绝不泄露区分信号。
			if res.ErrorClass != "model_not_available" {
				t.Fatalf("ErrorClass 应统一为 model_not_available(anti-enum), got %q", res.ErrorClass)
			}
		})
	}
}

// 后端 datastore 故障(ErrRegistryBackend)等非预期错误必须上抛,不被吞成 resolved=false。
func TestModelResolveDiagnoseBackendErrorBubbles(t *testing.T) {
	deps := ModelResolveDiagnoseDeps{
		Resolve: func(_ context.Context, _ string, _ int64) (registry.Resolved, error) {
			return registry.Resolved{}, registry.ErrRegistryBackend
		},
	}
	r := req(7)
	r.Args["model"] = "claude-3-5-sonnet"
	_, err := ModelResolveDiagnoseSpec(deps).Run(context.Background(), r)
	if !errors.Is(err, registry.ErrRegistryBackend) {
		t.Fatalf("后端故障应上抛 ErrRegistryBackend, got %v", err)
	}
}
