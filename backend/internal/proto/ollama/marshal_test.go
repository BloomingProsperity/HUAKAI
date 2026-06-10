package ollama

import (
	"encoding/json"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func marshalEnv(nodes ...proto.CapabilityNode) *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "llama3.2"
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

func bodyMessages(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	rawMsgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages 不是数组: %v", body["messages"])
	}
	msgs := make([]map[string]any, 0, len(rawMsgs))
	for _, m := range rawMsgs {
		msg, ok := m.(map[string]any)
		if !ok {
			t.Fatalf("message 不是对象: %v", m)
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// TestMarshalOptionsNesting 抓的回归:采样参数泄到顶层或 num_predict 改名
// 失败——Ollama 只认 options{} 内的采样参数,顶层 temperature/max_tokens 是
// openai 形协议污染(上游静默忽略=用户参数失效)。
func TestMarshalOptionsNesting(t *testing.T) {
	env := marshalEnv(textNode("n1", "user", "hi"))
	maxTokens := 128
	temperature := 0.7
	topP := 0.9
	seed := 42
	env.RequestControls.MaxTokens = &maxTokens
	env.RequestControls.Temperature = &temperature
	env.RequestControls.TopP = &topP
	env.RequestControls.Seed = &seed
	env.RequestControls.Stop = []string{"END"}

	body := marshalBody(t, env)
	for _, key := range []string{"max_tokens", "num_predict", "temperature", "top_p", "seed", "stop"} {
		if _, ok := body[key]; ok {
			t.Errorf("采样参数 %q 不得出现在顶层(必须嵌 options{}): %v", key, body)
		}
	}
	options, ok := body["options"].(map[string]any)
	if !ok {
		t.Fatalf("options 必须是对象: %v", body["options"])
	}
	if got := options["num_predict"]; got != float64(128) {
		t.Errorf("options.num_predict=%v want 128(MaxTokens 必须改名 num_predict)", got)
	}
	if _, ok := options["max_tokens"]; ok {
		t.Errorf("options 内不得出现 max_tokens 原名: %v", options)
	}
	if got := options["temperature"]; got != 0.7 {
		t.Errorf("options.temperature=%v want 0.7", got)
	}
	if got := options["top_p"]; got != 0.9 {
		t.Errorf("options.top_p=%v want 0.9", got)
	}
	if got := options["seed"]; got != float64(42) {
		t.Errorf("options.seed=%v want 42", got)
	}
	stop, ok := options["stop"].([]any)
	if !ok || len(stop) != 1 || stop[0] != "END" {
		t.Errorf("options.stop=%v want [END]", options["stop"])
	}
	// 全部控制都可表达,不应记 loss。
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("可表达控制不应产生 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalStreamExplicit 抓的回归:stream 字段缺失或写反——Ollama 默认
// stream=true,非流请求漏写显式 false 会被 NDJSON 流式响应打爆;流式请求
// 必须显式 true。两个方向都断言字段存在且值正确。
func TestMarshalStreamExplicit(t *testing.T) {
	buffered := marshalEnv(textNode("n1", "user", "hi"))
	buffered.StreamPlan.Mode = proto.StreamModeBuffered
	body := marshalBody(t, buffered)
	got, present := body["stream"]
	if !present {
		t.Fatal("非流式 body 必须显式携带 stream 字段(Ollama 缺省按 true 处理)")
	}
	if got != false {
		t.Fatalf("非流式 stream=%v want false", got)
	}

	streaming := marshalEnv(textNode("n1", "user", "hi"))
	streaming.StreamPlan.Mode = proto.StreamModeStreaming
	if got := marshalBody(t, streaming)["stream"]; got != true {
		t.Fatalf("流式 stream=%v want true", got)
	}
}

// TestMarshalSystemSingleSource 抓的回归:SystemPrompt 与 system 节点双写时
// system 消息重复出现(且第二份落在对话尾,语义扭曲+多计 system token)。
// SystemPrompt 非空时它是唯一真相源,作为首条消息,system 节点全部跳过。
func TestMarshalSystemSingleSource(t *testing.T) {
	env := marshalEnv(
		textNode("n2", "user", "hi"),
		textNode("n3", "assistant", "hello"),
		// 模拟入站 adapter 的双写:同文本 system 节点追加在消息之后。
		textNode("n1", "system", "be helpful"),
	)
	env.RequestControls.SystemPrompt = "be helpful"
	msgs := bodyMessages(t, marshalBody(t, env))
	if len(msgs) != 3 {
		t.Fatalf("应恰 3 条消息(system 双折叠或节点丢失): %v", msgs)
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "be helpful" {
		t.Fatalf("首条必须是 system 消息: %v", msgs[0])
	}
	if msgs[1]["role"] != "user" || msgs[2]["role"] != "assistant" {
		t.Fatalf("对话序错位: %v", msgs)
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("纯文本投影不应产生 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalSystemFromNodesWhenNoControl 抓的回归:SystemPrompt 为空时
// system 节点是唯一真相源,必须按节点序保留(双写修复不能误伤单源场景)。
func TestMarshalSystemFromNodesWhenNoControl(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "system", "follow the rules"),
		textNode("n2", "user", "hi"),
	)
	msgs := bodyMessages(t, marshalBody(t, env))
	if len(msgs) != 2 || msgs[0]["role"] != "system" || msgs[0]["content"] != "follow the rules" {
		t.Fatalf("无 SystemPrompt 时 system 节点投影丢失: %v", msgs)
	}
}

// TestMarshalToolUseAndResult 抓的回归:tool_use 节点的 arguments 被编码成
// 字符串(OpenAI 形)而非 JSON 对象,或 tool_result 没落成 role:"tool" 消息。
func TestMarshalToolUseAndResult(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "user", "weather?"),
		proto.CapabilityNode{
			ID:          "n_tool_use_1",
			Kind:        proto.CapabilityToolUse,
			StreamReady: proto.StreamReadyPartial,
			ToolUse:     &proto.ToolUseNode{ToolCallID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"sf"}`), Status: proto.ToolNodeComplete},
		},
		proto.CapabilityNode{
			ID:          "n_tool_result_1",
			Kind:        proto.CapabilityToolResult,
			StreamReady: proto.StreamReadyYes,
			ToolResult: &proto.ToolResultNode{
				ToolCallID: "call_1",
				Content:    []proto.CanonicalContentBlock{{Type: "text", Text: "sunny"}},
				Status:     proto.ToolNodeComplete,
			},
		},
	)
	msgs := bodyMessages(t, marshalBody(t, env))
	if len(msgs) != 3 {
		t.Fatalf("应恰 3 条消息: %v", msgs)
	}

	assistant := msgs[1]
	if assistant["role"] != "assistant" {
		t.Fatalf("tool_use 必须落 assistant 消息: %v", assistant)
	}
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant.tool_calls 应恰 1 项: %v", assistant["tool_calls"])
	}
	fn, ok := calls[0].(map[string]any)["function"].(map[string]any)
	if !ok {
		t.Fatalf("tool_calls[0].function 不是对象: %v", calls[0])
	}
	if fn["name"] != "get_weather" {
		t.Errorf("function.name=%v want get_weather", fn["name"])
	}
	args, ok := fn["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("arguments 必须是 JSON 对象(不是字符串): %T %v", fn["arguments"], fn["arguments"])
	}
	if args["city"] != "sf" {
		t.Errorf("arguments.city=%v want sf", args["city"])
	}

	toolMsg := msgs[2]
	if toolMsg["role"] != "tool" || toolMsg["content"] != "sunny" {
		t.Fatalf("tool_result 必须落 role:tool 消息: %v", toolMsg)
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("tool 链路可表达,不应产生 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalControlToolsTopLevel 抓的回归:RequestControls.Tools 没投影到
// 顶层 tools(OpenAI 形 function 声明)或被错误嵌进 options。
func TestMarshalControlToolsTopLevel(t *testing.T) {
	env := marshalEnv(textNode("n1", "user", "hi"))
	env.RequestControls.Tools = []proto.CanonicalTool{{
		Name:        "lookup",
		Description: "find things",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	body := marshalBody(t, env)
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("顶层 tools 应恰 1 项: %v", body["tools"])
	}
	decl, ok := tools[0].(map[string]any)
	if !ok || decl["type"] != "function" {
		t.Fatalf("tools[0] 应为 OpenAI 形 function 声明: %v", tools[0])
	}
	fn, ok := decl["function"].(map[string]any)
	if !ok || fn["name"] != "lookup" || fn["description"] != "find things" {
		t.Fatalf("function 声明字段不齐: %v", decl["function"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("tools 可表达,不应产生 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalImageBase64AttachesToPreviousUserMessage 抓的回归:base64 图片
// 没追加到上一条 user 消息的 images[](Ollama 多模态唯一载体),或 URL 图片
// 被静默吞(无 loss)——URL 形态 Ollama 不可表达,必须记 LOSSY。
func TestMarshalImageBase64AttachesToPreviousUserMessage(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "user", "what is this"),
		proto.CapabilityNode{
			ID:          "n_image_1",
			Kind:        proto.CapabilityImage,
			StreamReady: proto.StreamReadyYes,
			Image: &proto.ImageNode{
				SourceKind: proto.DataSourceInlineBase64,
				MediaType:  "image/png",
				Locator:    proto.DataLocator{Kind: proto.DataSourceInlineBase64, Value: "iVBORw0KGgo="},
			},
		},
		proto.CapabilityNode{
			ID:          "n_image_url",
			Kind:        proto.CapabilityImage,
			StreamReady: proto.StreamReadyYes,
			Image: &proto.ImageNode{
				SourceKind: proto.DataSourceURL,
				MediaType:  "image/png",
				Locator:    proto.DataLocator{Kind: proto.DataSourceURL, Value: "https://example.test/cat.png"},
			},
		},
	)
	msgs := bodyMessages(t, marshalBody(t, env))
	if len(msgs) != 1 {
		t.Fatalf("base64 图片应追加进上一条 user 消息而非新建: %v", msgs)
	}
	images, ok := msgs[0]["images"].([]any)
	if !ok || len(images) != 1 || images[0] != "iVBORw0KGgo=" {
		t.Fatalf("user 消息 images=%v want [iVBORw0KGgo=]", msgs[0]["images"])
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 1 {
		t.Fatalf("URL 图片应记恰一条 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
	loss := env.CapabilityGraph.ProtocolLoss[0]
	if loss.Code != "unsupported_image_source" || loss.NodeID != "n_image_url" {
		t.Fatalf("URL 图片 loss 字段不齐: %+v", loss)
	}
}

// TestMarshalImageWithoutPrecedingUserMessage 抓的回归:首节点即图片时 images
// 追加 panic 或图片被丢——无上一条 user 消息时必须新建空文本 user 消息承载。
func TestMarshalImageWithoutPrecedingUserMessage(t *testing.T) {
	env := marshalEnv(proto.CapabilityNode{
		ID:          "n_image_only",
		Kind:        proto.CapabilityImage,
		StreamReady: proto.StreamReadyYes,
		Image: &proto.ImageNode{
			SourceKind: proto.DataSourceInlineBase64,
			MediaType:  "image/png",
			Locator:    proto.DataLocator{Kind: proto.DataSourceInlineBase64, Value: "QUJD"},
		},
	})
	msgs := bodyMessages(t, marshalBody(t, env))
	if len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Fatalf("孤儿图片应新建 user 消息承载: %v", msgs)
	}
	images, ok := msgs[0]["images"].([]any)
	if !ok || len(images) != 1 || images[0] != "QUJD" {
		t.Fatalf("images=%v want [QUJD]", msgs[0]["images"])
	}
}

// TestMarshalNilPayloadGuards 抓的回归:nil payload 节点(text/tool_use/
// tool_result/image)必须走 loss 分支——守卫被删时本测试以 panic 红。
func TestMarshalNilPayloadGuards(t *testing.T) {
	env := marshalEnv(
		proto.CapabilityNode{ID: "nt", Kind: proto.CapabilityText, Text: nil},
		proto.CapabilityNode{ID: "nu", Kind: proto.CapabilityToolUse, ToolUse: nil},
		proto.CapabilityNode{ID: "nr", Kind: proto.CapabilityToolResult, ToolResult: nil},
		proto.CapabilityNode{ID: "ni", Kind: proto.CapabilityImage, Image: nil},
		textNode("n2", "user", "hi"),
	)
	msgs := bodyMessages(t, marshalBody(t, env))
	if len(msgs) != 1 || msgs[0]["content"] != "hi" {
		t.Fatalf("nil payload 节点不应进 messages: %v", msgs)
	}
	if len(env.CapabilityGraph.ProtocolLoss) != 4 {
		t.Fatalf("四个坏节点应各记一条 loss,got %d: %+v", len(env.CapabilityGraph.ProtocolLoss), env.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalResponseFormat 抓的回归:结构化输出投影丢失。真实管线里
// ResponseFormat 只会以 raw(openai 入站,Schema=客户端原始对象)或
// gemini_generation_config(gemini 入站)到达——这两种 dialect 必须解出来
// 投到 format,整体丢弃=客户端要 JSON 拿到散文(功能缩水)。
func TestMarshalResponseFormat(t *testing.T) {
	// 直接形(防御性保留)
	jsonObj := marshalEnv(textNode("n1", "user", "hi"))
	jsonObj.RequestControls.ResponseFormat = &proto.ResponseFormat{Type: "json_object"}
	if got := marshalBody(t, jsonObj)["format"]; got != "json" {
		t.Fatalf(`json_object format=%v want "json"`, got)
	}

	// openai 入站真实形:raw + {"type":"json_object"}
	rawJSONMode := marshalEnv(textNode("n1", "user", "hi"))
	rawJSONMode.RequestControls.ResponseFormat = &proto.ResponseFormat{Type: "raw", Schema: json.RawMessage(`{"type":"json_object"}`)}
	if got := marshalBody(t, rawJSONMode)["format"]; got != "json" {
		t.Fatalf(`raw json_object 应投影成 "json",got=%v(结构化输出丢失)`, got)
	}
	if len(rawJSONMode.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("可表达的 raw json_object 不应记 loss: %+v", rawJSONMode.CapabilityGraph.ProtocolLoss)
	}

	// openai 入站真实形:raw + json_schema(schema 体直填 format)
	rawSchema := marshalEnv(textNode("n1", "user", "hi"))
	rawSchema.RequestControls.ResponseFormat = &proto.ResponseFormat{
		Type:   "raw",
		Schema: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"Person","strict":true,"schema":{"type":"object","required":["x"]}}}`),
	}
	format, ok := marshalBody(t, rawSchema)["format"].(map[string]any)
	if !ok || format["type"] != "object" {
		t.Fatalf("raw json_schema 的 schema 体应直填 format: %v", format)
	}

	// Responses 形 {"format":{...}} 包装也要剥开
	rawWrapped := marshalEnv(textNode("n1", "user", "hi"))
	rawWrapped.RequestControls.ResponseFormat = &proto.ResponseFormat{
		Type:   "raw",
		Schema: json.RawMessage(`{"format":{"type":"json_object"}}`),
	}
	if got := marshalBody(t, rawWrapped)["format"]; got != "json" {
		t.Fatalf(`raw format 包装形应投影成 "json",got=%v`, got)
	}

	// gemini 入站真实形:responseSchema 直填;responseMimeType json → "json"
	gemSchema := marshalEnv(textNode("n1", "user", "hi"))
	gemSchema.RequestControls.ResponseFormat = &proto.ResponseFormat{
		Type:   "gemini_generation_config",
		Schema: json.RawMessage(`{"responseMimeType":"application/json","responseSchema":{"type":"object"}}`),
	}
	gFormat, ok := marshalBody(t, gemSchema)["format"].(map[string]any)
	if !ok || gFormat["type"] != "object" {
		t.Fatalf("gemini responseSchema 应直填 format: %v", gFormat)
	}
	gemMime := marshalEnv(textNode("n1", "user", "hi"))
	gemMime.RequestControls.ResponseFormat = &proto.ResponseFormat{
		Type:   "gemini_generation_config",
		Schema: json.RawMessage(`{"responseMimeType":"application/json"}`),
	}
	if got := marshalBody(t, gemMime)["format"]; got != "json" {
		t.Fatalf(`gemini responseMimeType=json 应投影 "json",got=%v`, got)
	}

	// 真不可表达:raw 里未知 type → 不写 format + 记 loss
	rawUnknown := marshalEnv(textNode("n1", "user", "hi"))
	rawUnknown.RequestControls.ResponseFormat = &proto.ResponseFormat{Type: "raw", Schema: json.RawMessage(`{"type":"grammar","grammar":"..."}`)}
	body := marshalBody(t, rawUnknown)
	if _, ok := body["format"]; ok {
		t.Fatalf("不可表达 dialect 不得写 format: %v", body["format"])
	}
	var rfLoss bool
	for _, loss := range rawUnknown.CapabilityGraph.ProtocolLoss {
		if loss.Field == "response_format" {
			rfLoss = true
		}
	}
	if !rfLoss {
		t.Fatalf("不可表达 response_format 被丢弃但未记 loss: %+v", rawUnknown.CapabilityGraph.ProtocolLoss)
	}
}

// TestMarshalUnsupportedControlsRecordLoss 抓的回归:tool_choice/
// parallel_tool_calls 无 Ollama 对应字段,被静默蒸发(无 loss)或被错误写进
// body。
func TestMarshalUnsupportedControlsRecordLoss(t *testing.T) {
	env := marshalEnv(textNode("n1", "user", "hi"))
	parallel := true
	env.RequestControls.ToolChoice = json.RawMessage(`"auto"`)
	env.RequestControls.ParallelToolCalls = &parallel

	body := marshalBody(t, env)
	for _, key := range []string{"tool_choice", "parallel_tool_calls"} {
		if _, ok := body[key]; ok {
			t.Errorf("body 不得含 %q 字段: %v", key, body)
		}
	}
	gotFields := map[string]bool{}
	for _, loss := range env.CapabilityGraph.ProtocolLoss {
		if loss.Code == "unsupported_request_control" {
			gotFields[loss.Field] = true
		}
	}
	for _, field := range []string{"tool_choice", "parallel_tool_calls"} {
		if !gotFields[field] {
			t.Errorf("control %q 被丢弃但未记 loss: %+v", field, env.CapabilityGraph.ProtocolLoss)
		}
	}
}

// TestMarshalModelFromUpstreamModel 抓的回归:registry 解析后的上游模型名
// 没写进 body.model(路由改写失效,请求打到入口模型名)。
func TestMarshalModelFromUpstreamModel(t *testing.T) {
	env := marshalEnv(textNode("n1", "user", "hi"))
	env.RequestMeta.UpstreamModel = "qwen2.5:14b"
	if got := marshalBody(t, env)["model"]; got != "qwen2.5:14b" {
		t.Fatalf("model=%v want qwen2.5:14b(UpstreamModel 优先)", got)
	}

	fallback := marshalEnv(textNode("n1", "user", "hi"))
	if got := marshalBody(t, fallback)["model"]; got != "llama3.2" {
		t.Fatalf("model=%v want llama3.2(回落入口模型名)", got)
	}
}

// TestMarshalUnsupportedCapabilityRecordsLoss 抓的回归:thinking 等无请求投影
// 的 capability 节点被静默丢弃。
func TestMarshalUnsupportedCapabilityRecordsLoss(t *testing.T) {
	env := marshalEnv(
		textNode("n1", "user", "hi"),
		proto.CapabilityNode{
			ID:          "n_think",
			Kind:        proto.CapabilityThinking,
			StreamReady: proto.StreamReadyNo,
			Thinking:    &proto.ThinkingNode{},
		},
	)
	marshalBody(t, env)
	if len(env.CapabilityGraph.ProtocolLoss) != 1 {
		t.Fatalf("thinking 节点应记恰一条 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
	if env.CapabilityGraph.ProtocolLoss[0].Code != "unsupported_capability" {
		t.Fatalf("loss code=%q want unsupported_capability", env.CapabilityGraph.ProtocolLoss[0].Code)
	}
}

// TestMarshalNilEnvelope 抓的回归:nil envelope 直接 panic 而非报错。
func TestMarshalNilEnvelope(t *testing.T) {
	if _, err := MarshalChatRequest(nil); err == nil {
		t.Fatal("nil envelope 必须报错")
	}
}
