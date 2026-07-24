// R7.1：Anthropic Messages API 的 cache_control 状态分析器。
// 当前凭据与协议合同见 docs/HUAKAI工程设计手册.md §4 和 §6。
//
// 只读检查器 + 断点规划器。纯 JSON 遍历——无 IO、无网络、不接触凭据、
// 不修改请求体。R7.2(变换器)是下一个原子步骤。
//
// Anthropic 文档规定每个请求最多 4 个 cache_control 断点。
// 本模块向调用方暴露该上限,并在仍有余量时帮助选择放置新断点的位置。
//
// D5(2026-05-06):为 CacheControlLocation 增加了 TTL 字段。Anthropic 现已
// 支持 {"type":"ephemeral"}(默认 5 分钟)与 {"type":"ephemeral","ttl":"1h"}
// (1 小时)。在请求中,TTL 较长的条目必须排在 TTL 较短的条目之前。
// ValidateTTLOrdering 强制执行该约束。
//
// D6(2026-05-06):增加了每个模型的最小可缓存 token 阈值。
// ModelMinCacheableTokens 把模型 ID 映射到其文档化的最小值。
// MinCacheableTokensForModel 提供带保守回退的查询。
// SuggestBreakpoints 接受可选的 estimatedBlockTokens,以跳过低于
// 每模型阈值的块。
package cachecontrol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// CacheControlMaxAllowed 是 Anthropic 文档化的每请求断点上限。
const CacheControlMaxAllowed = 4

// CacheControlLocation 标识 cache_control 字段被发现的位置。
type CacheControlLocation struct {
	Path  string // "system" | "messages" | "tools"
	Index int    // 数组下标;-1 表示顶层(例如作为单一对象的 system)
	Type  string // cache_control 类型(例如 "ephemeral")
	TTL   string // cache_control 的 ttl:"" = 默认 5 分钟,"1h" = 1 小时
}

// CacheControlSnapshot 汇总一个请求中所有 cache_control 的出现情况。
type CacheControlSnapshot struct {
	Count      int
	Locations  []CacheControlLocation
	MaxAllowed int
}

// BreakpointSuggestion 描述应在哪些位置添加 cache_control,以及哪些位置
// 因 MaxAllowed 上限或 token 阈值而被跳过。
// 两个字段都仅是供人执行的计划——绝不修改请求体。
type BreakpointSuggestion struct {
	Add     []CacheControlLocation
	Skipped []string
}

// ModelMinCacheableTokens 把 Anthropic 模型 ID 映射到其文档化的最小可缓存
// token 阈值。来源:platform.claude.com/docs/en/docs/build-with-claude/prompt-caching
// 取自 2026-05-06。
var ModelMinCacheableTokens = map[string]int{
	// Opus 4.x 系列
	"claude-opus-4-5": 4096,
	"claude-opus-4-6": 4096,
	"claude-opus-4-7": 4096,
	// Opus 4.1 / 4(更早一代)
	"claude-opus-4-1": 1024,
	"claude-opus-4":   1024,
	// Sonnet 4.6
	"claude-sonnet-4-6": 2048,
	// Sonnet 4.5 / 4 / 3.7
	"claude-sonnet-4-5": 1024,
	"claude-sonnet-4":   1024,
	"claude-sonnet-3-7": 1024,
	// Haiku 4.5
	"claude-haiku-4-5": 4096,
	// Haiku 3.5
	"claude-haiku-3-5": 2048,
}

// MinCacheableTokensForModel 返回给定模型的最小可缓存 token 阈值。
// 若模型未知,回退到保守的 4096。
func MinCacheableTokensForModel(model string) int {
	if threshold, ok := ModelMinCacheableTokens[model]; ok {
		return threshold
	}
	return 4096
}

// ValidateTTLOrdering 检查在一个快照内,所有 TTL 较长("1h")的位置都排在
// 所有 TTL 较短("" = 默认 5 分钟)的位置之前。Anthropic 要求 TTL 较长的
// 断点在请求中出现得比 TTL 较短的更靠前。
//
// 顺序有效时返回 nil,违规时返回描述性错误。
func ValidateTTLOrdering(snapshot CacheControlSnapshot) error {
	// 一旦看到短 TTL 条目,后面就不允许再出现长 TTL 条目。
	sawShortTTL := false
	for i, loc := range snapshot.Locations {
		isLong := loc.TTL == "1h"
		isShort := loc.TTL == ""
		if isShort {
			sawShortTTL = true
		}
		if isLong && sawShortTTL {
			return fmt.Errorf(
				"cache_control: TTL ordering violation at index %d (%s[%d]): "+
					"long-TTL (\"1h\") entry must precede all short-TTL (5 min default) entries",
				i, loc.Path, loc.Index,
			)
		}
	}
	return nil
}

// InspectCacheControl 解析一个 Anthropic Messages API 请求体并返回
// cache_control 快照。当 JSON 或 schema 非法(例如缺少 messages、
// role/content 类型错误)时返回错误。
func InspectCacheControl(body []byte) (CacheControlSnapshot, error) {
	root, err := decodeMessagesRequest(body)
	if err != nil {
		return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
	}
	return inspectCacheControlRoot(root)
}

// SuggestBreakpoints 根据当前快照推荐在何处添加 cache_control。
// 优先顺序:最后一个 system 块 → 最后一个 tool 定义 → 最后一条 user 消息。
// 绝不修改请求体。已被占用的位置会被跳过。超出 MaxAllowed 的候选进入 Skipped。
//
// estimatedBlockTokens 是一个可选映射,把 CacheControlLocation 映射到该块的
// 估算 token 数。提供时,估算 token 数低于每模型阈值(取自请求体中的 "model"
// 字段,或回退到 MinCacheableTokensForModel)的候选会进入 Skipped 而非 Add。
// 传 nil 可禁用阈值过滤(保持向后兼容)。
func SuggestBreakpoints(body []byte, snapshot CacheControlSnapshot, estimatedBlockTokens map[CacheControlLocation]int) (BreakpointSuggestion, error) {
	root, err := decodeMessagesRequest(body)
	if err != nil {
		return BreakpointSuggestion{}, err
	}
	if _, err := inspectCacheControlRoot(root); err != nil {
		return BreakpointSuggestion{}, err
	}

	candidates, err := breakpointCandidates(root)
	if err != nil {
		return BreakpointSuggestion{}, err
	}

	maxAllowed := snapshot.MaxAllowed
	if maxAllowed <= 0 {
		maxAllowed = CacheControlMaxAllowed
	}
	remaining := maxAllowed - snapshot.Count
	if remaining < 0 {
		remaining = 0
	}

	// 提供了 token 估算时,确定每模型阈值。
	var tokenThreshold int
	if estimatedBlockTokens != nil {
		model, _ := root["model"].(string)
		tokenThreshold = MinCacheableTokensForModel(model)
	}

	var suggestion BreakpointSuggestion
	for _, candidate := range candidates {
		// 先检查 token 阈值(在上限检查之前)。
		if estimatedBlockTokens != nil {
			tokens, hasEstimate := estimatedBlockTokens[candidate]
			if hasEstimate && tokens < tokenThreshold {
				suggestion.Skipped = append(suggestion.Skipped,
					formatSkippedThreshold(candidate, tokens, tokenThreshold))
				continue
			}
		}

		if remaining > 0 {
			suggestion.Add = append(suggestion.Add, candidate)
			remaining--
			continue
		}
		suggestion.Skipped = append(suggestion.Skipped, formatSkipped(candidate))
	}
	return suggestion, nil
}

func decodeMessagesRequest(body []byte) (map[string]interface{}, error) {
	if len(body) == 0 {
		return nil, errors.New("cache_control: request body is empty")
	}
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("cache_control: invalid JSON: %w", err)
	}
	if root == nil {
		return nil, errors.New("cache_control: request body must be a JSON object")
	}
	return root, nil
}

func inspectCacheControlRoot(root map[string]interface{}) (CacheControlSnapshot, error) {
	snapshot := CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}

	if system, ok := root["system"]; ok {
		if err := inspectSystem(system, &snapshot); err != nil {
			return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
		}
	}

	messages, ok := root["messages"]
	if !ok {
		return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, errors.New("cache_control: messages must be present")
	}
	if err := inspectMessages(messages, &snapshot); err != nil {
		return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
	}

	if tools, ok := root["tools"]; ok {
		if err := inspectTools(tools, &snapshot); err != nil {
			return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
		}
	}

	snapshot.Count = len(snapshot.Locations)
	return snapshot, nil
}

func inspectSystem(value interface{}, snapshot *CacheControlSnapshot) error {
	switch system := value.(type) {
	case string:
		// 纯字符串形式的 system:不可能有 cache_control。
		return nil
	case map[string]interface{}:
		// 单对象形式的 system:若存在,记为顶层(Index=-1)的 cache_control。
		return appendCacheControl(snapshot, system, CacheControlLocation{Path: "system", Index: -1}, "system")
	case []interface{}:
		for i, item := range system {
			block, err := objectAt(item, fmt.Sprintf("system[%d]", i))
			if err != nil {
				return err
			}
			if err := appendCacheControl(snapshot, block, CacheControlLocation{Path: "system", Index: i}, fmt.Sprintf("system[%d]", i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("cache_control: system must be a string, object, or array")
	}
}

func inspectMessages(value interface{}, snapshot *CacheControlSnapshot) error {
	messages, ok := value.([]interface{})
	if !ok {
		return errors.New("cache_control: messages must be an array")
	}
	for i, item := range messages {
		message, err := objectAt(item, fmt.Sprintf("messages[%d]", i))
		if err != nil {
			return err
		}
		role, ok := message["role"].(string)
		if !ok || role == "" {
			return fmt.Errorf("cache_control: messages[%d].role must be a non-empty string", i)
		}
		content, ok := message["content"]
		if !ok {
			return fmt.Errorf("cache_control: messages[%d].content must be present", i)
		}
		if err := inspectMessageContent(content, i, snapshot); err != nil {
			return err
		}
	}
	return nil
}

func inspectMessageContent(value interface{}, messageIndex int, snapshot *CacheControlSnapshot) error {
	switch content := value.(type) {
	case string:
		return nil
	case []interface{}:
		for i, item := range content {
			block, err := objectAt(item, fmt.Sprintf("messages[%d].content[%d]", messageIndex, i))
			if err != nil {
				return err
			}
			location := CacheControlLocation{Path: "messages", Index: messageIndex}
			if err := appendCacheControl(snapshot, block, location, fmt.Sprintf("messages[%d].content[%d]", messageIndex, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("cache_control: messages[%d].content must be a string or array", messageIndex)
	}
}

func inspectTools(value interface{}, snapshot *CacheControlSnapshot) error {
	tools, ok := value.([]interface{})
	if !ok {
		return errors.New("cache_control: tools must be an array")
	}
	for i, item := range tools {
		tool, err := objectAt(item, fmt.Sprintf("tools[%d]", i))
		if err != nil {
			return err
		}
		if err := appendCacheControl(snapshot, tool, CacheControlLocation{Path: "tools", Index: i}, fmt.Sprintf("tools[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

func appendCacheControl(snapshot *CacheControlSnapshot, block map[string]interface{}, location CacheControlLocation, where string) error {
	raw, ok := block["cache_control"]
	if !ok {
		return nil
	}
	cacheType, ttl, err := cacheControlType(raw, where)
	if err != nil {
		return err
	}
	location.Type = cacheType
	location.TTL = ttl
	snapshot.Locations = append(snapshot.Locations, location)
	return nil
}

// cacheControlType 从一个 cache_control 对象中提取 type 与 ttl。
// 返回 (type, ttl, error)。ttl 为 "" 表示默认 5 分钟,"1h" 表示 1 小时。
func cacheControlType(value interface{}, where string) (string, string, error) {
	control, ok := value.(map[string]interface{})
	if !ok {
		return "", "", fmt.Errorf("cache_control: %s.cache_control must be an object", where)
	}
	cacheType, ok := control["type"].(string)
	if !ok || cacheType == "" {
		return "", "", fmt.Errorf("cache_control: %s.cache_control.type must be a non-empty string", where)
	}
	// ttl 是可选的;"" 表示默认 5 分钟。
	ttl, _ := control["ttl"].(string)
	return cacheType, ttl, nil
}

// breakpointCandidates 返回有序的候选:每一轮按 system → tools → messages,
// 每个列表从后往前遍历(最后一个块优先级最高)。
func breakpointCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	system, err := systemCandidates(root)
	if err != nil {
		return nil, err
	}
	tools, err := toolCandidates(root)
	if err != nil {
		return nil, err
	}
	messages, err := userMessageCandidates(root)
	if err != nil {
		return nil, err
	}
	maxLen := len(system)
	if len(tools) > maxLen {
		maxLen = len(tools)
	}
	if len(messages) > maxLen {
		maxLen = len(messages)
	}
	candidates := make([]CacheControlLocation, 0, len(system)+len(tools)+len(messages))
	for i := 0; i < maxLen; i++ {
		if i < len(system) {
			candidates = append(candidates, system[i])
		}
		if i < len(tools) {
			candidates = append(candidates, tools[i])
		}
		if i < len(messages) {
			candidates = append(candidates, messages[i])
		}
	}
	return candidates, nil
}

func systemCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	value, ok := root["system"]
	if !ok {
		return nil, nil
	}
	switch system := value.(type) {
	case string:
		return nil, nil
	case map[string]interface{}:
		if hasCacheControl(system) {
			return nil, nil
		}
		return []CacheControlLocation{{Path: "system", Index: -1, Type: "ephemeral"}}, nil
	case []interface{}:
		var candidates []CacheControlLocation
		for i := len(system) - 1; i >= 0; i-- {
			block, err := objectAt(system[i], fmt.Sprintf("system[%d]", i))
			if err != nil {
				return nil, err
			}
			if !hasCacheControl(block) {
				candidates = append(candidates, CacheControlLocation{Path: "system", Index: i, Type: "ephemeral"})
			}
		}
		return candidates, nil
	default:
		return nil, errors.New("cache_control: system must be a string, object, or array")
	}
}

func toolCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	value, ok := root["tools"]
	if !ok {
		return nil, nil
	}
	tools, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("cache_control: tools must be an array")
	}
	var candidates []CacheControlLocation
	for i := len(tools) - 1; i >= 0; i-- {
		tool, err := objectAt(tools[i], fmt.Sprintf("tools[%d]", i))
		if err != nil {
			return nil, err
		}
		if !hasCacheControl(tool) {
			candidates = append(candidates, CacheControlLocation{Path: "tools", Index: i, Type: "ephemeral"})
		}
	}
	return candidates, nil
}

func userMessageCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	value, ok := root["messages"]
	if !ok {
		return nil, errors.New("cache_control: messages must be present")
	}
	messages, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("cache_control: messages must be an array")
	}
	var candidates []CacheControlLocation
	for i := len(messages) - 1; i >= 0; i-- {
		message, err := objectAt(messages[i], fmt.Sprintf("messages[%d]", i))
		if err != nil {
			return nil, err
		}
		role, ok := message["role"].(string)
		if !ok || role == "" {
			return nil, fmt.Errorf("cache_control: messages[%d].role must be a non-empty string", i)
		}
		if role != "user" {
			continue
		}
		hasControl, err := messageContentHasCacheControl(message, i)
		if err != nil {
			return nil, err
		}
		if !hasControl {
			candidates = append(candidates, CacheControlLocation{Path: "messages", Index: i, Type: "ephemeral"})
		}
	}
	return candidates, nil
}

func messageContentHasCacheControl(message map[string]interface{}, messageIndex int) (bool, error) {
	content, ok := message["content"]
	if !ok {
		return false, fmt.Errorf("cache_control: messages[%d].content must be present", messageIndex)
	}
	switch blocks := content.(type) {
	case string:
		return false, nil
	case []interface{}:
		for i, item := range blocks {
			block, err := objectAt(item, fmt.Sprintf("messages[%d].content[%d]", messageIndex, i))
			if err != nil {
				return false, err
			}
			if hasCacheControl(block) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("cache_control: messages[%d].content must be a string or array", messageIndex)
	}
}

func hasCacheControl(block map[string]interface{}) bool {
	_, ok := block["cache_control"]
	return ok
}

func objectAt(value interface{}, where string) (map[string]interface{}, error) {
	object, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cache_control: %s must be an object", where)
	}
	return object, nil
}

func formatSkipped(location CacheControlLocation) string {
	if location.Index < 0 {
		return fmt.Sprintf("%s[top-level] skipped: cache_control max reached", location.Path)
	}
	return fmt.Sprintf("%s[%d] skipped: cache_control max reached", location.Path, location.Index)
}

func formatSkippedThreshold(location CacheControlLocation, tokens, threshold int) string {
	if location.Index < 0 {
		return fmt.Sprintf("%s[top-level] skipped: estimated %d tokens below threshold %d", location.Path, tokens, threshold)
	}
	return fmt.Sprintf("%s[%d] skipped: estimated %d tokens below threshold %d", location.Path, location.Index, tokens, threshold)
}
