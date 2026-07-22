// R7.2：Anthropic Messages API 的 cache_control 变更器。
// R7.1 探测器（cache_control.go）的姊妹函数。
// 规格：docs/specs/upstream-credential-management.md §F-AUTH-005 Phase H /
// docs/reference_delta/2026-05-06/vendor-drift-audit.md（D5 TTL 约束）。
//
// 纯 JSON 变更 —— 无 IO、无网络、不接触凭证。
// 按 SuggestBreakpoints（R7.1）规划好的方案插入 cache_control 断点。
// 不变量：InspectCacheControl(result.Body).Count ==
//
//	InspectCacheControl(originalBody).Count + len(result.Applied)
package cachecontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// BreakpointApplyResult 是 ApplyBreakpoints 与
// ApplyBreakpointsWithTTLOrdering 的输出。Body 始终是新分配的内存；
// 原始输入切片绝不会被改动。
type BreakpointApplyResult struct {
	Body    []byte
	Applied []CacheControlLocation
	Skipped []SkipReason
}

// SkipReason 把一个位置与一段人类可读的说明配对，解释为何
// ApplyBreakpoints 拒绝在该处插入 cache_control。
type SkipReason struct {
	Location CacheControlLocation
	Reason   string
}

// skipReasonAlreadyHas 在目标 block 已携带 cache_control 字段时返回。
const skipReasonAlreadyHas = "already has cache_control"

// skipReasonNotFound 在请求的 path/index 在 body 中不存在时返回。
const skipReasonNotFound = "location not found in body"

// skipReasonExceedsCap 在插入断点会使总数超过 CacheControlMaxAllowed 时返回。
const skipReasonExceedsCap = "would exceed cap"

// ApplyBreakpoints 把 plan 描述的 cache_control 断点插入到 body 的副本中。
// 它绝不改动调用方的切片。
//
// 对 plan.Add 中的每个位置：
//   - 若该 block 已有 cache_control → Skipped（"already has cache_control"）。
//   - 若 path/index 无法解析 → Skipped（"location not found in body"）。
//   - 若插入会超过 CacheControlMaxAllowed → Skipped（"would exceed cap"）。
//   - 否则插入 cache_control 对象，并将该位置记入 Applied。
//
// TTL="" 产生 {"type":"ephemeral"}；TTL="1h" 产生
// {"type":"ephemeral","ttl":"1h"}。
//
// 返回的 Body 是从解析后的表示重新序列化的；对象内部的键顺序
// 相对输入可能发生变化。
func ApplyBreakpoints(body []byte, plan BreakpointSuggestion) (BreakpointApplyResult, error) {
	root, err := decodeMessagesRequest(body)
	if err != nil {
		return BreakpointApplyResult{}, err
	}

	// 统计已有断点数，以便知道还剩多少配额。
	snapshot, err := inspectCacheControlRoot(root)
	if err != nil {
		return BreakpointApplyResult{}, err
	}

	var result BreakpointApplyResult
	currentCount := snapshot.Count

	for _, loc := range plan.Add {
		// 配额上限守卫。
		if currentCount >= CacheControlMaxAllowed {
			result.Skipped = append(result.Skipped, SkipReason{Location: loc, Reason: skipReasonExceedsCap})
			continue
		}

		block, notFound, err := resolveBlock(root, loc)
		if err != nil {
			return BreakpointApplyResult{}, err
		}
		if notFound {
			result.Skipped = append(result.Skipped, SkipReason{Location: loc, Reason: skipReasonNotFound})
			continue
		}

		// 已占用守卫。
		if hasCacheControl(block) {
			result.Skipped = append(result.Skipped, SkipReason{Location: loc, Reason: skipReasonAlreadyHas})
			continue
		}

		// 插入 cache_control。
		block["cache_control"] = buildCacheControlObject(loc.TTL)
		currentCount++
		result.Applied = append(result.Applied, loc)
	}

	// 重新序列化。
	out, err := json.Marshal(root)
	if err != nil {
		return BreakpointApplyResult{}, fmt.Errorf("cache_control apply: re-serialization failed: %w", err)
	}
	result.Body = out
	return result, nil
}

// ApplyBreakpointsWithTTLOrdering 行为与 ApplyBreakpoints 完全相同，
// 但在插入前会先对 plan.Add 排序，使较长 TTL 的条目（"1h"）排在
// 较短 TTL 的条目（""）之前。这满足 Anthropic 要求：较长 TTL 的
// 断点必须在请求中更靠前。
//
// 若排序后的方案仍无法产生合法的顺序（例如既有断点本身已违反顺序），
// 则返回 error。
func ApplyBreakpointsWithTTLOrdering(body []byte, plan BreakpointSuggestion) (BreakpointApplyResult, error) {
	if len(plan.Add) == 0 {
		return ApplyBreakpoints(body, plan)
	}

	// 排序："1h"（长）排在 ""（短）之前。稳定排序保留同一 TTL 层级内的
	// 相对顺序。
	sorted := make([]CacheControlLocation, len(plan.Add))
	copy(sorted, plan.Add)
	sort.SliceStable(sorted, func(i, j int) bool {
		return ttlRank(sorted[i].TTL) > ttlRank(sorted[j].TTL)
	})

	sortedPlan := BreakpointSuggestion{
		Add:     sorted,
		Skipped: plan.Skipped,
	}

	result, err := ApplyBreakpoints(body, sortedPlan)
	if err != nil {
		return BreakpointApplyResult{}, err
	}

	// 校验最终 body 是否遵守 TTL 顺序。
	if len(result.Body) > 0 {
		finalSnap, err := InspectCacheControl(result.Body)
		if err != nil {
			return BreakpointApplyResult{}, fmt.Errorf("cache_control apply: post-apply inspect failed: %w", err)
		}
		if err := ValidateTTLOrdering(finalSnap); err != nil {
			return BreakpointApplyResult{}, fmt.Errorf("cache_control apply: TTL ordering cannot be satisfied: %w", err)
		}
	}

	return result, nil
}

// ttlRank 把 TTL 字符串映射为可排序的整数，使较长 TTL 的值排名更高。
// 未知 TTL 字符串排名低于 ""。
func ttlRank(ttl string) int {
	switch ttl {
	case "1h":
		return 2
	case "":
		return 1
	default:
		return 0
	}
}

// buildCacheControlObject 构造要插入的 cache_control map。
// TTL="" → {"type":"ephemeral"}；TTL="1h" → {"type":"ephemeral","ttl":"1h"}。
func buildCacheControlObject(ttl string) map[string]interface{} {
	obj := map[string]interface{}{"type": "ephemeral"}
	if ttl != "" {
		obj["ttl"] = ttl
	}
	return obj
}

// resolveBlock 在 root 中导航，找到 loc 指向的 map[string]interface{}。
// 当 path/index 不存在时返回 (block, notFound=true, nil)。
// 遇结构错误（例如 JSON 类型不对）时返回 (nil, false, err)。
//
// 支持的 path：
//   - "system" 且 Index=-1  → system 作为单个对象
//   - "system" 且 Index>=0  → system 作为数组元素
//   - "messages" 且 Index   → messages[Index] content 的最后一个 block
//     （content 为字符串时则是 message 自身）
//   - "tools"   且 Index    → tools[Index]
func resolveBlock(root map[string]interface{}, loc CacheControlLocation) (block map[string]interface{}, notFound bool, err error) {
	switch loc.Path {
	case "system":
		return resolveSystemBlock(root, loc.Index)
	case "messages":
		return resolveMessageBlock(root, loc.Index)
	case "tools":
		return resolveToolBlock(root, loc.Index)
	default:
		return nil, true, nil // 未知 path → 当作未找到处理
	}
}

func resolveSystemBlock(root map[string]interface{}, index int) (map[string]interface{}, bool, error) {
	value, ok := root["system"]
	if !ok {
		return nil, true, nil
	}
	switch system := value.(type) {
	case string:
		return nil, true, nil
	case map[string]interface{}:
		if index != -1 {
			return nil, true, nil
		}
		return system, false, nil
	case []interface{}:
		if index < 0 || index >= len(system) {
			return nil, true, nil
		}
		block, err := objectAt(system[index], fmt.Sprintf("system[%d]", index))
		if err != nil {
			return nil, false, err
		}
		return block, false, nil
	default:
		return nil, false, errors.New("cache_control apply: system must be a string, object, or array")
	}
}

func resolveMessageBlock(root map[string]interface{}, index int) (map[string]interface{}, bool, error) {
	value, ok := root["messages"]
	if !ok {
		return nil, true, nil
	}
	messages, ok := value.([]interface{})
	if !ok {
		return nil, false, errors.New("cache_control apply: messages must be an array")
	}
	if index < 0 || index >= len(messages) {
		return nil, true, nil
	}
	message, err := objectAt(messages[index], fmt.Sprintf("messages[%d]", index))
	if err != nil {
		return nil, false, err
	}

	// cache_control 应放在最后一个 content block 上（数组情形），或直接放在
	// message 对象上（content 为字符串情形）。对于变更器，我们需要返回那个
	// 应当接收 cache_control 的 block。content 为数组时取最后一个元素；
	// content 为字符串时无法附加 cache_control —— 返回 notFound。
	content, contentOK := message["content"]
	if !contentOK {
		return nil, true, nil
	}
	switch blocks := content.(type) {
	case string:
		// 字符串 content：此处无法放置 cache_control。
		return nil, true, nil
	case []interface{}:
		if len(blocks) == 0 {
			return nil, true, nil
		}
		// 定位最后一个 content block。
		last := blocks[len(blocks)-1]
		block, err := objectAt(last, fmt.Sprintf("messages[%d].content[%d]", index, len(blocks)-1))
		if err != nil {
			return nil, false, err
		}
		return block, false, nil
	default:
		return nil, false, fmt.Errorf("cache_control apply: messages[%d].content must be a string or array", index)
	}
}

func resolveToolBlock(root map[string]interface{}, index int) (map[string]interface{}, bool, error) {
	value, ok := root["tools"]
	if !ok {
		return nil, true, nil
	}
	tools, ok := value.([]interface{})
	if !ok {
		return nil, false, errors.New("cache_control apply: tools must be an array")
	}
	if index < 0 || index >= len(tools) {
		return nil, true, nil
	}
	block, err := objectAt(tools[index], fmt.Sprintf("tools[%d]", index))
	if err != nil {
		return nil, false, err
	}
	return block, false, nil
}

// EnforceCacheControlLimit 裁剪客户端提供的多余 cache_control 断点，
// 使 body 在转发给 Anthropic 之前最多携带 maxBlocks 个断点（Anthropic 硬性
// 上限为 CacheControlMaxAllowed=4，超出的请求一律 400）。
//
// 策略："保留最靠前"—— 按文档顺序遍历 system blocks，再遍历
// messages[].content blocks，保留携带 cache_control 的前 maxBlocks 个 block；
// 其余 block 上的 "cache_control" 键全部删除。这与 InspectCacheControl 使用的
// 优先级顺序一致。
//
// 不变量：
//   - 若 InspectCacheControl(body).Count <= maxBlocks，则原切片以字节级
//     完全相同的形式返回（不分配、不重新序列化）。
//   - 遇任何解码错误时，连同 error 一起返回原切片；调用方可忽略 error 直接
//     原样转发（fail-open）。
//   - 输入切片绝不会被改动。
func EnforceCacheControlLimit(body []byte, maxBlocks int) ([]byte, error) {
	snap, err := InspectCacheControl(body)
	if err != nil {
		// Fail-open：无法解析 → 原样返回原始 body。
		return body, err
	}
	if snap.Count <= maxBlocks {
		// 常见情形：已在上限内 —— 字节级完全相同地直通。
		return body, nil
	}

	root, err := decodeMessagesRequest(body)
	if err != nil {
		return body, err
	}

	kept := 0

	// 按文档顺序遍历 system blocks。
	if system, ok := root["system"]; ok {
		switch s := system.(type) {
		case map[string]interface{}:
			if hasCacheControl(s) {
				if kept < maxBlocks {
					kept++
				} else {
					delete(s, "cache_control")
				}
			}
		case []interface{}:
			for _, item := range s {
				if block, ok := item.(map[string]interface{}); ok {
					if hasCacheControl(block) {
						if kept < maxBlocks {
							kept++
						} else {
							delete(block, "cache_control")
						}
					}
				}
			}
		}
	}

	// 按文档顺序遍历 messages[].content blocks。
	if msgs, ok := root["messages"].([]interface{}); ok {
		for _, msgRaw := range msgs {
			msg, ok := msgRaw.(map[string]interface{})
			if !ok {
				continue
			}
			blocks, ok := msg["content"].([]interface{})
			if !ok {
				continue
			}
			for _, blockRaw := range blocks {
				block, ok := blockRaw.(map[string]interface{})
				if !ok {
					continue
				}
				if hasCacheControl(block) {
					if kept < maxBlocks {
						kept++
					} else {
						delete(block, "cache_control")
					}
				}
			}
		}
	}

	out, err := json.Marshal(root)
	if err != nil {
		return body, fmt.Errorf("cache_control enforce: re-serialization failed: %w", err)
	}
	return out, nil
}
