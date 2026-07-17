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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/bindingfallback"
	fallbackexec "github.com/BloomingProsperity/HUAKAI/internal/bindingfallback/executor"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
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
		// abort 给用户退款之前 best-effort 取消上游 prediction:Prefer: wait 超窗
		// (starting/processing)时上游仍在跑、按产出向平台计费,不取消=平台单边
		// 吃成本,客户端重试每轮再开新 prediction 叠加。prediction id + cancel
		// 结局进 abort 的 protocol_loss 审计,供事后对账上游账单。
		meta := replicate.PredictionMetaFromResponse(raw)
		outcome := ex.bestEffortCancelReplicatePrediction(meta)
		ex.abortWithLoss(w, "replicate_prediction_failed", 0, replicateAbortLoss(meta, outcome))
		writeJSONError(w, http.StatusBadGateway, clienterr.CodeUpstreamDispatchError, clienterr.MessageFor(clienterr.CodeUpstreamDispatchError))
		return nil, false
	}
	// 记录实际交付张数供 per_image settle 对账:Replicate 的 num_outputs 是
	// model-specific,不接受该参数的模型会静默只回 1 张;按请求数计费=多收。
	ex.deliveredImageCount = countDeliveredImages(translated)
	return translated, true
}

func (ex *execution) abortHTTPFailure(w http.ResponseWriter, reason string, raw []byte) (error, bool) {
	if ex.resolved.ProtocolFamily != replicateImageFamily {
		return ex.abortWithError(w, reason, 0), true
	}
	meta := replicate.PredictionMetaFromResponse(raw)
	outcome := ex.bestEffortCancelReplicatePrediction(meta)
	abortErr := ex.abortWithLossError(w, reason, 0, replicateAbortLoss(meta, outcome))
	retrySafe := meta.ID == "" || outcome == "cancel_issued"
	return abortErr, retrySafe
}

// familyRetrySafe 判定 family 专属的换号重试副作用安全性。Replicate prediction
// 一经提交即按产出计费:只有「未建 prediction 或已确认取消」且失败类属限流/授权
// (换号才有意义)时才许重试;5xx 多为提交后半途失败,换号重试=第二个号再建
// 付费任务(平台重复扣费),一律终态。其余 family 无上游侧付费副作用,放行。
func (ex *execution) familyRetrySafe(failure *fallbackexec.Failure, sideEffectRetrySafe bool) bool {
	if ex.resolved.ProtocolFamily != replicateImageFamily {
		return true
	}
	if failure == nil || !sideEffectRetrySafe {
		return false
	}
	if failure.AuthFailoverEligible {
		return true
	}
	return failure.Signal == bindingfallback.SignalUpstreamRateLimit
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

// replicateCancelTimeout 是单次 cancel POST 的上界。cancel 串行在 abort 退款
// 之前(保住「结局进审计」的完整链),必须收紧:Prefer: wait 最长 60s + 本上界
// + abort 5s 须远离 claim 租约 90s,否则 lease sweeper 抢先 abort 会丢整条
// prediction 审计链(评审 S3 竞态)。
const replicateCancelTimeout = 3 * time.Second

// defaultReplicateCancelClient 是 Deps.ReplicateCancelClient 未注入时的默认
// 控制面 client,与 transport factory standard 路径同口径:clone DefaultTransport
// 且显式 Proxy=nil(HUAKAI 唯一代理决策点是 dispatcher.applyProxy,cancel 不得
// 被 HTTP(S)_PROXY env 截胡),再包 dial 时刻 passthrough IP 守卫——cancel 与
// 主出站同享 fail-closed,不得成为绕过 SSRF 守卫的旁路。
var defaultReplicateCancelClient = newDefaultReplicateCancelClient()

func newDefaultReplicateCancelClient() cancelHTTPDoer {
	rt := http.RoundTripper(http.DefaultTransport)
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		cloned := base.Clone()
		cloned.Proxy = nil
		rt = cloned
	}
	if wrapped, err := provider.WrapPassthroughEndpointTransport(rt); err == nil {
		rt = wrapped
	}
	return &http.Client{Timeout: replicateCancelTimeout, Transport: rt}
}

// bestEffortCancelReplicatePrediction 取消上游未终态的 prediction。任何失败只
// 进审计 outcome 字符串,绝不向调用方返回错误——cancel 失败不得阻断 abort 退款
// 主路径。context 脱离请求取消(客户端断连正是最需要 cancel 的时刻)。
// 已知残留(台账):绑定出站代理的账号 cancel 走网关直连而非账号代理,可能被
// 上游拒——cancel 经 per-account 代理出口是 follow-up 切片。
func (ex *execution) bestEffortCancelReplicatePrediction(meta replicate.PredictionMeta) string {
	if meta.ID == "" {
		return "skipped_no_prediction_id"
	}
	if !replicate.CancelWorthwhile(meta.Status) {
		return "skipped_terminal_status"
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ex.ctx), replicateCancelTimeout)
	defer cancel()
	req, err := replicate.NewCancelRequest(cctx, ex.cred, meta.ID)
	if err != nil {
		return "cancel_build_failed: " + err.Error()
	}
	// 与主出站同口径的运行时 SSRF 守卫:租户自填 base_url 的 host 静态检查能过,
	// 但 DNS 可解析到内网/metadata(rebinding)。主路径在 dispatcher 里做这一步,
	// cancel 自己发请求就必须自己做,否则 cancel 成为守卫旁路(评审 S1)。
	if provider.UsesCustomPassthroughEndpoint(ex.cred) {
		if err := provider.ValidatePassthroughEndpointTarget(cctx, req.URL); err != nil {
			return "cancel_blocked_unsafe_endpoint: " + err.Error()
		}
	}
	client := ex.d.ReplicateCancelClient
	if client == nil {
		client = defaultReplicateCancelClient
	}
	res, err := client.Do(req)
	if err != nil {
		return "cancel_send_failed: " + err.Error()
	}
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Sprintf("cancel_rejected_status_%d", res.StatusCode)
	}
	return "cancel_issued"
}

// replicateAbortLoss 把 prediction id/status/cancel 结局编成 v0.4 protocol_loss
// 审计条目(abort 落 usage_records.protocol_loss):机器对账读 Code+Details,
// 人读 Reason;Severity=info 不与翻译损耗(lossy verdict)口径混淆。编码失败
// 返回 nil,不阻断 abort。
func replicateAbortLoss(meta replicate.PredictionMeta, cancelOutcome string) json.RawMessage {
	entry := proto.ProtocolLossEntry{
		Vendor:   "replicate",
		Severity: proto.ProtocolLossInfo,
		Code:     "replicate_prediction_cancel",
		Reason:   "prediction aborted before delivery; upstream task cancellation attempted best-effort",
		Details: map[string]string{
			"prediction_id":  meta.ID,
			"status":         meta.Status,
			"cancel_outcome": cancelOutcome,
		},
	}
	raw, err := json.Marshal([]proto.ProtocolLossEntry{entry})
	if err != nil {
		return nil
	}
	return raw
}

// pricingVendorForFamily 给 providerForPricing 提供 family 级兜底计价
// provider:replicate_image 在选号前(accInfo.Platform 未知)也能命中
// rate table 的 providers.replicate 节点。pool.VendorFromProtocolFamily
// 现已全量覆盖注册族(同值 "replicate",注册表驱动守卫锁死);本 shim 保留
// 作 lane 内冗余防线,两处必须同值。
func pricingVendorForFamily(protocolFamily string) string {
	if protocolFamily == replicateImageFamily {
		return "replicate"
	}
	return ""
}
