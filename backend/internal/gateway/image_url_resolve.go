package gateway

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// base64OnlyImageFamilies 是只接受内联 base64 图(不接受 url)的上游 marshal 族。
// 这些族的 url 来源 image 节点在 marshal 前需抓取转成 base64,否则会被丢弃。
var base64OnlyImageFamilies = map[string]struct{}{
	"gemini_messages": {},
}

// maxFetchedImageBytes 是抓取远程图的大小上限,防超大响应占内存。
const maxFetchedImageBytes = 20 << 20 // 20 MiB

// imageFetchClient 是抓取远程图用的 SSRF 守卫 http 客户端(挡环回/内网/link-local/
// metadata 目标 + DNS-rebind)。
var imageFetchClient = auth.NewSSRFProtectedOAuthClient(&http.Client{Timeout: 15 * time.Second})

// ImageFetcher 抓取一个 url 图,返回 (mediaType, base64Data)。
type ImageFetcher func(ctx context.Context, imageURL string) (mediaType, base64Data string, err error)

// resolveURLImagesForFamily 对只收内联 base64 的上游族,把 canonical graph 里 url 来源的
// image 节点抓取转成 inline_base64。抓取失败/超限的节点保持原样(由 marshal 侧记 loss)。
// 非 base64-only 族、fetch 为 nil、无 url 图时 no-op。原地修改 env。
func resolveURLImagesForFamily(ctx context.Context, env *proto.HCSF, endpointFamily string, fetch ImageFetcher) {
	if env == nil || fetch == nil {
		return
	}
	if _, ok := base64OnlyImageFamilies[hcsfProviderRequestModelFamily(endpointFamily)]; !ok {
		return
	}
	for i := range env.CapabilityGraph.Nodes {
		n := &env.CapabilityGraph.Nodes[i]
		if n.Kind != proto.CapabilityImage || n.Image == nil {
			continue
		}
		if n.Image.SourceKind != proto.DataSourceURL || n.Image.Locator.Value == "" {
			continue
		}
		mediaType, data, err := fetch(ctx, n.Image.Locator.Value)
		if err != nil || data == "" {
			continue
		}
		n.Image.SourceKind = proto.DataSourceInlineBase64
		n.Image.MediaType = mediaType
		n.Image.Locator = proto.DataLocator{Kind: proto.DataSourceInlineBase64, Value: data}
	}
}

// defaultImageFetcher 通过 SSRF 守卫客户端抓取 url 图,返回 (mediaType, base64Data)。
// mediaType 取自响应 Content-Type 主类型;缺失时用 application/octet-stream。
func defaultImageFetcher(ctx context.Context, imageURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := imageFetchClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("image fetch status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchedImageBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > maxFetchedImageBytes {
		return "", "", fmt.Errorf("fetched image exceeds %d bytes", maxFetchedImageBytes)
	}
	mediaType := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return mediaType, base64.StdEncoding.EncodeToString(data), nil
}
