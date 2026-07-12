package privacy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
)

const defaultMaxRequestBody = 8 << 20

type contextKey string

const requestMetadataKey contextKey = "privacy.request_metadata"

type RequestMetadata struct {
	RequestID      string `json:"request_id,omitempty"`
	Model          string `json:"model,omitempty"`
	TokenCount     int    `json:"token_count,omitempty"`
	MessageCount   int    `json:"message_count,omitempty"`
	RawBodyDiscard bool   `json:"raw_body_discard"`
}

func MetadataFromContext(ctx context.Context) (RequestMetadata, bool) {
	meta, ok := ctx.Value(requestMetadataKey).(RequestMetadata)
	return meta, ok
}

func Middleware(maxBodyBytes int) func(http.Handler) http.Handler {
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxRequestBody
	}
	return MiddlewareFunc(func(*http.Request) int { return maxBodyBytes })
}

// MiddlewareFunc 与 Middleware 行为一致,但允许按请求(通常按路径)选不同的缓冲上限。
// 动机:本中间件在 auth 之前对所有路由全量缓冲 body 解析元数据;若给非 relay 的未认证端点
// (login/register 等)也用 relay 数据面的大上限,会把它们的 pre-auth 内存放大面无谓抬高。
// 让调用方按 relay/非 relay 路径返回不同上限,即可只给真正需要大体的 relay 路径放宽,其余维持小上限。
// maxBodyBytesFor 返回 <=0 时回退到 defaultMaxRequestBody。
func MiddlewareFunc(maxBodyBytesFor func(*http.Request) int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.ContentLength == 0 {
				next.ServeHTTP(w, r)
				return
			}
			maxBodyBytes := maxBodyBytesFor(r)
			if maxBodyBytes <= 0 {
				maxBodyBytes = defaultMaxRequestBody
			}
			limited := io.LimitReader(r.Body, int64(maxBodyBytes)+1)
			raw, err := io.ReadAll(limited)
			_ = r.Body.Close()
			if err != nil {
				http.Error(w, "read request body", http.StatusBadRequest)
				return
			}
			if len(raw) > maxBodyBytes {
				Zeroize(raw)
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			meta := parseRequestMetadata(raw)
			meta.RequestID = requestIDFromHTTP(r)
			meta.RawBodyDiscard = true
			body := &zeroizingReadCloser{Reader: bytes.NewReader(raw), buf: raw}
			r = r.WithContext(context.WithValue(r.Context(), requestMetadataKey, meta))
			r.Body = body
			next.ServeHTTP(w, r)
			_ = body.Close()
		})
	}
}

func Recoverer(logger SystemLogger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = NewStdoutSystemLogger(DefaultRedactor())
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					ctx := r.Context()
					_ = logger.LogSystem(ctx, SystemEvent{
						Severity:   SeverityCritical,
						Component:  "http",
						RequestID:  requestIDFromHTTP(r),
						ErrorClass: "panic",
						PanicClass: "handler_panic",
						Attrs: map[string]any{
							"stack": string(debug.Stack()),
						},
					})
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type zeroizingReadCloser struct {
	*bytes.Reader
	buf    []byte
	closed bool
}

func (z *zeroizingReadCloser) Close() error {
	if z == nil || z.closed {
		return nil
	}
	z.closed = true
	Zeroize(z.buf)
	return nil
}

func parseRequestMetadata(raw []byte) RequestMetadata {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return RequestMetadata{}
	}
	var meta RequestMetadata
	if v := strings.TrimSpace(jsonString(root["model"])); v != "" {
		meta.Model = v
	}
	if messages, ok := root["messages"]; ok {
		var arr []json.RawMessage
		if err := json.Unmarshal(messages, &arr); err == nil {
			meta.MessageCount = len(arr)
		}
	}
	if maxTokens := jsonInt(root["max_tokens"]); maxTokens > 0 {
		meta.TokenCount = maxTokens
	}
	return meta
}

func jsonString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func jsonInt(raw json.RawMessage) int {
	var n int
	_ = json.Unmarshal(raw, &n)
	return n
}

func requestIDFromHTTP(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, name := range []string{"X-Request-Id", "X-Request-ID", "X-HUAKAI-Request-ID"} {
		if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
			return v
		}
	}
	return ""
}
