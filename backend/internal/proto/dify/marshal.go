package dify

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// marshalVendor 是 ProtocolLossEntry.Vendor 的统一标识。
const marshalVendor = "dify_chat"

// fallbackUser 是 RequestMeta 无 RequestID 时的兜底 user 标识（Dify user 必填）。
const fallbackUser = "huakai"

// MarshalChatRequest 把 HCSF envelope 投影成 Dify 会话请求 body。
//
// Dify 没有 per-request 模型/采样参数，也没有结构化 messages 数组：整个对话
// 折叠进单 query 字符串（每条消息一行，role 前缀 SYSTEM:/USER:/ASSISTANT:）。
// 任何 Dify 表达不了的 capability / request control 必须在
// env.CapabilityGraph.ProtocolLoss 记账，禁止静默丢弃。
func MarshalChatRequest(env *proto.HCSF) ([]byte, error) {
	if env == nil {
		return nil, errors.New("dify: nil HCSF envelope")
	}

	var lines []string
	var files []requestFile

	// system 文本的真相源二选一:入站 adapter(anthropic/openai_responses)会把
	// 同一段 system 同时写进 RequestControls.SystemPrompt 和一个 role=system 的
	// 文本节点——两处都折叠会让 system 在 query 里出现两次(且节点序在对话尾,
	// 语义扭曲 + 上游多计一份 system token)。约定:SystemPrompt 非空时它是
	// 唯一真相源,折叠在 query 首行,所有 system 节点跳过;SystemPrompt 为空
	// 时才按节点折叠 system 行。
	systemFromControls := env.RequestControls.SystemPrompt != ""
	if systemFromControls {
		lines = append(lines, "SYSTEM: "+env.RequestControls.SystemPrompt)
	}
	for _, n := range env.CapabilityGraph.Nodes {
		switch n.Kind {
		case proto.CapabilityText:
			if n.Text == nil {
				addNodeLoss(env, n, "text node missing payload", "missing_text_payload")
				continue
			}
			if systemFromControls && n.Text.Role == "system" {
				continue
			}
			lines = append(lines, rolePrefix(n.Text.Role)+n.Text.Block.Text)
		case proto.CapabilityImage:
			if file, ok := imageRequestFile(env, n); ok {
				files = append(files, file)
			}
		default:
			addNodeLoss(env, n, "capability unsupported by dify_chat marshal", "unsupported_capability")
		}
	}
	recordRequestControlLosses(env)

	body := chatRequest{
		Inputs:           map[string]any{},
		Query:            strings.Join(lines, "\n"),
		ResponseMode:     responseModeFor(env),
		User:             userIdentifier(env),
		AutoGenerateName: false,
		Files:            files,
	}
	return json.Marshal(body)
}

// rolePrefix 把 canonical role 折叠为行前缀；未知 role 按 USER 兜底。
func rolePrefix(role string) string {
	switch role {
	case "system":
		return "SYSTEM: "
	case "assistant":
		return "ASSISTANT: "
	default:
		return "USER: "
	}
}

// responseModeFor 由 StreamPlan 决定 response_mode；Dify 的流式开关在 body
// 内，不走顶层 stream 字段。
func responseModeFor(env *proto.HCSF) string {
	if env.StreamPlan.Mode == proto.StreamModeStreaming {
		return "streaming"
	}
	return "blocking"
}

// userIdentifier 取 RequestMeta.RequestID 作 user；缺失时用固定兜底值，
// 保证 Dify 必填字段恒非空。
func userIdentifier(env *proto.HCSF) string {
	if id := env.RequestMeta.RequestID; id != "" {
		return id
	}
	return fallbackUser
}

// imageRequestFile 把 image 节点投影为 files 元素。仅支持远程 URL；
// 返回值是逐字段显式构造的完整结构体——远程图片分支绝不允许依赖
// 半初始化的指针/零值结构（本仓不变量：nil 解引用类缺陷由回归测试钉死）。
// base64 输入按适配器契约（BuildRequest 不可发子请求，无法上传）记 LOSSY 丢弃。
func imageRequestFile(env *proto.HCSF, n proto.CapabilityNode) (requestFile, bool) {
	if n.Image == nil {
		addNodeLoss(env, n, "image node missing payload", "missing_image_payload")
		return requestFile{}, false
	}
	switch n.Image.SourceKind {
	case proto.DataSourceURL:
		if n.Image.Locator.Value == "" {
			addNodeLoss(env, n, "image url locator empty", "missing_image_url")
			return requestFile{}, false
		}
		return requestFile{
			Type:           "image",
			TransferMethod: "remote_url",
			URL:            n.Image.Locator.Value,
		}, true
	case proto.DataSourceInlineBase64:
		addNodeLoss(env, n, "inline base64 image cannot be projected; dify_chat files only accept remote_url and the adapter contract forbids upload sub-requests", "unsupported_image_source")
		return requestFile{}, false
	default:
		addNodeLoss(env, n, "image source kind unsupported by dify_chat files projection", "unsupported_image_source")
		return requestFile{}, false
	}
}

// recordRequestControlLosses 为 Dify 不存在的 per-request 控制项记账。
// 这些参数在 Dify 由 app 侧配置，body 里没有对应字段可写。
func recordRequestControlLosses(env *proto.HCSF) {
	c := env.RequestControls
	if c.MaxTokens != nil {
		addControlLoss(env, "max_tokens")
	}
	if c.Temperature != nil {
		addControlLoss(env, "temperature")
	}
	if c.TopP != nil {
		addControlLoss(env, "top_p")
	}
	if len(c.Stop) > 0 || len(c.StopSequences) > 0 {
		addControlLoss(env, "stop")
	}
	if len(c.Tools) > 0 {
		addControlLoss(env, "tools")
	}
	if len(c.ToolChoice) > 0 {
		addControlLoss(env, "tool_choice")
	}
	if c.ParallelToolCalls != nil {
		addControlLoss(env, "parallel_tool_calls")
	}
	if c.ResponseFormat != nil {
		addControlLoss(env, "response_format")
	}
	if c.Seed != nil {
		addControlLoss(env, "seed")
	}
}

// addNodeLoss 把 capability 节点级 marshal loss 追加到 envelope 图。
func addNodeLoss(env *proto.HCSF, n proto.CapabilityNode, reason, code string) {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, reason, code, n.Kind, n.ID)
	loss.Direction = string(proto.DirectionCanonicalToUpstream)
	loss.Vendor = marshalVendor
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
}

// addControlLoss 把 request control 级 marshal loss 追加到 envelope 图。
func addControlLoss(env *proto.HCSF, field string) {
	loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, "request control has no dify_chat projection; the Dify app configuration governs it", "unsupported_request_control", "", "")
	loss.Direction = string(proto.DirectionCanonicalToUpstream)
	loss.Vendor = marshalVendor
	loss.Field = field
	env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
}
