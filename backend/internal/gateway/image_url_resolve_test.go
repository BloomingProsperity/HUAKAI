package gateway

import (
	"context"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func urlImageEnv(url string) *proto.HCSF {
	return &proto.HCSF{
		CapabilityGraph: proto.CapabilityGraph{
			Nodes: []proto.CapabilityNode{
				{Kind: proto.CapabilityImage, Image: &proto.ImageNode{
					SourceKind: proto.DataSourceURL,
					Locator:    proto.DataLocator{Kind: proto.DataSourceURL, Value: url},
				}},
			},
		},
	}
}

// TestResolveURLImages_Gemini抓取转base64 验证 gemini 族的 url 图被抓取转成 inline_base64。
// 变异证伪:去掉 n.Image.SourceKind==DataSourceURL 判定,或不改写 SourceKind → 节点仍是
// url → 转换断言变红。
func TestResolveURLImages_Gemini抓取转base64(t *testing.T) {
	env := urlImageEnv("https://x/y.png")
	stub := func(ctx context.Context, u string) (string, string, error) {
		if u != "https://x/y.png" {
			t.Fatalf("fetch 收到意外 url: %q", u)
		}
		return "image/png", "AQID", nil
	}
	resolveURLImagesForFamily(context.Background(), env, "gemini_messages", stub)
	got := env.CapabilityGraph.Nodes[0].Image
	if got.SourceKind != proto.DataSourceInlineBase64 {
		t.Fatalf("url 图应转 inline_base64,得 %q", got.SourceKind)
	}
	if got.MediaType != "image/png" || got.Locator.Value != "AQID" || got.Locator.Kind != proto.DataSourceInlineBase64 {
		t.Fatalf("转换后字段不对: %+v", got)
	}
}

// TestResolveURLImages_非base64族不动 验证接受 url 的族(openai_chat)不抓取 url 图。
func TestResolveURLImages_非base64族不动(t *testing.T) {
	env := urlImageEnv("https://x/y.png")
	called := false
	stub := func(ctx context.Context, u string) (string, string, error) {
		called = true
		return "image/png", "AQID", nil
	}
	resolveURLImagesForFamily(context.Background(), env, "openai_chat", stub)
	if called {
		t.Fatal("非 base64-only 族不应抓取 url 图")
	}
	if env.CapabilityGraph.Nodes[0].Image.SourceKind != proto.DataSourceURL {
		t.Fatal("非 base64-only 族的 url 图应保持原样")
	}
}

// TestResolveURLImages_抓取失败保持原样 验证 fetch 失败时节点不变(交给 marshal 记 loss)。
func TestResolveURLImages_抓取失败保持原样(t *testing.T) {
	env := urlImageEnv("https://x/y.png")
	stub := func(ctx context.Context, u string) (string, string, error) {
		return "", "", context.DeadlineExceeded
	}
	resolveURLImagesForFamily(context.Background(), env, "gemini_messages", stub)
	if env.CapabilityGraph.Nodes[0].Image.SourceKind != proto.DataSourceURL {
		t.Fatal("抓取失败时 url 图应保持原样")
	}
}

// TestResolveURLImages_已是base64不动 验证已是 inline_base64 的图不被 fetch。
func TestResolveURLImages_已是base64不动(t *testing.T) {
	env := &proto.HCSF{CapabilityGraph: proto.CapabilityGraph{Nodes: []proto.CapabilityNode{
		{Kind: proto.CapabilityImage, Image: &proto.ImageNode{
			SourceKind: proto.DataSourceInlineBase64,
			MediaType:  "image/png",
			Locator:    proto.DataLocator{Kind: proto.DataSourceInlineBase64, Value: "AQID"},
		}},
	}}}
	called := false
	stub := func(ctx context.Context, u string) (string, string, error) {
		called = true
		return "", "", nil
	}
	resolveURLImagesForFamily(context.Background(), env, "gemini_messages", stub)
	if called {
		t.Fatal("已是 base64 的图不应触发 fetch")
	}
}
