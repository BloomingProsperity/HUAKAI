// R7.4：Anthropic Messages API 工具名混淆引擎（强伪装层 6 步 body 变换的
// 第 4 步）。Spec：docs/specs/upstream-credential-management.md §Phase C 第
// 27 步 step 4 of 6。
//
// 纯 JSON 变换，不做 IO/网络/凭据接触。覆盖工具名出现的三个位置：
//   1. 顶层 tools[].name —— 工具声明列表
//   2. messages[].content[] 中 type=="tool_use" 块的 name 字段
//   3. 顶层 tool_choice.name —— 仅当 tool_choice.type=="tool" 时（强制
//      调用某工具的场景；name 必须与 tools[] 中声明的名一致，否则上游报错）
// type=="tool_result" 块只含 tool_use_id，无 name 字段，不触碰。
//
// HUAKAI 相对 sub2api 的差异：映射由调用方传入而非硬编码；纯函数无 service
// 耦合；每次改名输出一行审计 (path, from, to) 便于 admin 查询；幂等（目标
// 名已等于期望值时不重写、不产生审计行）；未知字段经 json.RawMessage
// round-trip 完整保留。
//
// Code-parallel 双 lane 合成（CLAUDE.md #10 + 2026-05-04 directive）：取 Codex
// 紧凑结构 + Claude 的 detectToolUsePresence 精确区分 + 全中文注释。
package gateway

import (
	"encoding/json"
	"fmt"
)

// 出参 Reason 的封闭枚举值。
const (
	reasonToolRenamed      = "renamed"       // 至少一处改名生效
	reasonToolNoMatch      = "no_match"      // 有工具相关字段但无名称命中映射
	reasonToolNoTools      = "no_tools"      // 既无 tools[] 也无 tool_use 块
	reasonToolEmptyMapping = "empty_mapping" // 映射为空 / nil
	reasonToolInvalidBody  = "invalid_body"  // body 解析失败
)

// ToolNameMapping 是调用方提供的原始名→混淆名映射表（精确匹配）。
type ToolNameMapping map[string]string

// ToolNameRewritePlan 是单次重写的入参配置。
type ToolNameRewritePlan struct {
	// Mapping 指定哪些工具名需要被替换。key 是原始名，value 是混淆后名称。
	Mapping ToolNameMapping
}

// ToolRename 记录单次工具名改写的审计信息。
type ToolRename struct {
	// Path 是改写发生的 JSON 路径，例如 "tools[3].name" 或
	// "messages[0].content[2].name"。
	Path string
	// From 是改写前的原始名称。
	From string
	// To 是改写后的目标名称。
	To string
}

// ToolNameRewriteResult 携带重写结果与审计明细。
//
// Reason 取值（封闭枚举）：
//   - "renamed"       至少一处改名生效
//   - "no_match"      有工具但无名称命中映射
//   - "no_tools"      请求中无工具相关字段
//   - "empty_mapping" plan.Mapping 为空 / nil
//   - "invalid_body"  body 解析失败
type ToolNameRewriteResult struct {
	Body    []byte
	Applied bool
	Renames []ToolRename
	Reason  string
}

// rawObject 是用于保留全部字段的 raw 对象类型别名。
type rawObject = map[string]json.RawMessage

// RewriteToolNames 按 plan 改写 body 中所有出现的工具名。永不修改入参切片：
// 未变动时返回 body 的拷贝，变动时返回重新序列化的字节。
func RewriteToolNames(body []byte, plan ToolNameRewritePlan) (ToolNameRewriteResult, error) {
	if len(plan.Mapping) == 0 {
		return toolUnchanged(body, reasonToolEmptyMapping), nil
	}
	root, err := decodeToolBody(body)
	if err != nil {
		return toolUnchanged(body, reasonToolInvalidBody), err
	}

	var renames []ToolRename

	// 步骤一：顶层 tools[]。
	toolsTouched, err := rewriteToolsArray(root, plan.Mapping, &renames)
	if err != nil {
		return toolUnchanged(body, reasonToolInvalidBody), err
	}
	// 步骤二：messages[].content[] 中的 tool_use 块。
	hasToolUse, err := rewriteMessagesToolUse(root, plan.Mapping, &renames)
	if err != nil {
		return toolUnchanged(body, reasonToolInvalidBody), err
	}
	// 步骤三：顶层 tool_choice.name（仅 type=="tool" 时改写）。强制工具
	// 调用场景下，tool_choice.name 必须与 tools[] 同步改名，否则上游会因
	// "无此工具"报错。
	hasToolChoice, err := rewriteToolChoice(root, plan.Mapping, &renames)
	if err != nil {
		return toolUnchanged(body, reasonToolInvalidBody), err
	}

	if !toolsTouched && !hasToolUse && !hasToolChoice {
		return toolUnchanged(body, reasonToolNoTools), nil
	}
	if len(renames) == 0 {
		return toolUnchanged(body, reasonToolNoMatch), nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return ToolNameRewriteResult{}, fmt.Errorf("tool name rewrite: re-serialize: %w", err)
	}
	return ToolNameRewriteResult{Body: out, Applied: true, Renames: renames, Reason: reasonToolRenamed}, nil
}

// rewriteToolsArray 改写 root["tools"] 中每个元素的 name 字段。返回 bool
// 表示 tools 字段是否存在（无论是否产生改写）。
func rewriteToolsArray(root rawObject, mapping ToolNameMapping, renames *[]ToolRename) (bool, error) {
	raw, ok := root["tools"]
	if !ok {
		return false, nil
	}
	items, err := decodeRawArray(raw)
	if err != nil {
		// tools 字段存在但形态非数组 — 直接报错触发 invalid_body。
		return false, fmt.Errorf("tools must be array: %w", err)
	}

	changed := false
	for i, rawItem := range items {
		obj, err := decodeRawObject(rawItem)
		if err != nil {
			continue // 元素不是对象时静默跳过
		}
		path := fmt.Sprintf("tools[%d].name", i)
		if !rewriteNameField(obj, mapping, path, renames) {
			continue
		}
		newRaw, err := json.Marshal(obj)
		if err != nil {
			return true, fmt.Errorf("re-marshal tool[%d]: %w", i, err)
		}
		items[i] = newRaw
		changed = true
	}
	if changed {
		newTools, err := json.Marshal(items)
		if err != nil {
			return true, fmt.Errorf("re-marshal tools[]: %w", err)
		}
		root["tools"] = newTools
	}
	return true, nil
}

// rewriteMessagesToolUse 遍历 root["messages"][].content[]，对每个 type==
// "tool_use" 块改写其 name。返回 bool 表示是否检测到任何 tool_use 块（用于
// 区分 no_tools 与 no_match）。
func rewriteMessagesToolUse(root rawObject, mapping ToolNameMapping, renames *[]ToolRename) (bool, error) {
	raw, ok := root["messages"]
	if !ok {
		return false, nil
	}
	msgs, err := decodeRawArray(raw)
	if err != nil {
		return false, fmt.Errorf("messages must be array: %w", err)
	}

	hasToolUse := false
	msgsChanged := false

	for mi, rawMsg := range msgs {
		msgObj, err := decodeRawObject(rawMsg)
		if err != nil {
			continue
		}
		rawContent, ok := msgObj["content"]
		if !ok {
			continue
		}
		// content 可能是字符串（纯文本）；非数组时直接跳过本条。
		blocks, err := decodeRawArray(rawContent)
		if err != nil {
			continue
		}

		blocksChanged := false
		for ci, rawBlock := range blocks {
			block, err := decodeRawObject(rawBlock)
			if err != nil {
				continue
			}
			t, ok := decodeRawString(block["type"])
			if !ok || t != "tool_use" {
				continue
			}
			hasToolUse = true
			path := fmt.Sprintf("messages[%d].content[%d].name", mi, ci)
			if !rewriteNameField(block, mapping, path, renames) {
				continue
			}
			newBlock, err := json.Marshal(block)
			if err != nil {
				return hasToolUse, fmt.Errorf("re-marshal block: %w", err)
			}
			blocks[ci] = newBlock
			blocksChanged = true
		}
		if blocksChanged {
			newContent, err := json.Marshal(blocks)
			if err != nil {
				return hasToolUse, fmt.Errorf("re-marshal content: %w", err)
			}
			msgObj["content"] = newContent
			newMsg, err := json.Marshal(msgObj)
			if err != nil {
				return hasToolUse, fmt.Errorf("re-marshal message: %w", err)
			}
			msgs[mi] = newMsg
			msgsChanged = true
		}
	}
	if msgsChanged {
		newMsgs, err := json.Marshal(msgs)
		if err != nil {
			return hasToolUse, fmt.Errorf("re-marshal messages[]: %w", err)
		}
		root["messages"] = newMsgs
	}
	return hasToolUse, nil
}

// rewriteToolChoice 改写 root["tool_choice"]：仅当其形如 {"type":"tool",
// "name":"X"} 时，按 mapping 改写 name 字段。其他形态（"auto"/"any"/
// "none" 字符串、{"type":"auto"}、{"type":"any"} 等）一律不触碰。
// 返回 bool 表示 tool_choice 字段是否表明请求"涉及工具"（用于区分
// no_tools 与 no_match）：tool_choice 存在且不是 "none" 时即视为涉及工具。
func rewriteToolChoice(root rawObject, mapping ToolNameMapping, renames *[]ToolRename) (bool, error) {
	raw, ok := root["tool_choice"]
	if !ok {
		return false, nil
	}
	// "none" 字面量明确表示禁用工具，不算"涉及工具"。其他字符串字面量（"auto" /
	// "any"）视为涉及工具，但无 name 字段可改。
	if s, ok := decodeRawString(raw); ok {
		return s != "none", nil
	}
	// 对象形态：必须是 {"type":"tool","name":"X"} 才改写。
	obj, err := decodeRawObject(raw)
	if err != nil {
		return false, fmt.Errorf("tool_choice must be string or object: %w", err)
	}
	t, _ := decodeRawString(obj["type"])
	if t != "tool" {
		// type=="auto" / "any" / "none" / 缺省 —— 都表示无 name 可改。
		// 视为涉及工具（除非显式 none），但不调 rewriteNameField。
		return t != "none", nil
	}
	if !rewriteNameField(obj, mapping, "tool_choice.name", renames) {
		return true, nil
	}
	newRaw, err := json.Marshal(obj)
	if err != nil {
		return true, fmt.Errorf("re-marshal tool_choice: %w", err)
	}
	root["tool_choice"] = newRaw
	return true, nil
}

// rewriteNameField 检查 obj.name 是否在 mapping 中；命中且非幂等情形时改名
// 并追加审计行。返回 true 表示发生改名。
func rewriteNameField(obj rawObject, mapping ToolNameMapping, path string, renames *[]ToolRename) bool {
	from, ok := decodeRawString(obj["name"])
	if !ok {
		return false
	}
	to, ok := mapping[from]
	if !ok || to == from {
		return false
	}
	newName, err := json.Marshal(to)
	if err != nil {
		return false
	}
	obj["name"] = newName
	*renames = append(*renames, ToolRename{Path: path, From: from, To: to})
	return true
}

// decodeToolBody 把 body 解码为顶层 raw 对象。
func decodeToolBody(body []byte) (rawObject, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("tool name rewrite: empty body")
	}
	var root rawObject
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("tool name rewrite: invalid body: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("tool name rewrite: invalid body: expected JSON object")
	}
	return root, nil
}

// decodeRawArray 把 raw 解析为 raw 元素切片。
func decodeRawArray(raw json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// decodeRawObject 把 raw 解析为字段 map。
func decodeRawObject(raw json.RawMessage) (rawObject, error) {
	var obj rawObject
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, fmt.Errorf("nil object")
	}
	return obj, nil
}

// decodeRawString 尝试把 raw 解析为字符串；不是字符串则返回 ok=false。
func decodeRawString(raw json.RawMessage) (string, bool) {
	var s string
	return s, json.Unmarshal(raw, &s) == nil
}

// toolUnchanged 返回原 body 的拷贝。
func toolUnchanged(body []byte, reason string) ToolNameRewriteResult {
	return ToolNameRewriteResult{Body: append([]byte(nil), body...), Reason: reason}
}
