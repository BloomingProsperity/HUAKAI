package gatewayhttp

import (
	"fmt"
	"net/http"
)

const MaxRequestIDLength = 256

// RequestIDLengthLimiter 拒绝过长 X-Request-Id，避免下游账务和审计路径放大输入。
func RequestIDLengthLimiter(maxBytes int) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = MaxRequestIDLength
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(r.Header.Get("X-Request-Id")) > maxBytes {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, `{"error":"request_id_too_long"}`)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
