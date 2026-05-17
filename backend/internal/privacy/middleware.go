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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.ContentLength == 0 {
				next.ServeHTTP(w, r)
				return
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
