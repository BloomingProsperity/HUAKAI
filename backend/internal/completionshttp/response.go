package completionshttp

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

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

// writeInsufficientQuotaErrorRetryable 把窗口配额拒绝的退避信息写回客户端。
func writeInsufficientQuotaErrorRetryable(w http.ResponseWriter, retryAfter time.Duration, windowKind string) {
	w.Header().Set("Content-Type", "application/json")
	errFields := map[string]string{
		"code":    clienterr.CodeInsufficientBalance,
		"message": clienterr.MessageFor(clienterr.CodeInsufficientBalance),
	}
	if retryAfter > 0 {
		secs := int64(math.Ceil(retryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		errFields["window_resets_at"] = time.Now().UTC().Add(retryAfter).Format(time.RFC3339)
	}
	if windowKind != "" {
		errFields["window_kind"] = windowKind
	}
	w.WriteHeader(http.StatusTooManyRequests)
	body, err := json.Marshal(map[string]map[string]string{"error": errFields})
	if err != nil {
		body = []byte(`{"error":{"code":"insufficient_balance","message":"余额不足"}}`)
	}
	_, _ = w.Write(body)
}

func copyAllowedHeaders(dst, src http.Header) {
	for _, key := range []string{"Content-Type", "Openai-Processing-Ms", "Openai-Version", "X-Request-Id"} {
		for _, value := range src.Values(key) {
			dst.Add(key, value)
		}
	}
}
