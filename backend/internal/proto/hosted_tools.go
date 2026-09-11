package proto

import (
	"encoding/json"
	"errors"
	"strings"
)

// ErrHostedToolRouteUnsupported 表示客户端声明了托管/检索档，但目标协议族无法原样表达。
var ErrHostedToolRouteUnsupported = errors.New("proto: hosted retrieval tool cannot be expressed on this protocol family")

// ErrUnknownHostedToolKind 表示工具数组含未识别档，不得静默当函数或剥掉放行。
var ErrUnknownHostedToolKind = errors.New("proto: unknown hosted retrieval tool kind")

func hostedToolKinds() map[string]struct{} {
	return map[string]struct{}{
		"web_search":           {},
		"web_search_preview":   {},
		"file_search":          {},
		"code_interpreter":     {},
		"computer":             {},
		"computer_use_preview": {},
		"image_generation":     {},
		"mcp":                  {},
		"tool_search":          {},
		"apply_patch":          {},
		"shell":                {},
	}
}

// HostedToolKind 报告该字面量是否为官方托管/检索档（不是函数工具）。
func HostedToolKind(kind string) bool {
	_, ok := hostedToolKinds()[strings.TrimSpace(kind)]
	return ok
}

// HostedCallItem 报告响应输出项是否为托管调用项。
func HostedCallItem(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "web_search_call", "file_search_call", "code_interpreter_call",
		"computer_call", "image_generation_call", "mcp_call", "mcp_list_tools",
		"tool_search_call", "apply_patch_call", "shell_call":
		return true
	default:
		return false
	}
}

// ToolKeepsHostedDeclaration 报告该声明是否必须按原对象出站，而不能改写成函数。
func ToolKeepsHostedDeclaration(tool CanonicalTool) bool {
	kind := strings.TrimSpace(tool.Kind)
	if kind == "" || kind == "function" {
		return false
	}
	return HostedToolKind(kind) && len(bytesTrimJSON(tool.Declaration)) > 0
}

// FamilyPreservesHostedTools 报告该出站族能否原样携带托管/检索档。
func FamilyPreservesHostedTools(family string) bool {
	switch strings.TrimSpace(family) {
	case "openai_responses", "openai_codex":
		return true
	default:
		return false
	}
}

// RejectHostedToolsOnFamily 在目标族无法表达托管档时 fail-closed。
func RejectHostedToolsOnFamily(family string, tools []CanonicalTool) error {
	if FamilyPreservesHostedTools(family) {
		return nil
	}
	for _, tool := range tools {
		if ToolKeepsHostedDeclaration(tool) {
			return ErrHostedToolRouteUnsupported
		}
	}
	return nil
}

func bytesTrimJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func hostedCallBillable(kind, status string) (web, file, image int) {
	st := strings.TrimSpace(status)
	if st != "" && st != "completed" && st != "in_progress" {
		return 0, 0, 0
	}
	// 进行中不入账；只把完成态计入按次费。空状态按完成（缓冲响应常见省略）。
	if st == "in_progress" {
		return 0, 0, 0
	}
	switch strings.TrimSpace(kind) {
	case "web_search_call":
		return 1, 0, 0
	case "file_search_call":
		return 0, 1, 0
	case "image_generation_call":
		return 0, 0, 1
	default:
		return 0, 0, 0
	}
}

// AddHostedCallUsage 把已完成的网页/文件/图像托管调用计入按次用量，不发明未接线档单价。
func AddHostedCallUsage(usage CanonicalUsage, kind, status string) CanonicalUsage {
	web, file, image := hostedCallBillable(kind, status)
	usage.WebSearchCalls += web
	usage.FileSearchCalls += file
	usage.ImageGenerationCalls += image
	return usage
}
