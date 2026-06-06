package rerankhttp

import (
	"encoding/json"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
)

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(map[string]map[string]string{"error": {"code": code, "message": message}})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	_, _ = w.Write(body)
}

func writeInsufficientBalanceError(w http.ResponseWriter) {
	writeJSONError(w, http.StatusPaymentRequired, clienterr.CodeInsufficientBalance, clienterr.MessageFor(clienterr.CodeInsufficientBalance))
}

func writeInsufficientQuotaError(w http.ResponseWriter) {
	writeJSONError(w, http.StatusTooManyRequests, clienterr.CodeInsufficientBalance, clienterr.MessageFor(clienterr.CodeInsufficientBalance))
}

func copyAllowedHeaders(dst, src http.Header) {
	for _, key := range []string{"Content-Type", "Openai-Processing-Ms", "Openai-Version", "X-Request-Id"} {
		for _, value := range src.Values(key) {
			dst.Add(key, value)
		}
	}
}
