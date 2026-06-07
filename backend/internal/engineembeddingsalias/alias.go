package engineembeddingsalias

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
)

const maxAliasRequestBodyBytes = 2 << 20

// NewHandler injects the legacy /engines/{model}/embeddings path model into
// request bodies that omit model, then delegates to the canonical embeddings
// handler.
func NewHandler(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if next == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "gateway_not_configured",
					"message": "engines embeddings alias delegate unset",
				},
			})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxAliasRequestBodyBytes+1))
		if err != nil {
			writeAliasError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
			return
		}
		if len(body) > maxAliasRequestBodyBytes {
			writeAliasError(w, http.StatusBadRequest, clienterr.CodeBodyReadError, clienterr.MessageFor(clienterr.CodeBodyReadError))
			return
		}
		rewritten := injectPathModel(body, chi.URLParam(r, "model"))
		delegateWithBody(next, w, r, rewritten)
	}
}

func injectPathModel(body []byte, pathModel string) []byte {
	if strings.TrimSpace(pathModel) == "" {
		return body
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return body
	}
	if _, exists := payload["model"]; exists {
		return body
	}
	encoded, err := json.Marshal(pathModel)
	if err != nil {
		return body
	}
	payload["model"] = encoded
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

func delegateWithBody(next http.Handler, w http.ResponseWriter, r *http.Request, body []byte) {
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	next.ServeHTTP(w, req)
}

func writeAliasError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
