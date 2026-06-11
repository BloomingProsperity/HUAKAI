package geminicodeassist

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/proto/gemini"
)

// Adapter 解析 cloudcode-pa Code Assist 响应：先 unwrap 外层 "response"，再
// 委托既有 gemini.Adapter 做标准 Gemini 解析。
//
// 组合（非继承）：内嵌一个 *gemini.Adapter，请求侧方法（CanonicalToProviderRequest /
// FinalizeUpstreamStream）直接透传；响应侧两法（ProviderResponseToCanonical /
// ProviderEventToCanonicalEvents）先 unwrap 再委托。
//
// UpstreamState 复用 gemini.UpstreamState——forwarder/protosse 的
// newUpstreamState 类型分派需为 *geminicodeassist.Adapter 加 case 返回
// &gemini.UpstreamState{}（族集对称第 8 站，否则落 default=anthropic state，
// gemini 委托内的 type-assert 失败）。
type Adapter struct {
	inner gemini.Adapter
}

// 编译期接口合规断言。
var _ proto.UpstreamAdapter = (*Adapter)(nil)

// CanonicalToProviderRequest 透传 inner。请求 envelope（{model,project,request}）
// 由出站 provider/gemini.CodeAssistAdapter 包装，本入站层不重复——委托 inner
// 以保持接口诚实（gemini.Adapter 的实现返回 proto.ErrNotImplemented，因为
// gemini 请求 body 由 HCSF marshal 层产出）。
func (a *Adapter) CanonicalToProviderRequest(ctx context.Context, canonical *proto.HCSF) ([]byte, []proto.ProtocolLossEntry, error) {
	return a.inner.CanonicalToProviderRequest(ctx, canonical)
}

// ProviderResponseToCanonical unwrap 外层 "response" 后委托 inner。
//
// 非流式 cloudcode-pa body 形如 {"response":{<gemini body>}}；unwrap 后喂
// gemini.Adapter 的标准非流式解析。容忍已 unwrap 的裸 gemini body（无
// "response" 字段时原样用 raw）。
func (a *Adapter) ProviderResponseToCanonical(ctx context.Context, raw []byte) (*proto.HCSF, []proto.ProtocolLossEntry, error) {
	return a.inner.ProviderResponseToCanonical(ctx, unwrapResponse(raw))
}

// ProviderEventToCanonicalEvents 对每个 SSE chunk unwrap "response" 后委托 inner。
//
// providerEvt 可能是 gemini.SSEEvent / []byte / string / json.RawMessage / 流
// 终止哨兵。哨兵原样透传（unwrap 不适用）；数据型先取出 data 字节、unwrap
// "response"、再以等价形态回喂 inner（保留 SSEEvent 的 Type/Done 元数据）。
func (a *Adapter) ProviderEventToCanonicalEvents(ctx context.Context, providerEvt any, state any) ([]any, []proto.ProtocolLossEntry, error) {
	switch evt := providerEvt.(type) {
	case gemini.SSEEvent:
		// 终止帧（Done 或 type=="end"）不含数据，原样透传给 inner 触发 finalize。
		if evt.Done || evt.Type == "end" {
			return a.inner.ProviderEventToCanonicalEvents(ctx, evt, state)
		}
		evt.Data = unwrapResponse(bytes.TrimSpace(evt.Data))
		return a.inner.ProviderEventToCanonicalEvents(ctx, evt, state)
	case json.RawMessage:
		return a.inner.ProviderEventToCanonicalEvents(ctx, json.RawMessage(unwrapResponse(evt)), state)
	case []byte:
		return a.inner.ProviderEventToCanonicalEvents(ctx, unwrapResponse(evt), state)
	case string:
		return a.inner.ProviderEventToCanonicalEvents(ctx, string(unwrapResponse([]byte(evt))), state)
	default:
		// 流终止哨兵（gemini.StreamEnd / nil / error 等）与未知形态原样透传，
		// 让 inner 的 coerce 逻辑统一处置。
		return a.inner.ProviderEventToCanonicalEvents(ctx, providerEvt, state)
	}
}

// FinalizeUpstreamStream 透传 inner。
func (a *Adapter) FinalizeUpstreamStream(ctx context.Context, state any) ([]any, error) {
	return a.inner.FinalizeUpstreamStream(ctx, state)
}

// unwrapResponse 剥掉 cloudcode-pa 的 {"response":...} 外层，返回内层 gemini
// body 字节。无 "response" 字段（已 unwrap / 非 envelope）时原样返回 raw（容忍）。
// raw 为 SSE 多行（含 "data:" 前缀）或非 JSON 时也原样返回——交给 inner 的
// extractGeminiSSEData 先剥 SSE 前缀后再由调用方/inner 处理；本函数只处理纯
// JSON envelope 的 unwrap。
func unwrapResponse(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw
	}
	// 仅当是 JSON 对象时尝试 unwrap；非对象（SSE 行、数组等）原样返回。
	if trimmed[0] != '{' {
		return raw
	}
	var env codeAssistResponseEnvelope
	if json.Unmarshal(trimmed, &env) != nil {
		return raw
	}
	if len(bytes.TrimSpace(env.Response)) == 0 {
		// 无 "response" 字段或为空 → 已 unwrap 的裸 gemini body，容忍原样用。
		return raw
	}
	return env.Response
}
