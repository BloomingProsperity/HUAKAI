package dify

import (
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func marshalEnv(nodes ...proto.CapabilityNode) *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "dify-app"
	env.CapabilityGraph.Nodes = nodes
	return env
}

func textNode(id, role, text string) proto.CapabilityNode {
	return proto.CapabilityNode{
		ID:          id,
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text:        &proto.TextNode{Role: role, Block: proto.CanonicalContentBlock{Type: "text", Text: text}},
	}
}

func marshalBody(t *testing.T, env *proto.HCSF) map[string]any {
	t.Helper()
	raw, err := MarshalChatRequest(env)
	if err != nil {
		t.Fatalf("MarshalChatRequest: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body json: %v\n%s", err, raw)
	}
	return body
}

// TestMarshalChatRequestFoldsConversation 抓的回归:消息折叠的 role 前缀/
// 顺序/连接符变化——Dify 单 query 字段是唯一上下文载体,折叠错位=上游看到
// 错乱对话。逐字节断言折叠结果。
// 关键判别:anthropic/openai_responses 入站会把同一段 system 同时写进
// RequestControls.SystemPrompt 和一个 role=system 节点(节点序还在对话尾)。
// SystemPrompt 非空时必须只折叠它一次、跳过全部 system 节点——双折叠会让
// system 重复出现且第二份落在对话末尾(语义扭曲+多计一份 system token)。
func TestMarshalChatRequestFoldsConversation(t *testing.T) {
	env := marshalEnv(
		textNode("n2", "user", "hi"),
		textNode("n3", "assistant", "hello"),
		textNode("n4", "user", "again"),
		// 模拟入站 adapter 的双写:同文本 system 节点追加在消息之后。
		textNode("n1", "system", "be helpful"),
	)
	env.RequestControls.SystemPrompt = "be helpful"
	body := marshalBody(t, env)

	want := "SYSTEM: be helpful\nUSER: hi\nASSISTANT: hello\nUSER: again"
	if got := body["query"]; got != want {
		t.Fatalf("query 折叠错位(system 双折叠或顺序错):\ngot:  %q\nwant: %q", got, want)
	}
	if _, ok := body["inputs"].(map[string]any); !ok {
		t.Fatalf("inputs 必须是对象: %v", body["inputs"])
	}
	if body["auto_generate_name"] != false {
		t.Fatalf("auto_generate_name 必须显式 false: %v", body["auto_generate_name"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("纯文本折叠不应产生 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalChatRequestSystemFromNodesWhenNoControl 抓的回归:SystemPrompt
// 为空时 system 节点是唯一真相源,必须照常折叠(双折叠修复不能误伤单源场景)。
func TestMarshalChatRequestSystemFromNodesWhenNoControl(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "system", "follow the rules"),
		textNode("n2", "user", "hi"),
	)
	body := marshalBody(t, env)
	want := "SYSTEM: follow the rules\nUSER: hi"
	if got := body["query"]; got != want {
		t.Fatalf("无 SystemPrompt 时 system 节点折叠丢失:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestMarshalChatRequestNilPayloadGuards 抓的回归:nil payload 节点(text/
// image)与空 URL locator 必须走 loss 分支——守卫被删时本测试以 panic 红。
func TestMarshalChatRequestNilPayloadGuards(t *testing.T) {
	env := marshalEnv(
		proto.CapabilityNode{ID: "nt", Kind: proto.CapabilityText, Text: nil},
		proto.CapabilityNode{ID: "ni", Kind: proto.CapabilityImage, Image: nil},
		proto.CapabilityNode{ID: "nu", Kind: proto.CapabilityImage, Image: &proto.ImageNode{SourceKind: proto.DataSourceURL}},
		textNode("n2", "user", "hi"),
	)
	body := marshalBody(t, env)
	if got := body["query"]; got != "USER: hi" {
		t.Fatalf("nil payload 节点不应进折叠: %q", got)
	}
	if _, hasFiles := body["files"]; hasFiles {
		t.Fatalf("坏 image 节点不应产出 files: %v", body["files"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 3 {
		t.Fatalf("三个坏节点应各记一条 loss,got %d: %+v", len(env.CapabilityGraph.ProtocolLoss), env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalChatRequestResponseMode 抓的回归:response_mode 写反/恒定——
// Dify 的流式开关只有这个字段,写错=非流请求被上游按 SSE 回、流式请求被
// 阻塞缓冲。
func TestMarshalChatRequestResponseMode(t *testing.T) {
	stream := marshalEnv(textNode("n1", "user", "hi"))
	stream.StreamPlan.Mode = proto.StreamModeStreaming
	if got := marshalBody(t, stream)["response_mode"]; got != "streaming" {
		t.Fatalf("StreamPlan=streaming 时 response_mode=%v want streaming", got)
	}

	buffered := marshalEnv(textNode("n1", "user", "hi"))
	buffered.StreamPlan.Mode = proto.StreamModeBuffered
	if got := marshalBody(t, buffered)["response_mode"]; got != "blocking" {
		t.Fatalf("StreamPlan=buffered 时 response_mode=%v want blocking", got)
	}
}

// TestMarshalChatRequestUserIdentifier 抓的回归:user 字段为空——Dify user
// 必填,空值直接 400;有 RequestID 用之,无则固定兜底。
func TestMarshalChatRequestUserIdentifier(t *testing.T) {
	withID := marshalEnv(textNode("n1", "user", "hi"))
	withID.RequestMeta.RequestID = "req_42"
	if got := marshalBody(t, withID)["user"]; got != "req_42" {
		t.Fatalf("user=%v want req_42", got)
	}

	withoutID := marshalEnv(textNode("n1", "user", "hi"))
	got, ok := marshalBody(t, withoutID)["user"].(string)
	if !ok || got == "" {
		t.Fatalf("无 RequestID 时 user 不可为空, got=%v", got)
	}
	if got != "huakai" {
		t.Fatalf("无 RequestID 时 user=%q want huakai", got)
	}
}

// TestMarshalChatRequestToolNodeLoss 抓的回归:tool 节点被静默丢弃(无 loss)
// 或被误投影成 body 里的 tools 字段(Dify 无工具协议,上游会拒)。
func TestMarshalChatRequestToolNodeLoss(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "user", "hi"),
		proto.CapabilityNode{
			ID:          "n_tool_use_1",
			Kind:        proto.CapabilityToolUse,
			StreamReady: proto.StreamReadyPartial,
			ToolUse:     &proto.ToolUseNode{ToolCallID: "call_1", Name: "lookup", Input: json.RawMessage(`{"q":"x"}`)},
		},
	)
	body := marshalBody(t, env)
	if _, ok := body["tools"]; ok {
		t.Fatalf("dify body 不得含 tools 字段: %v", body)
	}
	if body["query"] != "USER: hi" {
		t.Fatalf("tool 节点不得污染 query: %v", body["query"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 1 {
		t.Fatalf("tool 节点应记恰一条 loss, got=%+v", env.CapabilityGraph.ProtocolLoss)
	}
	loss := env.CapabilityGraph.ProtocolLoss[0]
	if loss.Capability != proto.CapabilityToolUse || loss.NodeID != "n_tool_use_1" || loss.Code != "unsupported_capability" {
		t.Fatalf("tool loss 字段不齐: %+v", loss)
	}
}

// TestMarshalChatRequestRemoteImageFile 抓的回归:远程图片分支结构体欠初始化
// (panic / 空字段)——files[0] 三字段逐一全断言。
func TestMarshalChatRequestRemoteImageFile(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "user", "what is this"),
		proto.CapabilityNode{
			ID:          "n_image_1",
			Kind:        proto.CapabilityImage,
			StreamReady: proto.StreamReadyYes,
			Image: &proto.ImageNode{
				SourceKind: proto.DataSourceURL,
				MediaType:  "image/png",
				Locator:    proto.DataLocator{Kind: proto.DataSourceURL, Value: "https://example.test/cat.png"},
			},
		},
	)
	body := marshalBody(t, env)
	files, ok := body["files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("files 应恰有 1 项: %v", body["files"])
	}
	file, ok := files[0].(map[string]any)
	if !ok {
		t.Fatalf("files[0] 不是对象: %v", files[0])
	}
	if file["type"] != "image" {
		t.Errorf("files[0].type=%v want image", file["type"])
	}
	if file["transfer_method"] != "remote_url" {
		t.Errorf("files[0].transfer_method=%v want remote_url", file["transfer_method"])
	}
	if file["url"] != "https://example.test/cat.png" {
		t.Errorf("files[0].url=%v want https://example.test/cat.png", file["url"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("URL 图片应无 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalChatRequestBase64ImageDropped 抓的回归:base64 图片被静默吞
// (无 loss)或被错误塞进 files(v1 契约禁子请求上传,只支持 remote_url)。
func TestMarshalChatRequestBase64ImageDropped(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "user", "look"),
		proto.CapabilityNode{
			ID:          "n_image_b64",
			Kind:        proto.CapabilityImage,
			StreamReady: proto.StreamReadyYes,
			Image: &proto.ImageNode{
				SourceKind: proto.DataSourceInlineBase64,
				MediaType:  "image/png",
				Locator:    proto.DataLocator{Kind: proto.DataSourceInlineBase64, Value: "iVBORw0KGgo="},
			},
		},
	)
	body := marshalBody(t, env)
	if _, ok := body["files"]; ok {
		t.Fatalf("base64 图片不得进 files: %v", body["files"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 1 {
		t.Fatalf("base64 图片应记恰一条 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
	loss := env.CapabilityGraph.ProtocolLoss[0]
	if loss.Code != "unsupported_image_source" || loss.NodeID != "n_image_b64" {
		t.Fatalf("base64 loss 字段不齐: %+v", loss)
	}
}

// TestMarshalChatRequestControlsRecordLoss 抓的回归:per-request 控制项
// (max_tokens 等)被静默蒸发(无 loss)或被错误写进 Dify body。
func TestMarshalChatRequestControlsRecordLoss(t *testing.T) {
	env := marshalEnv(textNode("n1", "user", "hi"))
	maxTokens := 64
	temperature := 0.5
	env.RequestControls.MaxTokens = &maxTokens
	env.RequestControls.Temperature = &temperature
	env.RequestControls.Tools = []proto.CanonicalTool{{Name: "lookup"}}

	body := marshalBody(t, env)
	for _, key := range []string{"max_tokens", "temperature", "tools"} {
		if _, ok := body[key]; ok {
			t.Errorf("dify body 不得含 %q 字段: %v", key, body)
		}
	}
	gotFields := map[string]bool{}
	for _, loss := range env.CapabilityGraph.ProtocolLoss {
		if loss.Code == "unsupported_request_control" {
			gotFields[loss.Field] = true
		}
	}
	for _, field := range []string{"max_tokens", "temperature", "tools"} {
		if !gotFields[field] {
			t.Errorf("control %q 被丢弃但未记 loss: %+v", field, env.CapabilityGraph.ProtocolLoss)
		}
	}
}
