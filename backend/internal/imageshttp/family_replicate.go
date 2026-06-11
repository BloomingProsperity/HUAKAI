// family_replicate.go — replicate_image protocol family 在图片 lane 的全部
// 专属逻辑(单一职责小文件):请求侧 pre-reserve 校验门 + 响应侧翻译钩子 +
// 计价 provider 映射。其余 family(openai 图片直通)不受影响。
//
// 路径事实:本 lane 的 Dispatch 走出站 AdapterRegistry.For(ProtocolFamily),
// 不经过 chat lane 的入站 protocol_selector / stream_scanner /
// MarshalToProviderRequest;replicate_image 因此只注册出站
// (registrydefault),chat lane 误绑定时 marshal fail-closed(守卫:
// gateway.TestMarshalReplicateImageFamilyFailsClosedOnChatLane)。
package imageshttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/replicate"
)

// replicateImageFamily 与 registrydefault.ProtocolReplicateImage 同值;
// lane 侧按 registry 解析出的 family 字符串分流(repo 惯例同
// pool.VendorFromProtocolFamily 的字面量 switch)。一致性由
// TestReplicateImagesHandler_FamilyConstantMatchesRegistry 钉住。
const replicateImageFamily = "replicate_image"

// validateFamilyConstraints 在 prepareRoute 之后(family 已知)、reserve 之前
// (零成本拒绝,不开 claim)执行 family 专属校验。对齐 stream:true 的
// 入口显式拒绝先例(request.go)。
func (ex *execution) validateFamilyConstraints(w http.ResponseWriter) bool {
	if ex.resolved.ProtocolFamily != replicateImageFamily {
		return true
	}
	if ex.endpoint != imageEndpointGenerations {
		// edits/variations 需要 multipart 文件上传子请求(adapter 契约禁止
		// adapter 内发子请求),v1 范围外,roadmap 项;静默转发只会拿上游 404
		// 还烧掉 reserve/abort 一轮。
		writeJSONError(w, http.StatusBadRequest, "endpoint_not_supported_for_model",
			"replicate models support /v1/images/generations only (edits/variations on roadmap)")
		return false
	}
	if strings.EqualFold(strings.TrimSpace(ex.req.ResponseFormat), "b64_json") {
		// Replicate 输出是文件 URL;b64 需出站后下载子请求,v1 范围外。
		// 显式 400,不静默降级成 url(客户端会按 b64 解析失败)。
		writeJSONError(w, http.StatusBadRequest, "response_format_not_supported",
			"replicate models return url output only; response_format b64_json is not supported")
		return false
	}
	return true
}

// translateUpstreamResponseForFamily 在上游 2xx body 读回后、写客户端/settle
// 之前执行 family 专属响应翻译。replicate_image:prediction JSON → OpenAI
// images 形;翻译失败(status 非 succeeded / error 非空 / output 为空)按
// 上游错误处理——abort 退预留、绝不 settle 计费(误计费守卫)。
func (ex *execution) translateUpstreamResponseForFamily(w http.ResponseWriter, raw []byte) ([]byte, bool) {
	if ex.resolved.ProtocolFamily != replicateImageFamily {
		return raw, true
	}
	translated, err := replicate.TranslateImageResponse(raw, time.Now)
	if err != nil {
		ex.abort(w, "replicate_prediction_failed", 0)
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return nil, false
	}
	// 记录实际交付张数供 per_image settle 对账:Replicate 的 num_outputs 是
	// model-specific,不接受该参数的模型会静默只回 1 张;按请求数计费=多收。
	ex.deliveredImageCount = countDeliveredImages(translated)
	return translated, true
}

// countDeliveredImages 数翻译后 OpenAI images 响应的 data 条数。解析失败
// 返回 0(回退按请求 amount 计费,保守不少收)。
func countDeliveredImages(translated []byte) int {
	var resp struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(translated, &resp) != nil {
		return 0
	}
	return len(resp.Data)
}

// pricingVendorForFamily 给 providerForPricing 提供 family 级兜底计价
// provider:replicate_image 在选号前(accInfo.Platform 未知)也能命中
// rate table 的 providers.replicate 节点。pool.VendorFromProtocolFamily
// 的映射范围被 4-vendor 真账号 metric 切片测试锁死,不在那里扩。
func pricingVendorForFamily(protocolFamily string) string {
	if protocolFamily == replicateImageFamily {
		return "replicate"
	}
	return ""
}
