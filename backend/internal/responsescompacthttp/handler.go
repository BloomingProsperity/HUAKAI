package responsescompacthttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

const maxCompactRequestBodyBytes = 1 << 20

// NewCompactHandler 拒绝流式的 compact 请求,移除任何非 true 的顶层
// stream 标志,然后委派给 canonical Responses 管线。
func NewCompactHandler(delegate http.Handler, canonicalPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if delegate == nil {
			writeCompactError(w, http.StatusServiceUnavailable, "invalid_request_error", "compact responses delegate unset")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCompactRequestBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeCompactError(w, http.StatusBadRequest, "invalid_request_error", "Request body could not be read")
			return
		}

		rewritten, ok := normalizeCompactBody(w, body)
		if !ok {
			return
		}
		req := r.Clone(r.Context())
		req.Body = io.NopCloser(bytes.NewReader(rewritten))
		req.ContentLength = int64(len(rewritten))
		if canonicalPath != "" {
			u := *r.URL
			u.Path = canonicalPath
			req.URL = &u
		}
		delegate.ServeHTTP(w, req)
	}
}

func normalizeCompactBody(w http.ResponseWriter, body []byte) ([]byte, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		writeCompactError(w, http.StatusBadRequest, "invalid_request_error", "Request body must be a JSON object")
		return nil, false
	}
	rawStream, hasStream := payload["stream"]
	if !hasStream {
		return body, true
	}
	var stream bool
	if err := json.Unmarshal(rawStream, &stream); err == nil && stream {
		writeCompactError(w, http.StatusBadRequest, "invalid_request_error", "Streaming not supported for compact responses")
		return nil, false
	}
	delete(payload, "stream")
	rewritten, err := json.Marshal(payload)
	if err != nil {
		writeCompactError(w, http.StatusBadRequest, "invalid_request_error", "Request body could not be rewritten")
		return nil, false
	}
	return rewritten, true
}

func writeCompactError(w http.ResponseWriter, status int, typ, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    typ,
			"message": message,
		},
	})
}
