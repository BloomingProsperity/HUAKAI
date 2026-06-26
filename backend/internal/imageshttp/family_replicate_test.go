package imageshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/replicate"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

const replicateTestModel = "black-forest-labs/flux-1.1-pro"

type replicateRegistryStub struct{}

func (replicateRegistryStub) ResolveModel(_ context.Context, model string, _ int64) (registry.Resolved, error) {
	return registry.Resolved{
		PublicAlias:      model,
		CanonicalModelID: "image/" + model,
		ProviderModelID:  replicateTestModel,
		ProtocolFamily:   registrydefault.ProtocolReplicateImage,
		Capabilities:     []string{"image_output"},
		PoolCandidates:   []int64{101},
		SnapshotVersion:  "registry:7:1",
	}, nil
}

type replicateVaultStub struct{}

func (replicateVaultStub) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "r8_test"}, provider.AccountInfo{
		AccountID: 44,
		TenantID:  7,
		Platform:  "replicate",
	}, nil
}

// platformImageVaultStub 让测试覆盖解析账号的 Platform(默认 replicateVaultStub
// 恒返 replicate)。
type platformImageVaultStub struct{ platform string }

func (s platformImageVaultStub) Resolve(context.Context, int64, int64) (provider.Credential, provider.AccountInfo, error) {
	return provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "r8_test"}, provider.AccountInfo{
		AccountID: 44,
		TenantID:  7,
		Platform:  s.platform,
	}, nil
}

// TestReplicateImagesHandler_SettleRepricesByResolvedAccountPlatform F3:per_image
// 正常交付路径(交付数==请求数)必须按解析账号平台重定价。预扣用协议族 vendor
// (replicate_image→replicate,0.04/张);解析账号平台是 gemini(0.08/张,价不同)。
// reserve 估 replicate 价,settle 必须重算成 gemini 价。
// 变异:正常路径沿用 predictedCost(去掉 perImageCost 重算)→ settle ActualCost
// 回到 reserve 的 0.04 → 本断言红。判别关键:gemini 价≠replicate 价,且账号平台≠
// 协议族 vendor(gemini 是合法 transport provider code)。
func TestReplicateImagesHandler_SettleRepricesByResolvedAccountPlatform(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"status":"succeeded","output":["https://r.test/out.webp"]}`,
	})
	env.deps.CredentialVault = platformImageVaultStub{platform: "gemini"}
	env.rateTable.raw = json.RawMessage(`{
		"providers": {
			"replicate": {"models": {"black-forest-labs/flux-1.1-pro": {"pricing_scheme": "per_image", "image_base_micro_usd": "40000", "image_size_multipliers": {"1024x1024": "1"}, "image_quality_multipliers": {"standard": "1"}, "image_amount_range": {"min": 1, "max": 4}}}},
			"gemini":    {"models": {"black-forest-labs/flux-1.1-pro": {"pricing_scheme": "per_image", "image_base_micro_usd": "80000", "image_size_multipliers": {"1024x1024": "1"}, "image_quality_multipliers": {"standard": "1"}, "image_amount_range": {"min": 1, "max": 4}}}}
		}
	}`)

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	// reserve:1 张 × replicate 0.04(协议族 vendor 估,选号前)。
	if got := env.claims.reserves[0].req.PredictedCost.String(); got != "0.04" {
		t.Fatalf("reserve PredictedCost=%s want 0.04(协议族 vendor replicate 估)", got)
	}
	// settle:1 张 × gemini 0.08(解析账号平台重算)。
	if got := env.settler.settles[0].ActualCost.String(); got != "0.08" {
		t.Fatalf("settle ActualCost=%s want 0.08(账号平台 gemini 重定价,非沿用预估 0.04)", got)
	}
}

// newReplicateImagesTestEnv 复用 openai 夹具(claims/settler/transport/
// router/selector),换 registry(family=replicate_image)、vault(platform=
// replicate)、出站 adapter 与 providers.replicate 计价表。Dispatcher 走真
// transport.Factory.For("replicate", standard)——transport/policy.go 删
// "replicate" 条目时本环境的端到端用例整组转红。
func newReplicateImagesTestEnv(t *testing.T, endpoint imageEndpoint, resp upstreamResponse) *imagesTestEnv {
	t.Helper()
	env := newImagesTestEnv(t, endpoint, resp)
	env.deps.Registry = replicateRegistryStub{}
	env.deps.CredentialVault = replicateVaultStub{}
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister(registrydefault.ProtocolReplicateImage, &replicate.Adapter{})
	tf := transport.NewFactory()
	tf.SetStandard(env.transport)
	env.deps.Dispatcher = &gateway.UpstreamDispatcher{Adapters: adapters, TransportFactory: tf}
	env.rateTable.raw = replicateRateTableFixture()
	return env
}

func replicateRateTableFixture() json.RawMessage {
	return json.RawMessage(`{
		"providers": {
			"replicate": {
				"models": {
					"black-forest-labs/flux-1.1-pro": {
						"pricing_scheme": "per_image",
						"image_base_micro_usd": "40000",
						"image_size_multipliers": {"1024x1024": "1", "1792x1024": "1.5"},
						"image_quality_multipliers": {"standard": "1", "hd": "1.25"},
						"image_amount_range": {"min": 1, "max": 4},
						"image_prompt_max_chars": 4000
					}
				}
			}
		}
	}`)
}

// TestReplicateImagesHandler_FamilyConstantMatchesRegistry 钉住 lane 侧字面量
// 与 registrydefault 常量一致——漂移=本文件所有分流逻辑对真实 family 失效
// (replicate 请求静默走 openai 直通路径)。
func TestReplicateImagesHandler_FamilyConstantMatchesRegistry(t *testing.T) {
	if replicateImageFamily != registrydefault.ProtocolReplicateImage {
		t.Fatalf("lane 常量 %q != registrydefault.ProtocolReplicateImage %q", replicateImageFamily, registrydefault.ProtocolReplicateImage)
	}
}

// TestReplicateImagesHandler_EndToEndSucceededPrediction 端到端正路:客户端
// OpenAI 形请求 → 出站打到 models/{model}/predictions(model 含 "/" 保留为
// 路径段)+ Prefer: wait + {"input":{...}} body;上游 succeeded → 客户端拿
// OpenAI 形 data[].url(翻译产物,非 prediction 原样转发),settle 恰一次。
func TestReplicateImagesHandler_EndToEndSucceededPrediction(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"status":"succeeded","output":["https://r.test/out.webp"],"error":null}`,
	})

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"a tiny lighthouse","size":"1024x1024","n":2}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	env.assertNoHangingClaims(t)
	if got := env.transport.path; got != "/v1/models/black-forest-labs/flux-1.1-pro/predictions" {
		t.Fatalf("upstream path=%q want model 进 path 的 predictions 端点", got)
	}
	if got := env.transport.header.Get("Prefer"); got != "wait=60" {
		t.Fatalf("Prefer=%q want wait=60(计费正确性承重墙)", got)
	}
	var outBody struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal([]byte(env.transport.body), &outBody); err != nil || outBody.Input == nil {
		t.Fatalf("出站 body 不是 {\"input\":{...}} 形: %s", env.transport.body)
	}
	if outBody.Input["prompt"] != "a tiny lighthouse" || outBody.Input["num_outputs"] != float64(2) || outBody.Input["aspect_ratio"] != "1:1" {
		t.Fatalf("出站 input 翻译错: %v", outBody.Input)
	}
	var clientResp struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &clientResp); err != nil {
		t.Fatalf("客户端响应不是 JSON: %v", err)
	}
	if len(clientResp.Data) != 1 || clientResp.Data[0].URL != "https://r.test/out.webp" || clientResp.Created == 0 {
		t.Fatalf("客户端响应=%s want OpenAI 形 data[].url + created", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"status"`) || strings.Contains(rec.Body.String(), `"output"`) {
		t.Fatalf("prediction 原始字段泄漏到客户端(翻译钩子被绕过): %s", rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
}

// TestReplicateImagesHandler_NonSucceededPredictionAbortsWithoutSettle
// 误计费守卫:上游 200 但 status=starting(Prefer: wait 超窗)/failed 时,
// 必须 abort 退预留、settle 0、客户端 502。变异:去掉 attempt.go 的
// 翻译钩子(原样转发 prediction)→ 200 + settle 1 → 本测试红。
func TestReplicateImagesHandler_NonSucceededPredictionAbortsWithoutSettle(t *testing.T) {
	for name, body := range map[string]string{
		"starting": `{"status":"starting","output":null,"error":null}`,
		"failed":   `{"status":"failed","output":null,"error":"NSFW content"}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
				status: http.StatusOK,
				body:   body,
			})

			rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024"}`)

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
			}
			env.assertNoHangingClaims(t)
			if got := len(env.settler.settles); got != 0 {
				t.Fatalf("settle calls=%d want 0(未完成/失败的 prediction 不得计费)", got)
			}
			if got := len(env.settler.aborts); got != 1 {
				t.Fatalf("abort calls=%d want 1", got)
			}
			if env.settler.aborts[0].reason != "replicate_prediction_failed" {
				t.Fatalf("abort reason=%q want replicate_prediction_failed", env.settler.aborts[0].reason)
			}
		})
	}
}

// TestReplicateImagesHandler_B64JSONRejectedPreReserve 抓的回归:b64_json
// 滑过入口门走到 dispatch(BuildRequest 失败 → 502 + reserve/abort 一轮,
// 错误码和成本都不对;入口应零成本 400)。
func TestReplicateImagesHandler_B64JSONRejectedPreReserve(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"status":"succeeded","output":"https://r.test/x.png"}`,
	})

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024","response_format":"b64_json"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "response_format_not_supported") {
		t.Fatalf("body=%s want response_format_not_supported", rec.Body.String())
	}
	if got := len(env.claims.reserves); got != 0 {
		t.Fatalf("reserve calls=%d want 0(pre-reserve 拒绝)", got)
	}
	if env.transport.called {
		t.Fatal("b64_json 请求不应出站")
	}
}

// TestReplicateImagesHandler_EditsVariationsRejectedPreReserve 抓的回归:
// edits/variations 被转发到 Replicate(端点不存在,multipart 上传 v1 范围外,
// roadmap)。openai family 同端点不受影响由既有 handler_test 用例钉住。
func TestReplicateImagesHandler_EditsVariationsRejectedPreReserve(t *testing.T) {
	for _, endpoint := range []imageEndpoint{imageEndpointEdits, imageEndpointVariations} {
		env := newReplicateImagesTestEnv(t, endpoint, upstreamResponse{status: http.StatusOK, body: `{}`})

		body := `{"model":"flux-pro","prompt":"x","size":"1024x1024","image_url":"https://img.test/s.png"}`
		if endpoint == imageEndpointVariations {
			body = `{"model":"flux-pro","size":"1024x1024","image_url":"https://img.test/s.png"}`
		}
		rec := env.invoke(t, body)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("endpoint=%s status=%d body=%s want 400", endpoint, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "endpoint_not_supported_for_model") {
			t.Fatalf("body=%s want endpoint_not_supported_for_model", rec.Body.String())
		}
		if got := len(env.claims.reserves); got != 0 {
			t.Fatalf("reserve calls=%d want 0", got)
		}
		if env.transport.called {
			t.Fatal("replicate edits/variations 不应出站")
		}
	}
}

// TestReplicateImagesHandler_PricingResolvesProvidersReplicateNode 抓的回归:
// providerForPricing 对 replicate_image 在选号前解析不出 "replicate"
// (pool.VendorFromProtocolFamily 返空 → providers.replicate 计价节点永远
// 找不到 → 全部请求 503 pricing_unavailable)。变异:删
// pricingVendorForFamily 映射 → 本测试红。
func TestReplicateImagesHandler_PricingResolvesProvidersReplicateNode(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"status":"succeeded","output":"https://r.test/a.png"}`,
	})

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200(providers.replicate 计价节点应可达)", rec.Code, rec.Body.String())
	}
	want := "0.04"
	if got := env.settler.settles[0].ActualCost.String(); got != want {
		t.Fatalf("ActualCost=%s want %s(image_base_micro_usd=40000)", got, want)
	}
}

// TestReplicateImagesHandler_SettlesByDeliveredImageCount 抓的回归(S1 多收):
// 请求 n=2 但上游 succeeded 只回 1 张(Replicate model-specific num_outputs
// 被忽略/部分输出被过滤),settle 必须按交付 1 张(0.04)计费、ImageCount=1,
// 绝不按请求 2 张(0.08)多收用户钱。
// 变异:删 billableImageCount 的交付数对账分支(总返回 amount)→
// ActualCost 变 0.08 + ImageCount 变 2,本测试红。
func TestReplicateImagesHandler_SettlesByDeliveredImageCount(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"status":"succeeded","output":["https://r.test/only-one.webp"]}`,
	})

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024","n":2}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 1 {
		t.Fatalf("settle calls=%d want 1", got)
	}
	if got := env.settler.settles[0].ActualCost.String(); got != "0.04" {
		t.Fatalf("ActualCost=%s want 0.04(按交付 1 张,非请求 2 张 0.08——多收守卫)", got)
	}
	if got := env.settler.settles[0].Draft.ImageCount; got != 1 {
		t.Fatalf("Draft.ImageCount=%d want 1(交付数,非请求数 2)", got)
	}
}

// recordingCancelDoer 记录 best-effort cancel 请求的控制面 client 夹具。
// 纪律:任何「带 prediction id 且非终态」的 fixture 必须注入本 doer——否则默认
// client 会在单测里真发 api.replicate.com。
// 与真实 http.Client 同口径:context 已取消则拒发(detached-context 判别用)。
type recordingCancelDoer struct {
	requests []*http.Request
	status   int
	err      error
}

func (d *recordingCancelDoer) Do(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	d.requests = append(d.requests, req)
	if d.err != nil {
		return nil, d.err
	}
	status := d.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Body: http.NoBody}, nil
}

// TestBestEffortCancelSurvivesCanceledRequestContext detached-context 守卫:
// 客户端断连(请求 context 已取消)正是最需要 cancel 的时刻,cancel 必须在
// 脱离请求取消的 context 上发出。
// 变异:bestEffortCancel 的 context 直接从 ex.ctx 派生(去掉 WithoutCancel)
// → doer 按真实 client 口径拒发 → outcome 变 cancel_send_failed → 变红。
func TestBestEffortCancelSurvivesCanceledRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	doer := &recordingCancelDoer{}
	ex := &execution{
		ctx:  ctx,
		cred: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "r8"},
		d:    Deps{ReplicateCancelClient: doer},
	}

	outcome := ex.bestEffortCancelReplicatePrediction(replicate.PredictionMeta{ID: "pred-ctx", Status: "processing"})

	if outcome != "cancel_issued" {
		t.Fatalf("outcome=%q want cancel_issued(断连后仍须取消上游任务)", outcome)
	}
	if got := len(doer.requests); got != 1 {
		t.Fatalf("cancel requests=%d want 1", got)
	}
}

// TestBestEffortCancelBlocksRebindingPassthroughEndpoint 运行时 SSRF 守卫
// (评审 S1):租户自填 base_url 的 host 静态检查能过,但 DNS 解析到内网/
// metadata(rebinding)时,cancel 发送前必须 fail-closed——cancel 不得成为
// 主出站守卫的旁路。
// 变异:删 bestEffortCancel 里的 ValidatePassthroughEndpointTarget 调用 →
// doer 收到请求 + outcome=cancel_issued → 两断言变红。
func TestBestEffortCancelBlocksRebindingPassthroughEndpoint(t *testing.T) {
	restore := provider.SwapPassthroughEndpointLookupForTesting(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
	})
	defer restore()
	doer := &recordingCancelDoer{}
	ex := &execution{
		ctx: context.Background(),
		cred: provider.Credential{
			Type:  provider.CredentialTypeUpstreamPassthrough,
			Value: "Token x",
			Extra: map[string]string{"base_url": "https://relay.rebind.example"},
		},
		d: Deps{ReplicateCancelClient: doer},
	}

	outcome := ex.bestEffortCancelReplicatePrediction(replicate.PredictionMeta{ID: "pred-ssrf", Status: "processing"})

	if !strings.HasPrefix(outcome, "cancel_blocked_unsafe_endpoint") {
		t.Fatalf("outcome=%q want cancel_blocked_unsafe_endpoint 前缀(rebinding 必须拦截)", outcome)
	}
	if got := len(doer.requests); got != 0 {
		t.Fatalf("cancel requests=%d want 0(被守卫拦截不得出站)", got)
	}
}

// TestBestEffortCancelRecordsRejectedStatus 非 2xx cancel 响应进审计结局
// (评审遗留 X2:此前 cancel_rejected_status_* 是测试死路径)。
// 变异:删 status 检查恒返 cancel_issued → 变红。
func TestBestEffortCancelRecordsRejectedStatus(t *testing.T) {
	doer := &recordingCancelDoer{status: 422}
	ex := &execution{
		ctx:  context.Background(),
		cred: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "r8"},
		d:    Deps{ReplicateCancelClient: doer},
	}

	outcome := ex.bestEffortCancelReplicatePrediction(replicate.PredictionMeta{ID: "pred-422", Status: "processing"})

	if outcome != "cancel_rejected_status_422" {
		t.Fatalf("outcome=%q want cancel_rejected_status_422", outcome)
	}
}

// TestReplicateImagesHandler_WaitOverrunCancelsPrediction 钱路守卫:Prefer: wait
// 超窗(status=processing 且带 prediction id)时,abort 退款之外必须向上游发
// POST predictions/{id}/cancel(鉴权同出站口径),且 id+结局进 abort 审计。
// 变异:删 translateUpstreamResponseForFamily 的 cancel 调用 → doer 0 请求
// → 本测试红(上游任务继续烧钱,平台单边吃成本)。
func TestReplicateImagesHandler_WaitOverrunCancelsPrediction(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"id":"pred-42","status":"processing","output":null,"error":null}`,
	})
	doer := &recordingCancelDoer{}
	env.deps.ReplicateCancelClient = doer

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.settles); got != 0 {
		t.Fatalf("settle calls=%d want 0", got)
	}
	if got := len(doer.requests); got != 1 {
		t.Fatalf("cancel requests=%d want 1(超窗 prediction 必须取消)", got)
	}
	cancelReq := doer.requests[0]
	if cancelReq.Method != http.MethodPost {
		t.Fatalf("cancel method=%s want POST", cancelReq.Method)
	}
	if got := cancelReq.URL.String(); got != "https://api.replicate.com/v1/predictions/pred-42/cancel" {
		t.Fatalf("cancel url=%q want predictions/pred-42/cancel", got)
	}
	if got := cancelReq.Header.Get("Authorization"); got != "Bearer r8_test" {
		t.Fatalf("cancel auth=%q want 与出站同口径 Bearer", got)
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1", got)
	}
	loss := string(env.settler.aborts[0].protocolLoss)
	if !strings.Contains(loss, "pred-42") || !strings.Contains(loss, "cancel_issued") {
		t.Fatalf("abort protocolLoss=%s want prediction id + cancel_issued 审计", loss)
	}
}

// TestReplicateImagesHandler_CancelFailureDoesNotBlockAbort best-effort 语义:
// cancel 发送失败时 abort 退款主路径照常走完(退预留、502、无 abort-failed 头),
// 失败结局只进审计。变异:cancel 错误传染 abort(提前 return / 改 reason)
// → 本测试红。
func TestReplicateImagesHandler_CancelFailureDoesNotBlockAbort(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"id":"pred-77","status":"starting","output":null,"error":null}`,
	})
	doer := &recordingCancelDoer{err: errors.New("upstream cancel unreachable")}
	env.deps.ReplicateCancelClient = doer

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if got := len(env.settler.aborts); got != 1 {
		t.Fatalf("abort calls=%d want 1(cancel 失败不得阻断 abort)", got)
	}
	if env.settler.aborts[0].reason != "replicate_prediction_failed" {
		t.Fatalf("abort reason=%q want replicate_prediction_failed", env.settler.aborts[0].reason)
	}
	if got := rec.Header().Get("X-Huakai-Abort-Failed"); got != "" {
		t.Fatalf("X-Huakai-Abort-Failed=%q want empty(abort 本身成功)", got)
	}
	loss := string(env.settler.aborts[0].protocolLoss)
	if !strings.Contains(loss, "pred-77") || !strings.Contains(loss, "cancel_send_failed") {
		t.Fatalf("abort protocolLoss=%s want prediction id + cancel_send_failed 审计", loss)
	}
}

// TestReplicateImagesHandler_TerminalPredictionNotCanceled 终态(failed)不发
// cancel(徒增上游调用),但 prediction id 仍进 abort 审计。
// 变异:去掉 CancelWorthwhile 状态门 → doer 收到请求 → 本测试红。
func TestReplicateImagesHandler_TerminalPredictionNotCanceled(t *testing.T) {
	env := newReplicateImagesTestEnv(t, imageEndpointGenerations, upstreamResponse{
		status: http.StatusOK,
		body:   `{"id":"pred-99","status":"failed","output":null,"error":"NSFW content"}`,
	})
	doer := &recordingCancelDoer{}
	env.deps.ReplicateCancelClient = doer

	rec := env.invoke(t, `{"model":"flux-pro","prompt":"x","size":"1024x1024"}`)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s want 502", rec.Code, rec.Body.String())
	}
	if got := len(doer.requests); got != 0 {
		t.Fatalf("cancel requests=%d want 0(终态 prediction 不取消)", got)
	}
	loss := string(env.settler.aborts[0].protocolLoss)
	if !strings.Contains(loss, "pred-99") || !strings.Contains(loss, "skipped_terminal_status") {
		t.Fatalf("abort protocolLoss=%s want prediction id + skipped_terminal_status 审计", loss)
	}
}

// 静态断言:夹具 stub 满足接口(防 handler_test 夹具演化后本文件悄悄漂移)。
var (
	_                 = auth.Identity{}
	_ billing.Settler = (*recordingSettler)(nil)
	_ cancelHTTPDoer  = (*recordingCancelDoer)(nil)
)
