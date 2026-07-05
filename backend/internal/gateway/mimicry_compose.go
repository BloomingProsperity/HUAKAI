// R7.6：强伪装层 6 步 body 变换组合器。把 R7.1～R7.5 各原子
// 串成完整管线，按调用方配置启停每一步，输出每步审计 + 最终 body。
//
// 规格：docs/specs/upstream-credential-management.md §Phase C 第 27 步：
//
//	应用 6 步 body 变换：system 重写 + system cache_control 剥离
//	+ cache_control 断点注入 + tool 名混淆 + metadata user_id 注入
//	+ tools[-1] 缓存断点。
//
// 步骤映射：
//
//	步骤 1:重写 system                         →  RewriteSystem            (R7.3)
//	步骤 2:剥除 system cache_control            →  内嵌 stripSystemCacheControl
//	步骤 3:注入 cache_control 断点              →  ApplyBreakpoints[WithTTLOrdering] (R7.2)
//	步骤 4:工具名混淆                           →  RewriteToolNames         (R7.4)
//	步骤 5:注入 metadata user_id               →  RewriteMetadataUserID    (R7.5)
//	步骤 6:tools[-1] 缓存断点                   →  内嵌 applyToolsTailCacheBreakpoint
//
// step 2 与 step 6 是组合器内置的小辅助，未单独开原子变更：
//   - step 2 仅遍历 system 数组并在每个块上删除 cache_control 字段；
//     之所以 step 1 之后立刻做，是为了让 step 3 有"干净"的输入分配
//     新断点，避免与既有的 cache_control 互冲。
//   - step 6 在 tools 数组的最后一个元素上挂一个 ephemeral cache_control；
//     用于伪装 Claude Code 默认的工具列表缓存位。
//
// HUAKAI 相对 sub2api 的差异：
//   - 6 步进程内完整审计，每步独立 reason
//   - 任一原子失败时记录部分结果再返回 error（便于 admin 定位）
//   - plan 中每个步骤都可单独 nil/false 关闭，做单步测试
package gateway

import (
	"encoding/json"
	"fmt"
)

// 步骤名常量（封闭枚举，audit/admin 渲染用）。
const (
	MimicryStepSystemRewrite       = "system_rewrite"
	MimicryStepStripSystemCC       = "strip_system_cache_control"
	MimicryStepCacheBreakpoints    = "cache_breakpoints"
	MimicryStepToolNames           = "tool_names"
	MimicryStepMetadataUserID      = "metadata_user_id"
	MimicryStepToolsTailCacheBP    = "tools_tail_cache_breakpoint"
	mimicryStepSkippedReason       = "step_disabled"
	mimicryStepNoToolsReason       = "no_tools_array"
	mimicryStepEmptyToolsReason    = "tools_array_empty"
	mimicryStepStripDoneReason     = "stripped"
	mimicryStepStripNothingReason  = "nothing_to_strip"
	mimicryStepStripNotArrayReason = "system_not_array_skip"
	mimicryStepBPAppliedReason     = "applied"
	mimicryStepTailAlreadyHas      = "tools_tail_already_has_cache_control"
	mimicryStepTailExceedsCap      = "would_exceed_cache_control_cap"
	mimicryStepFeatureDisabled     = "feature_disabled"
)

// MimicryPlan 描述一次 6-step 强伪装管线的入参。任一字段 nil/false 即跳过
// 对应步骤。
//
// Feature flag（一致项）：Enabled=false（默认）时整个管线空操作，
// 全部 6 步标记 Skipped。这是产线安全默认 — 仅当调用方在配置/policy
// 层确认"该 provider 该 binding 应当走强伪装"才显式置 true。详见
// §5。
type MimicryPlan struct {
	// Enabled 是 R7 强伪装层的 feature flag。零值 false 时整个 ApplyMimicryPlan
	// 直接返回 body 拷贝 + 6 step 全标记 Skipped + Reason="feature_disabled"。
	// 仅调用方在 policy 层确认开启时才显式 true。
	Enabled bool
	// SystemRewrite 启动 step 1：系统提示词重写。
	SystemRewrite *SystemRewritePlan
	// StripSystemCacheControl=true 启动 step 2：清除 system 各块上的
	// cache_control（为 step 3 分配新 breakpoint 让路）。
	StripSystemCacheControl bool
	// CacheBreakpoints 启动 step 3：在指定位置注入 cache_control。
	CacheBreakpoints *BreakpointSuggestion
	// UseTTLOrderingForStep3=true 时 step 3 走 TTL 排序版本（长 TTL 在前）。
	UseTTLOrderingForStep3 bool
	// ToolNames 启动 step 4：工具名混淆。
	ToolNames *ToolNameRewritePlan
	// MetadataUserID 启动 step 5：metadata.user_id 重写。
	MetadataUserID *MetadataUserIDPlan
	// ApplyToolsTailCacheBP=true 启动 step 6：tools[-1] 加 ephemeral cache_control。
	ApplyToolsTailCacheBP bool
	// ToolsTailTTL 是 step 6 写入 cache_control 的 ttl 值。空串 = 不带 ttl。
	ToolsTailTTL string
}

// MimicryStepResult 记录单步执行情况。
type MimicryStepResult struct {
	Step    string
	Skipped bool
	Applied bool
	Reason  string
	// Audit 是步骤特定结果（SystemRewriteResult / BreakpointApplyResult 等），
	// admin trace 渲染时按 Step 名分支取出展开。
	Audit interface{}
}

// MimicryResult 是整个管线的产出。
type MimicryResult struct {
	Body       []byte
	Steps      []MimicryStepResult
	AnyApplied bool
}

// ApplyMimicryPlan 串行执行 6 步并返回结果。任一步返回 error 时，前置步骤
// 的 audit 会保留在结果里再附上 error；body 此时是最后一次成功步骤的结果。
//
// Feature flag 短路：plan.Enabled=false 时直接返回 body 拷贝 + 6 步全
// 标记 Skipped + Reason="feature_disabled"，不解析 body、不调任何子原子。
func ApplyMimicryPlan(body []byte, plan MimicryPlan) (MimicryResult, error) {
	out := MimicryResult{Body: append([]byte(nil), body...)}

	if !plan.Enabled {
		for _, step := range []string{
			MimicryStepSystemRewrite,
			MimicryStepStripSystemCC,
			MimicryStepCacheBreakpoints,
			MimicryStepToolNames,
			MimicryStepMetadataUserID,
			MimicryStepToolsTailCacheBP,
		} {
			out.Steps = append(out.Steps, MimicryStepResult{
				Step: step, Skipped: true, Reason: mimicryStepFeatureDisabled,
			})
		}
		return out, nil
	}

	// 步骤 1:重写 system
	if plan.SystemRewrite != nil {
		r, err := RewriteSystem(out.Body, *plan.SystemRewrite)
		out.Steps = append(out.Steps, MimicryStepResult{
			Step: MimicryStepSystemRewrite, Applied: r.Applied, Reason: r.Reason, Audit: r,
		})
		if err != nil {
			return out, fmt.Errorf("mimicry step 1 system rewrite: %w", err)
		}
		out.Body = r.Body
		if r.Applied {
			out.AnyApplied = true
		}
	} else {
		out.Steps = append(out.Steps, MimicryStepResult{Step: MimicryStepSystemRewrite, Skipped: true, Reason: mimicryStepSkippedReason})
	}

	// 步骤 2:剥除 system 的 cache_control
	if plan.StripSystemCacheControl {
		newBody, applied, reason, err := stripSystemCacheControl(out.Body)
		out.Steps = append(out.Steps, MimicryStepResult{
			Step: MimicryStepStripSystemCC, Applied: applied, Reason: reason,
		})
		if err != nil {
			return out, fmt.Errorf("mimicry step 2 strip system cc: %w", err)
		}
		out.Body = newBody
		if applied {
			out.AnyApplied = true
		}
	} else {
		out.Steps = append(out.Steps, MimicryStepResult{Step: MimicryStepStripSystemCC, Skipped: true, Reason: mimicryStepSkippedReason})
	}

	// 步骤 3:注入 cache_control 断点
	if plan.CacheBreakpoints != nil {
		var r BreakpointApplyResult
		var err error
		if plan.UseTTLOrderingForStep3 {
			r, err = ApplyBreakpointsWithTTLOrdering(out.Body, *plan.CacheBreakpoints)
		} else {
			r, err = ApplyBreakpoints(out.Body, *plan.CacheBreakpoints)
		}
		applied := len(r.Applied) > 0
		reason := mimicryStepBPAppliedReason
		if !applied {
			reason = mimicryStepStripNothingReason
		}
		out.Steps = append(out.Steps, MimicryStepResult{
			Step: MimicryStepCacheBreakpoints, Applied: applied, Reason: reason, Audit: r,
		})
		if err != nil {
			return out, fmt.Errorf("mimicry step 3 cache breakpoints: %w", err)
		}
		out.Body = r.Body
		if applied {
			out.AnyApplied = true
		}
	} else {
		out.Steps = append(out.Steps, MimicryStepResult{Step: MimicryStepCacheBreakpoints, Skipped: true, Reason: mimicryStepSkippedReason})
	}

	// 步骤 4:工具名混淆
	if plan.ToolNames != nil {
		r, err := RewriteToolNames(out.Body, *plan.ToolNames)
		out.Steps = append(out.Steps, MimicryStepResult{
			Step: MimicryStepToolNames, Applied: r.Applied, Reason: r.Reason, Audit: r,
		})
		if err != nil {
			return out, fmt.Errorf("mimicry step 4 tool names: %w", err)
		}
		out.Body = r.Body
		if r.Applied {
			out.AnyApplied = true
		}
	} else {
		out.Steps = append(out.Steps, MimicryStepResult{Step: MimicryStepToolNames, Skipped: true, Reason: mimicryStepSkippedReason})
	}

	// 步骤 5:注入 metadata user_id
	if plan.MetadataUserID != nil {
		r, err := RewriteMetadataUserID(out.Body, *plan.MetadataUserID)
		out.Steps = append(out.Steps, MimicryStepResult{
			Step: MimicryStepMetadataUserID, Applied: r.Applied, Reason: r.Reason, Audit: r,
		})
		if err != nil {
			return out, fmt.Errorf("mimicry step 5 metadata user_id: %w", err)
		}
		out.Body = r.Body
		if r.Applied {
			out.AnyApplied = true
		}
	} else {
		out.Steps = append(out.Steps, MimicryStepResult{Step: MimicryStepMetadataUserID, Skipped: true, Reason: mimicryStepSkippedReason})
	}

	// 步骤 6:tools[-1] 缓存断点
	if plan.ApplyToolsTailCacheBP {
		newBody, applied, reason, err := applyToolsTailCacheBreakpoint(out.Body, plan.ToolsTailTTL)
		out.Steps = append(out.Steps, MimicryStepResult{
			Step: MimicryStepToolsTailCacheBP, Applied: applied, Reason: reason,
		})
		if err != nil {
			return out, fmt.Errorf("mimicry step 6 tools tail bp: %w", err)
		}
		out.Body = newBody
		if applied {
			out.AnyApplied = true
		}
	} else {
		out.Steps = append(out.Steps, MimicryStepResult{Step: MimicryStepToolsTailCacheBP, Skipped: true, Reason: mimicryStepSkippedReason})
	}

	return out, nil
}

// stripSystemCacheControl 遍历 body.system 数组并删除每个 text 块上的
// cache_control 字段。返回 (新 body, 是否有删除发生, reason, err)。
//
//   - system 字段不存在 / null / 字符串形态 → applied=false, reason=nothing_to_strip
//   - system 是数组但所有块都没有 cache_control → applied=false, reason=nothing_to_strip
//   - system 是数组且至少一个块的 cache_control 被删 → applied=true, reason=stripped
//   - system 是不支持的形态 → applied=false, reason=system_not_array_skip
func stripSystemCacheControl(body []byte) ([]byte, bool, string, error) {
	root, err := decodeMetaBody(body)
	if err != nil {
		return body, false, reasonMetaInvalidBody, err
	}
	raw, ok := root["system"]
	if !ok || isJSONNull(raw) {
		return appendCopy(body), false, mimicryStepStripNothingReason, nil
	}
	if _, isStr := decodeRawString(raw); isStr {
		return appendCopy(body), false, mimicryStepStripNothingReason, nil
	}
	blocks, err := decodeRawArray(raw)
	if err != nil {
		return appendCopy(body), false, mimicryStepStripNotArrayReason, nil
	}
	stripped := false
	for i, rawBlock := range blocks {
		obj, err := decodeRawObject(rawBlock)
		if err != nil {
			continue
		}
		if _, has := obj["cache_control"]; !has {
			continue
		}
		delete(obj, "cache_control")
		newBlock, err := json.Marshal(obj)
		if err != nil {
			return body, false, mimicryStepStripNothingReason, fmt.Errorf("strip system cc: re-marshal block[%d]: %w", i, err)
		}
		blocks[i] = newBlock
		stripped = true
	}
	if !stripped {
		return appendCopy(body), false, mimicryStepStripNothingReason, nil
	}
	newSystem, err := json.Marshal(blocks)
	if err != nil {
		return body, false, mimicryStepStripNothingReason, fmt.Errorf("strip system cc: re-marshal system: %w", err)
	}
	root["system"] = newSystem
	out, err := json.Marshal(root)
	if err != nil {
		return body, false, mimicryStepStripNothingReason, fmt.Errorf("strip system cc: re-marshal body: %w", err)
	}
	return out, true, mimicryStepStripDoneReason, nil
}

// applyToolsTailCacheBreakpoint 在 body.tools[-1] 上挂 ephemeral cache_control。
// ttl 为空时 cache_control = {"type":"ephemeral"}；非空时 = {"type":"ephemeral","ttl":ttl}。
//
// 守卫两条不变量：
//   - 当前 body 已有 4 个 cache_control 时拒写（CacheControlMaxAllowed）
//   - tools[-1] 已带 cache_control 时拒写，避免覆盖客户原 TTL
func applyToolsTailCacheBreakpoint(body []byte, ttl string) ([]byte, bool, string, error) {
	root, err := decodeMetaBody(body)
	if err != nil {
		return body, false, reasonMetaInvalidBody, err
	}
	raw, ok := root["tools"]
	if !ok {
		return appendCopy(body), false, mimicryStepNoToolsReason, nil
	}
	tools, err := decodeRawArray(raw)
	if err != nil {
		return appendCopy(body), false, mimicryStepNoToolsReason, nil
	}
	if len(tools) == 0 {
		return appendCopy(body), false, mimicryStepEmptyToolsReason, nil
	}
	lastIdx := len(tools) - 1
	lastObj, err := decodeRawObject(tools[lastIdx])
	if err != nil {
		return appendCopy(body), false, mimicryStepEmptyToolsReason, nil
	}
	// 守卫一：tools[-1] 已带 cache_control 时拒写。
	if _, has := lastObj["cache_control"]; has {
		return appendCopy(body), false, mimicryStepTailAlreadyHas, nil
	}
	// 守卫二：检查当前 body 总 cache_control 数量是否达到 cap。InspectCacheControl
	// 走与既有 R7.1 inspector 一致的路径，确保两边对"已有几个"的视角统一。
	snapshot, inspectErr := InspectCacheControl(body)
	if inspectErr == nil && len(snapshot.Locations) >= CacheControlMaxAllowed {
		return appendCopy(body), false, mimicryStepTailExceedsCap, nil
	}
	cc := map[string]string{"type": "ephemeral"}
	if ttl != "" {
		cc["ttl"] = ttl
	}
	ccRaw, err := json.Marshal(cc)
	if err != nil {
		return body, false, mimicryStepEmptyToolsReason, fmt.Errorf("tools tail bp: marshal cc: %w", err)
	}
	lastObj["cache_control"] = ccRaw
	newLast, err := json.Marshal(lastObj)
	if err != nil {
		return body, false, mimicryStepEmptyToolsReason, fmt.Errorf("tools tail bp: re-marshal last tool: %w", err)
	}
	tools[lastIdx] = newLast
	newTools, err := json.Marshal(tools)
	if err != nil {
		return body, false, mimicryStepEmptyToolsReason, fmt.Errorf("tools tail bp: re-marshal tools: %w", err)
	}
	root["tools"] = newTools
	out, err := json.Marshal(root)
	if err != nil {
		return body, false, mimicryStepEmptyToolsReason, fmt.Errorf("tools tail bp: re-marshal body: %w", err)
	}
	return out, true, mimicryStepBPAppliedReason, nil
}

// appendCopy 返回 b 的拷贝。
func appendCopy(b []byte) []byte {
	return append([]byte(nil), b...)
}
