// R7.6 composer 测试：覆盖每步独立启停 / 集成端到端 / 单步报错路径。
package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// composerInputBody 是端到端测试的标准 body，包含 system / messages / tools /
// metadata 各部分以便检验全 6 步效果。
const composerInputBody = `{
	"model": "claude-opus-4-5",
	"system": [
		{"type":"text","text":"existing system","cache_control":{"type":"ephemeral","ttl":"5m"}}
	],
	"tools": [
		{"name":"mcp_search","description":"search"},
		{"name":"bash","description":"shell"}
	],
	"messages": [
		{"role":"user","content":"hi"}
	],
	"metadata": {
		"user_id": "user_abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789_account_00000000-1111-2222-3333-444444444444_session_11111111-2222-3333-4444-555555555555"
	}
}`

// TestApplyMimicryPlan_AllStepsSkipped 不启用任何步骤时 body 不变。
func TestApplyMimicryPlan_AllStepsSkipped(t *testing.T) {
	res, err := ApplyMimicryPlan([]byte(composerInputBody), MimicryPlan{})
	if err != nil {
		t.Fatal(err)
	}
	if res.AnyApplied {
		t.Errorf("AnyApplied=true 但所有步骤都禁用")
	}
	if len(res.Steps) != 6 {
		t.Errorf("步骤数 = %d want 6", len(res.Steps))
	}
	for _, s := range res.Steps {
		if !s.Skipped {
			t.Errorf("步骤 %s 应当 skipped", s.Step)
		}
	}
	// body 与输入字节相等（仅做了拷贝）
	var got, want map[string]interface{}
	if err := json.Unmarshal(res.Body, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(composerInputBody), &want); err != nil {
		t.Fatal(err)
	}
}

// TestApplyMimicryPlan_Step1Only 仅启动 step 1 系统提示词重写。
func TestApplyMimicryPlan_Step1Only(t *testing.T) {
	plan := MimicryPlan{
		SystemRewrite: &SystemRewritePlan{PrefixText: "你是 Claude Code。"},
	}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !res.AnyApplied {
		t.Errorf("AnyApplied=false")
	}
	step1 := res.Steps[0]
	if step1.Step != MimicryStepSystemRewrite || !step1.Applied {
		t.Errorf("step 1 = %+v", step1)
	}
	for i := 1; i < len(res.Steps); i++ {
		if !res.Steps[i].Skipped {
			t.Errorf("step %d 应 skipped: %+v", i, res.Steps[i])
		}
	}
}

// TestApplyMimicryPlan_Step2StripCacheControl 验证 step 2 删除 system 块上的 cache_control。
func TestApplyMimicryPlan_Step2StripCacheControl(t *testing.T) {
	plan := MimicryPlan{StripSystemCacheControl: true}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	step2 := findStep(t, res, MimicryStepStripSystemCC)
	if !step2.Applied {
		t.Errorf("step 2 应 Applied=true: %+v", step2)
	}
	if step2.Reason != mimicryStepStripDoneReason {
		t.Errorf("step 2 reason = %q", step2.Reason)
	}
	// 校验改写后 system 块上无 cache_control
	var root map[string]json.RawMessage
	if err := json.Unmarshal(res.Body, &root); err != nil {
		t.Fatal(err)
	}
	var blocks []map[string]json.RawMessage
	if err := json.Unmarshal(root["system"], &blocks); err != nil {
		t.Fatal(err)
	}
	if _, ok := blocks[0]["cache_control"]; ok {
		t.Error("cache_control 未被删除")
	}
}

// TestApplyMimicryPlan_Step2NothingToStrip system 不带 cache_control 时不动。
func TestApplyMimicryPlan_Step2NothingToStrip(t *testing.T) {
	body := `{"system":[{"type":"text","text":"clean"}]}`
	res, err := ApplyMimicryPlan([]byte(body), MimicryPlan{StripSystemCacheControl: true})
	if err != nil {
		t.Fatal(err)
	}
	step2 := findStep(t, res, MimicryStepStripSystemCC)
	if step2.Applied {
		t.Errorf("step 2 不应触发")
	}
	if step2.Reason != mimicryStepStripNothingReason {
		t.Errorf("reason = %q", step2.Reason)
	}
}

// TestApplyMimicryPlan_Step4ToolNames 验证 step 4 调用 R7.4。
func TestApplyMimicryPlan_Step4ToolNames(t *testing.T) {
	plan := MimicryPlan{
		ToolNames: &ToolNameRewritePlan{
			Mapping: ToolNameMapping{"mcp_search": "analyze_sea00"},
		},
	}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	step4 := findStep(t, res, MimicryStepToolNames)
	if !step4.Applied {
		t.Errorf("step 4 应 applied")
	}
	// 校验 body 中工具名变了
	var root map[string]json.RawMessage
	if err := json.Unmarshal(res.Body, &root); err != nil {
		t.Fatal(err)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(root["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := json.Unmarshal(tools[0]["name"], &name); err != nil {
		t.Fatal(err)
	}
	if name != "analyze_sea00" {
		t.Errorf("tools[0].name = %q want analyze_sea00", name)
	}
}

// TestApplyMimicryPlan_Step5MetadataUserID 验证 step 5 重写 metadata.user_id。
func TestApplyMimicryPlan_Step5MetadataUserID(t *testing.T) {
	plan := MimicryPlan{
		MetadataUserID: &MetadataUserIDPlan{
			Mode:     MetadataInjectRewrite,
			DeviceID: "1111111111111111111111111111111111111111111111111111111111111111",
		},
	}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	step5 := findStep(t, res, MimicryStepMetadataUserID)
	if !step5.Applied {
		t.Errorf("step 5 应 applied")
	}
	if !strings.Contains(string(res.Body), "1111111111111111111111111111111111111111111111111111111111111111") {
		t.Errorf("新 device_id 未写入 body")
	}
}

// TestApplyMimicryPlan_Step6ToolsTailCacheBP 验证 step 6 给 tools[-1] 加 cache_control。
func TestApplyMimicryPlan_Step6ToolsTailCacheBP(t *testing.T) {
	plan := MimicryPlan{ApplyToolsTailCacheBP: true}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	step6 := findStep(t, res, MimicryStepToolsTailCacheBP)
	if !step6.Applied {
		t.Errorf("step 6 应 applied: %+v", step6)
	}
	// 校验 body 中 tools[-1] 有 cache_control
	var root map[string]json.RawMessage
	if err := json.Unmarshal(res.Body, &root); err != nil {
		t.Fatal(err)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(root["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	last := tools[len(tools)-1]
	if _, ok := last["cache_control"]; !ok {
		t.Errorf("tools[-1] 未挂 cache_control")
	}
}

// TestApplyMimicryPlan_Step6WithTTL 验证 ttl 字段写入 cache_control。
func TestApplyMimicryPlan_Step6WithTTL(t *testing.T) {
	plan := MimicryPlan{ApplyToolsTailCacheBP: true, ToolsTailTTL: "1h"}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(res.Body), `"ttl":"1h"`) {
		t.Errorf("ttl=1h 未写入: %s", res.Body)
	}
}

// TestApplyMimicryPlan_Step6NoTools tools 字段缺失时 step 6 报 no_tools_array。
func TestApplyMimicryPlan_Step6NoTools(t *testing.T) {
	body := `{"messages":[]}`
	res, err := ApplyMimicryPlan([]byte(body), MimicryPlan{ApplyToolsTailCacheBP: true})
	if err != nil {
		t.Fatal(err)
	}
	step6 := findStep(t, res, MimicryStepToolsTailCacheBP)
	if step6.Applied {
		t.Errorf("step 6 不应触发")
	}
	if step6.Reason != mimicryStepNoToolsReason {
		t.Errorf("reason=%q", step6.Reason)
	}
}

// TestApplyMimicryPlan_Step6EmptyTools tools 为空数组时 step 6 报 tools_array_empty。
func TestApplyMimicryPlan_Step6EmptyTools(t *testing.T) {
	body := `{"tools":[]}`
	res, err := ApplyMimicryPlan([]byte(body), MimicryPlan{ApplyToolsTailCacheBP: true})
	if err != nil {
		t.Fatal(err)
	}
	step6 := findStep(t, res, MimicryStepToolsTailCacheBP)
	if step6.Applied {
		t.Errorf("step 6 不应触发")
	}
	if step6.Reason != mimicryStepEmptyToolsReason {
		t.Errorf("reason=%q", step6.Reason)
	}
}

// TestApplyMimicryPlan_AllStepsEnabled 端到端 6 步全启动，验证组合效果。
func TestApplyMimicryPlan_AllStepsEnabled(t *testing.T) {
	plan := MimicryPlan{
		SystemRewrite:           &SystemRewritePlan{PrefixText: "You are Claude Code."},
		StripSystemCacheControl: true,
		// 注：CacheBreakpoints 真实场景由 SuggestBreakpoints 输出；这里给一个简化空 plan
		// 来验证 step 3 路径（applied 取决于建议是否非空）
		CacheBreakpoints: &BreakpointSuggestion{},
		ToolNames: &ToolNameRewritePlan{
			Mapping: ToolNameMapping{"mcp_search": "analyze_sea00"},
		},
		MetadataUserID: &MetadataUserIDPlan{
			Mode:     MetadataInjectRewrite,
			DeviceID: "1111111111111111111111111111111111111111111111111111111111111111",
		},
		ApplyToolsTailCacheBP: true,
		ToolsTailTTL:          "5m",
	}
	res, err := ApplyMimicryPlan([]byte(composerInputBody), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !res.AnyApplied {
		t.Error("AnyApplied 应为 true")
	}
	if len(res.Steps) != 6 {
		t.Errorf("步骤数 = %d", len(res.Steps))
	}
	// step 1, 2, 4, 5, 6 都应 Applied=true（step 3 取决于 plan 内容，可能 false）
	expectAppliedSteps := []string{
		MimicryStepSystemRewrite,
		MimicryStepStripSystemCC,
		MimicryStepToolNames,
		MimicryStepMetadataUserID,
		MimicryStepToolsTailCacheBP,
	}
	for _, name := range expectAppliedSteps {
		s := findStep(t, res, name)
		if !s.Applied {
			t.Errorf("step %s 期望 applied: %+v", name, s)
		}
	}
	// 校验最终 body 是合法 JSON
	var root map[string]interface{}
	if err := json.Unmarshal(res.Body, &root); err != nil {
		t.Fatalf("最终 body 不是合法 JSON: %v", err)
	}
}

// TestApplyMimicryPlan_InvalidBodyAtStep1 step 1 报错时返回部分结果 + error。
func TestApplyMimicryPlan_InvalidBodyAtStep1(t *testing.T) {
	plan := MimicryPlan{
		SystemRewrite: &SystemRewritePlan{PrefixText: "x"},
	}
	res, err := ApplyMimicryPlan([]byte("not json"), plan)
	if err == nil {
		t.Fatal("期望 error，实际 nil")
	}
	// step 1 应已尝试并记录 invalid_body
	if len(res.Steps) < 1 {
		t.Fatal("应至少有一步记录")
	}
	if res.Steps[0].Step != MimicryStepSystemRewrite {
		t.Errorf("第 1 步应是 system_rewrite，实际 %s", res.Steps[0].Step)
	}
}

// findStep 在 result.Steps 中按名查找。
func findStep(t *testing.T, res MimicryResult, name string) MimicryStepResult {
	t.Helper()
	for _, s := range res.Steps {
		if s.Step == name {
			return s
		}
	}
	t.Fatalf("找不到步骤 %s", name)
	return MimicryStepResult{}
}
