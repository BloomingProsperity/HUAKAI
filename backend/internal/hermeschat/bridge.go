package hermeschat

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/headerfirewall"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
)

type txRunner interface {
	RunHermesTx(context.Context, func(hermes.Store) error) error
}

type warningLogger interface {
	Warnf(string, ...any)
}

type stdWarningLogger struct{}

func (stdWarningLogger) Warnf(format string, args ...any) {
	log.Printf(format, args...)
}

type Option func(*Bridge)

func WithInternalTokenSecret(secret []byte) Option {
	return func(b *Bridge) {
		b.internalTokenSecret = append([]byte(nil), secret...)
	}
}

func WithInternalBaseURL(baseURL string) Option {
	return func(b *Bridge) {
		b.internalBaseURL = strings.TrimSpace(baseURL)
	}
}

func WithClock(now func() time.Time) Option {
	return func(b *Bridge) {
		if now != nil {
			b.now = now
		}
	}
}

func WithWarningLogger(logger warningLogger) Option {
	return func(b *Bridge) {
		if logger != nil {
			b.logger = logger
		}
	}
}

func WithAuditDLQ(dlq auditDLQ) Option {
	return func(b *Bridge) {
		b.auditDLQ = dlq
	}
}

func WithResponseHeaderSettings(settings headerfirewall.PlatformSettings) Option {
	return func(b *Bridge) {
		b.headerSettings = settings
	}
}

func WithMessageContentKeys(keys credentialstore.KeyProvider) Option {
	return func(b *Bridge) {
		b.messageContentKeys = keys
	}
}

// WithSessionBindings 挂载会话式只读工具循环(WAVE H3b)所用的进程内 operator 绑定存储。
// 设置后,PrepareRequest 会绑定每个聊天会话的 operator 身份(以 internal_token 的
// request_id 为键),使 runner 在对话中途的工具回调能解析到该会话的 operator。为 nil 时,
// 聊天路径保持不变,且没有可用的工具循环(内部工具端点在缺少绑定时 fail closed)。
func WithSessionBindings(b2 *SessionBindings) Option {
	return func(b *Bridge) {
		b.sessionBindings = b2
	}
}

// WithToolCatalog 挂载工具目录提供方。设置后,PrepareRequest 会把经清洗的目录注入 runner 的
// 聊天 payload,使 LLM 知道它可以调用哪些诊断工具(并据此使回答有据可依)。默认提供方只返回只读
// 工具;在 Phase B 提议 KNOB 打开时,提供方还会带上可提议的 B 级 mutating 工具(每个都打了
// mutating / requires_confirmation 标志,只供 LLM 提议、绝不直接执行)。
func WithToolCatalog(provider ToolCatalogProvider) Option {
	return func(b *Bridge) {
		b.toolCatalog = provider
	}
}

// WithToolLoopEnabled 是 KNOB B 的 bridge 侧闸门:LLM 会话式工具循环目录注入的运行时
// kill-switch。为 false 时,即便是已绑定的 admin 会话,PrepareRequest 也不会在 runner
// body 中设置 tool_catalog 字段,因此 LLM 被告知没有任何工具。默认 true(在 NewBridge
// 中设置)=> 未设置时行为零变。与 WithToolCatalog/WithSessionBindings 正交:即使已接线
// 目录提供方,本闸门仍会抑制注入。
func WithToolLoopEnabled(enabled bool) Option {
	return func(b *Bridge) {
		b.toolLoopEnabled = enabled
	}
}

// ToolCatalogProvider 返回注入聊天 payload 的、面向 LLM 的工具目录。
// *hermesops.Registry 可通过一层薄适配器满足此接口;bridge 只依赖 marshal 后的结构形状,因此不
// import hermesops。
type ToolCatalogProvider interface {
	// ToolCatalog 以已可 marshal 的值返回目录条目。只读条目为 {name, description, input_schema};
	// 可提议的 mutating 条目额外带 mutating / requires_confirmation 标志。具体注入哪一种(只读目录 vs
	// 可提议目录)由提供方按 Phase B 提议 KNOB 决定;不可提议的 mutating 工具永不出现。
	ToolCatalog() []map[string]any
}

type Bridge struct {
	tx                  txRunner
	internalTokenSecret []byte
	internalBaseURL     string
	now                 func() time.Time
	logger              warningLogger
	auditDLQ            auditDLQ
	headerSettings      headerfirewall.PlatformSettings
	messageContentKeys  credentialstore.KeyProvider
	// sessionBindings + toolCatalog 支撑会话式只读工具循环(WAVE H3b)。两者均可选:
	// 为 nil 时聊天路径保持不变。
	sessionBindings *SessionBindings
	toolCatalog     ToolCatalogProvider
	// toolLoopEnabled 是 KNOB B 的 bridge 侧闸门。默认 true(NewBridge)。为 false 时,
	// 即便是已绑定的 admin 会话也不会向 runner body 注入 tool_catalog。通过
	// WithToolLoopEnabled 设置。
	toolLoopEnabled bool
}

func NewBridge(tx txRunner, opts ...Option) (*Bridge, error) {
	b := &Bridge{
		tx: tx, internalBaseURL: DefaultInternalBaseURL,
		now:    func() time.Time { return time.Now().UTC() },
		logger: stdWarningLogger{},
		// KNOB B 默认值:除非有 WithToolLoopEnabled(false) 选项翻转它,否则 LLM 会话式
		// 工具循环处于启用状态——未设置时行为零变。
		toolLoopEnabled: true,
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.tx == nil {
		return nil, fmt.Errorf("%w: hermes transaction runner is required", hermes.ErrMisconfigured)
	}
	if len(b.internalTokenSecret) == 0 {
		return nil, fmt.Errorf("%w: %s is required", hermes.ErrMisconfigured, InternalTokenSecretEnv)
	}
	if strings.TrimSpace(b.internalBaseURL) == "" {
		return nil, fmt.Errorf("%w: %s is required", hermes.ErrMisconfigured, InternalBaseURLEnv)
	}
	if b.messageContentKeys == nil {
		return nil, fmt.Errorf("%w: hermes message content encryption key provider is required", hermes.ErrMisconfigured)
	}
	return b, nil
}

// ReleaseSession 为一个已结束的聊天会话丢弃其 WAVE H3b operator 绑定。在 request_id
// 为空或未接线绑定存储时调用是安全的(空操作)。startChat 会在流结束后 defer 调用它,
// 这样即使在基于过期的清理回收之前,绑定也不会比其会话存活更久。
func (b *Bridge) ReleaseSession(requestID string) {
	if b == nil || b.sessionBindings == nil {
		return
	}
	b.sessionBindings.Release(requestID)
}

func (b *Bridge) Stream(ctx context.Context, w http.ResponseWriter, resp *http.Response, prepared PreparedRequest) error {
	if b == nil || w == nil || resp == nil || resp.Body == nil {
		return hermes.ErrMisconfigured
	}
	defer resp.Body.Close()
	policy := headerfirewall.PolicyFromPlatformSettings(ctx, b.headerSettings)
	copyResponseHeadersWithPolicy(w, resp.Header, policy)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream")
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	flush(flusher)

	if prepared.CreatedConversation {
		if err := writeConversationEvent(w, flusher, prepared.ConversationID); err != nil {
			return err
		}
	}
	state := &streamState{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	var block bytes.Buffer
	for scanner.Scan() {
		line := scanner.Bytes()
		block.Write(line)
		block.WriteByte('\n')
		if len(bytes.TrimSpace(line)) == 0 {
			if err := b.handleBlock(ctx, w, flusher, prepared, state, block.Bytes()); err != nil {
				return err
			}
			block.Reset()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if block.Len() > 0 {
		if err := b.handleBlock(ctx, w, flusher, prepared, state, block.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

func copyResponseHeaders(w http.ResponseWriter, header http.Header) {
	copyResponseHeadersWithPolicy(w, header, headerfirewall.Policy{})
}

func copyResponseHeadersWithPolicy(w http.ResponseWriter, header http.Header, policy headerfirewall.Policy) {
	filtered := headerfirewall.FilterResponseHeaders(header, policy.ExtraDeny, policy.AllowOverride)
	for k, values := range filtered {
		if bridgeManagedHeader(k) {
			continue
		}
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
}

func bridgeManagedHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Content-Length", "Transfer-Encoding":
		return true
	default:
		return hopByHopHeader(name)
	}
}

func writeConversationEvent(w io.Writer, flusher http.Flusher, conversationID int64) error {
	return writeAndFlush(w, flusher, []byte(fmt.Sprintf("event: conversation\ndata: {\"id\":%d}\n\n", conversationID)))
}

func writeConversationMismatchError(w io.Writer, flusher http.Flusher) error {
	return writeAndFlush(w, flusher, []byte("event: error\ndata: {\"code\":\"conversation_mismatch\",\"message\":\"runner conversation id mismatch\"}\n\n"))
}

func writePersistError(w io.Writer, flusher http.Flusher) error {
	return writeAndFlush(w, flusher, []byte("event: error\ndata: {\"code\":\"persist_failed\",\"message\":\"message persistence failed\"}\n\n"))
}

func writeAndFlush(w io.Writer, flusher http.Flusher, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	flush(flusher)
	return nil
}

func flush(flusher http.Flusher) {
	if flusher != nil {
		flusher.Flush()
	}
}

func hopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
