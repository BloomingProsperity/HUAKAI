// stream_scanner.go — A1 atomic（Bedrock plan §A1）：StreamForwarder
// 从硬编码 SSE 行扫描升级为可插拔的 wire-format scanner。
//
// 现状（A1 之前）：StreamForwarder.Forward 内部直接调用 ScanSSEEvents，
// 这意味着所有 protocol family 都被假定走 SSE 文本帧。Bedrock streaming
// 的 binary EventStream 无法塞进 bufio.Scanner（按 \n 切分会切碎 frame）。
//
// A1 引入的抽象层（行为不变）：
//   - StreamScanner 接口：把 io.Reader 切成 SSEEvent 流（保留事件结构，
//     避免大改 forwarder 下游消费代码）
//   - StreamScannerRegistry：按 protocol family 路由到对应 scanner
//   - SSEStreamScanner：thin wrapper 调旧 ScanSSEEvents（实现等价）
//   - BuildDefaultStreamScannerRegistry：覆盖所有现有 19 个 family，全部
//     走 SSE。Bedrock 专属 scanner 在 A2+A3 atomic 加入
//
// 不做：
//   - 不改 SSEEvent 结构（保留 Type/Data/ObservedAt 三字段；下游 forwarder
//     代码不动）
//   - 不动 ScanSSEEvents 实现（只是被 SSEStreamScanner 调用）
//   - 不引入新的 wire 元数据字段（StreamWireProtocol 留到
//     未来 observability 需要时再加）
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"reflect"
)

// StreamScanner 把 io.Reader 上的字节按 wire format 切成 SSEEvent 流。
//
// Scan 返回值规则：
//   - 正常事件：(SSEEvent{Type, Data, ObservedAt}, nil)
//   - scanner 内部错误：(SSEEvent{}, err)（如 ErrScannerOverflow / ctx.Err()）
//   - 流结束：iterator 自然 done，不 yield 任何零值
//
// 实现责任：
//   - 必须遵守 ctx.Done() 提前退出
//   - bufferCap 是底层缓冲上限，scanner 自行决定如何 honor（SSE 走
//     bufio.Scanner.Buffer；Bedrock 走逐 frame 读 + frame size 上限）
type StreamScanner interface {
	Scan(ctx context.Context, r io.Reader, bufferCap int) iter.Seq2[SSEEvent, error]
}

// StreamScannerRegistry 按 protocol family 字符串返回对应 StreamScanner。
type StreamScannerRegistry interface {
	For(protocolFamily string) (StreamScanner, error)
}

// ErrUnknownStreamScanner 表示 protocol family 没有注册对应的 scanner。
var ErrUnknownStreamScanner = errors.New("gateway: 未注册该 protocol family 的 stream scanner")

// errDuplicateStreamScanner 用于 Register 重复注册。
var errDuplicateStreamScanner = errors.New("gateway: stream scanner 重复注册")

// StaticStreamScannerRegistry 是只读静态注册表；启动期 Register 完成后只读。
// 与 StaticProtocolAdapterRegistry 同形态：MustRegister panic on duplicate / nil。
type StaticStreamScannerRegistry struct {
	scanners map[string]StreamScanner
}

// NewStaticStreamScannerRegistry 返回空的 scanner 注册表。
func NewStaticStreamScannerRegistry() *StaticStreamScannerRegistry {
	return &StaticStreamScannerRegistry{scanners: make(map[string]StreamScanner)}
}

// Register 注册 protocol family 到 scanner 的映射。
func (r *StaticStreamScannerRegistry) Register(family string, s StreamScanner) error {
	if r == nil {
		return errors.New("gateway: stream scanner registry 是 nil")
	}
	if family == "" {
		return errors.New("gateway: protocol family 不能为空")
	}
	if isNilStreamScanner(s) {
		return errors.New("gateway: stream scanner 不能为 nil")
	}
	if r.scanners == nil {
		r.scanners = make(map[string]StreamScanner)
	}
	if _, ok := r.scanners[family]; ok {
		return fmt.Errorf("%w: %s", errDuplicateStreamScanner, family)
	}
	r.scanners[family] = s
	return nil
}

// MustRegister 是 Register 的 panic 版本，仅用于启动期确定性注册。
func (r *StaticStreamScannerRegistry) MustRegister(family string, s StreamScanner) {
	if err := r.Register(family, s); err != nil {
		panic(err)
	}
}

// For 返回 protocol family 对应的 stream scanner。
func (r *StaticStreamScannerRegistry) For(family string) (StreamScanner, error) {
	if r == nil || r.scanners == nil {
		return nil, fmt.Errorf("%w: registry 未初始化", ErrUnknownStreamScanner)
	}
	s, ok := r.scanners[family]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownStreamScanner, family)
	}
	return s, nil
}

// SSEStreamScanner 是 ScanSSEEvents 的 thin adapter，把现有 SSE 扫描
// 暴露为 StreamScanner 接口实现。行为完全等价。
type SSEStreamScanner struct{}

// Scan 委托给 ScanSSEEvents（包级现有函数）。签名一致，零拷贝。
func (s *SSEStreamScanner) Scan(ctx context.Context, r io.Reader, bufferCap int) iter.Seq2[SSEEvent, error] {
	return ScanSSEEvents(ctx, r, bufferCap)
}

// BuildDefaultStreamScannerRegistry 构造默认 scanner 注册表。
// 当前阶段除 bedrock_invoke 外所有 family 走 SSE；bedrock_invoke 走专用
// BedrockEventStreamScanner（A3 atomic 实现，binary EventStream 切帧）。
// 注册顺序与 BuildDefaultProtocolAdapterRegistry 保持一致便于代码 cross-check。
func BuildDefaultStreamScannerRegistry() *StaticStreamScannerRegistry {
	r := NewStaticStreamScannerRegistry()
	sse := &SSEStreamScanner{}

	// 31 个走 SSE 的 family（与 protocol_selector.BuildDefaultProtocolAdapterRegistry
	// 注册顺序对齐）。bedrock_invoke 走 binary scanner，单独注册在下方。
	for _, family := range []string{
		// 既有官方 API 路径
		"anthropic_messages",
		"openai_chat",
		"openai_responses",
		"openai_codex",
		"gemini_messages",
		"openrouter_chat",
		"grok_chat",
		// 6 家 OpenAI 兼容直 API key 路径
		"deepseek_chat",
		"mistral_chat",
		"groqcloud_chat",
		"together_chat",
		"perplexity_chat",
		"fireworks_chat",
		// 12 家 OpenAI 兼容族(国内 + cohere + ollama;均走 OpenAI 兼容 SSE)。
		// 与 protocol_selector 双注册;漏此处=这些 family 流式请求在 forwarder
		// Scanners.For 取 scanner 失败、投递前挂(同 23e0cb91 入站漏接同源)。
		// 注:ollama_chat 现走兼容模式 SSE;将来原生 /api/chat adapter 落地后
		// 改注册 NDJSON scanner(仍在两 registry 里,族集对称不变)。
		"kimi_chat",
		"qwen_chat",
		"glm_chat",
		"yi_chat",
		"baichuan_chat",
		"doubao_chat",
		"ernie_chat",
		"step_chat",
		"hunyuan_chat",
		"minimax_chat",
		"cohere_chat",
		"ollama_chat",
		// 6 家订阅 session 反转
		"copilot_session",
		"cursor_session",
		"gemini_advanced_session",
		"antigravity_session",
		"kiro_session",
		"windsurf_session",
	} {
		r.MustRegister(family, sse)
	}

	// AWS Bedrock invoke-with-response-stream 走 binary EventStream wire format
	// （非 SSE），需 BedrockEventStreamScanner 切帧并解 chunk envelope。
	r.MustRegister("bedrock_invoke", &BedrockEventStreamScanner{})

	return r
}

// isNilStreamScanner 检查 nil 接口（含 typed nil）。
// 复用 protocol_selector.isNilUpstreamAdapter 的 reflect 逻辑。
func isNilStreamScanner(s StreamScanner) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

var _ StreamScannerRegistry = (*StaticStreamScannerRegistry)(nil)
