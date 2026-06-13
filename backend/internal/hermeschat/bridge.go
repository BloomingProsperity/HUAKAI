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

// WithSessionBindings attaches the in-process operator-binding store used by the
// conversational READ-ONLY tool loop (WAVE H3b). When set, PrepareRequest binds
// each chat session's operator identity (keyed by the internal_token's
// request_id) so the runner's mid-conversation tool callbacks resolve to the
// session operator. When nil, the chat path is unchanged and no tool loop is
// available (the internal tool endpoint fails closed on an absent binding).
func WithSessionBindings(b2 *SessionBindings) Option {
	return func(b *Bridge) {
		b.sessionBindings = b2
	}
}

// WithToolCatalog attaches the read-only tool catalog provider. When set,
// PrepareRequest injects the sanitized catalog into the runner's chat payload so
// the LLM knows which diagnostic tools it may call (and grounds its answers).
// The provider returns ONLY read-only tools; a mutating tool is never described.
func WithToolCatalog(provider ToolCatalogProvider) Option {
	return func(b *Bridge) {
		b.toolCatalog = provider
	}
}

// ToolCatalogProvider returns the LLM-facing read-only tool catalog injected into
// the chat payload. *hermesops.Registry's ReadOnlyCatalog satisfies a thin
// adapter for this; the bridge depends only on the marshaled shape so it does
// not import hermesops.
type ToolCatalogProvider interface {
	// ReadOnlyToolCatalog returns the catalog entries as already-marshalable
	// values (name + description + input_schema). It MUST exclude mutating tools.
	ReadOnlyToolCatalog() []map[string]any
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
	// sessionBindings + toolCatalog back the conversational READ-ONLY tool loop
	// (WAVE H3b). Both optional: when nil, the chat path is unchanged.
	sessionBindings *SessionBindings
	toolCatalog     ToolCatalogProvider
}

func NewBridge(tx txRunner, opts ...Option) (*Bridge, error) {
	b := &Bridge{
		tx: tx, internalBaseURL: DefaultInternalBaseURL,
		now:    func() time.Time { return time.Now().UTC() },
		logger: stdWarningLogger{},
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

// ReleaseSession drops the WAVE H3b operator binding for a finished chat
// session. It is safe to call with an empty request_id or when no bindings store
// is wired (no-op). startChat defers it after the stream so a binding does not
// outlive its session even before the expiry-based prune reclaims it.
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
