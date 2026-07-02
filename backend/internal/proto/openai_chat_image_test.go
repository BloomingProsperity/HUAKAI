package proto

import (
	"strings"
	"testing"
)

// F4 视觉修复回归测试:OpenAI Chat 入口的 image_url part 必须建
// CapabilityImage 节点(此前只记 d5_image_pending loss 把图丢了,HCSF
// 默认开时上游收不到图)。

// testRedPixelPNGBase64 是 1x1 红色 PNG 的 base64,判别性 fixture:
// Locator 逐字节相等断言能抓"载荷被截断/换体"类回归。
const testRedPixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

// findImageNodes 收集 CapabilityGraph 里全部 image 节点。
func findImageNodes(env *HCSF) []CapabilityNode {
	var out []CapabilityNode
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityImage {
			out = append(out, n)
		}
	}
	return out
}

// TestOpenAIChatClient_ImageDataURIBuildsImageNode 断言 data URI 图片解析
// 全链:节点字段、text/image 顺序、block.Image 透传、无 pending loss。
// mutation 契约:解析侧还原成"只记 loss 不建节点"→ 本测试全红。
func TestOpenAIChatClient_ImageDataURIBuildsImageNode(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":[
			{"type":"text","text":"see this:"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,` + testRedPixelPNGBase64 + `"}}
		]}]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	// 图节点存在且字段逐一判别(mime 硬断言、base64 逐字节相等)
	imgs := findImageNodes(env)
	if len(imgs) != 1 {
		t.Fatalf("CapabilityImage nodes = %d, want 1", len(imgs))
	}
	img := imgs[0].Image
	if img == nil {
		t.Fatal("image node missing Image payload")
	}
	if img.SourceKind != DataSourceInlineBase64 {
		t.Errorf("SourceKind = %q, want inline_base64", img.SourceKind)
	}
	if img.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", img.MediaType)
	}
	if img.Locator.Kind != DataSourceInlineBase64 {
		t.Errorf("Locator.Kind = %q, want inline_base64", img.Locator.Kind)
	}
	if img.Locator.Value != testRedPixelPNGBase64 {
		t.Errorf("Locator.Value 与原 base64 不逐字节相等:\n got=%q\nwant=%q", img.Locator.Value, testRedPixelPNGBase64)
	}

	// text 节点仍在,消息 Content 顺序保留(text 在前 image 在后)
	if len(env.Messages) != 1 || len(env.Messages[0].Content) != 2 {
		t.Fatalf("messages/content 形状不对: %+v", env.Messages)
	}
	if env.Messages[0].Content[0].Type != "text" || env.Messages[0].Content[0].Text != "see this:" {
		t.Errorf("Content[0] 应为原 text block, got %+v", env.Messages[0].Content[0])
	}
	blk := env.Messages[0].Content[1]
	if blk.Type != "image" {
		t.Fatalf("Content[1].Type = %q, want image", blk.Type)
	}
	// block.Image 透传 image_url 原始 JSON(含完整 data URI)
	if !strings.Contains(string(blk.Image), "data:image/png;base64,"+testRedPixelPNGBase64) {
		t.Errorf("Content[1].Image 未保留原始 image_url JSON: %s", blk.Image)
	}

	// graph 节点顺序:text 节点先于 image 节点(交织顺序保留)
	var textSeen bool
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityText {
			textSeen = true
		}
		if n.Kind == CapabilityImage && !textSeen {
			t.Error("image 节点出现在 text 节点之前,交织顺序丢失")
		}
	}
	if !textSeen {
		t.Error("text 节点缺失")
	}

	// 不再有 d5_image_pending loss
	for _, l := range losses {
		if l.Code == "d5_image_pending" {
			t.Errorf("图已解析仍报 pending loss: %+v", l)
		}
	}
}

// TestOpenAIChatClient_ImageHTTPURLKind 断言非 data URI 的 url 走
// SourceKind=url 且原样透传。
func TestOpenAIChatClient_ImageHTTPURLKind(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[{"role":"user","content":[
			{"type":"image_url","image_url":{"url":"https://x/y.png"}}
		]}]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	imgs := findImageNodes(env)
	if len(imgs) != 1 {
		t.Fatalf("CapabilityImage nodes = %d, want 1", len(imgs))
	}
	img := imgs[0].Image
	if img.SourceKind != DataSourceURL {
		t.Errorf("SourceKind = %q, want url", img.SourceKind)
	}
	if img.Locator.Kind != DataSourceURL || img.Locator.Value != "https://x/y.png" {
		t.Errorf("Locator = %+v, want url/https://x/y.png", img.Locator)
	}
	for _, l := range losses {
		if l.Code == "d5_image_pending" {
			t.Errorf("URL 图已解析仍报 pending loss: %+v", l)
		}
	}
}

// TestOpenAIChatClient_ImageMalformedURLRecordsLoss 断言空 url / 畸形
// data URI 记 loss 不 panic 不 hard error,也不建图节点。
func TestOpenAIChatClient_ImageMalformedURLRecordsLoss(t *testing.T) {
	cases := []struct {
		name string
		part string
	}{
		{"空url", `{"type":"image_url","image_url":{"url":""}}`},
		{"缺image_url对象", `{"type":"image_url"}`},
		{"畸形dataURI缺base64段", `{"type":"image_url","image_url":{"url":"data:image/png"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &OpenAIChatClient{}
			body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":[` + tc.part + `]}]}`)
			env, losses, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
			if err != nil {
				t.Fatalf("畸形 image_url 不应 hard error: %v", err)
			}
			if got := len(findImageNodes(env)); got != 0 {
				t.Errorf("畸形 image_url 不应建图节点, got %d", got)
			}
			var found bool
			for _, l := range losses {
				if l.Code == "invalid_image_url" {
					found = true
					if l.Severity == "" {
						t.Errorf("loss 不能静默: %+v", l)
					}
				}
			}
			if !found {
				t.Errorf("畸形 image_url 应记 invalid_image_url loss, got %+v", losses)
			}
		})
	}
}

// TestOpenAIChatClient_ToolResultImageStaysDeferred 断言 role=tool 消息带图
// 仍走 deferred 策略:只拼 text、保留原 d5_image_pending loss、不建图节点
// (现状不回退,tool result 带图另开切片)。
func TestOpenAIChatClient_ToolResultImageStaysDeferred(t *testing.T) {
	adapter := &OpenAIChatClient{}
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":[
				{"type":"text","text":"result text"},
				{"type":"image_url","image_url":{"url":"https://x/y.png"}}
			]}
		]
	}`)
	env, losses, err := adapter.RequestToCanonical(newTestOpenAIChatCtx(t), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got := len(findImageNodes(env)); got != 0 {
		t.Errorf("tool result 带图不应建图节点(deferred), got %d", got)
	}
	var found bool
	for _, l := range losses {
		if l.Code == "d5_image_pending" {
			found = true
		}
	}
	if !found {
		t.Errorf("tool result 带图应保留 d5_image_pending loss, got %+v", losses)
	}
	// tool_result 内容仍只吃 text
	var toolResultSeen bool
	for _, n := range env.CapabilityGraph.Nodes {
		if n.Kind == CapabilityToolResult && n.ToolResult != nil {
			toolResultSeen = true
			if len(n.ToolResult.Content) != 1 || n.ToolResult.Content[0].Text != "result text" {
				t.Errorf("tool_result 内容应只含 text, got %+v", n.ToolResult.Content)
			}
		}
	}
	if !toolResultSeen {
		t.Error("tool_result 节点缺失")
	}
}
