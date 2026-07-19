package accesslog

import (
	"io"
	"net/http"
	"strconv"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
)

// Middleware 在被包裹的 handler 返回后,发出一条结构化的 access-log 事件。
// 它刻意只记录 URL.Path,绝不记录 query、body、headers、credentials 或
// remote IP。
func Middleware(logger *zap.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			result, errorClass, errorCode, retryable := classifyHTTPResult(rec.status)
			fields := []zap.Field{
				zap.String(logcontract.FieldCategory, string(logcontract.CategoryAccess)),
				zap.String(logcontract.FieldEventType, "http.request_completed"),
				zap.String(logcontract.FieldResult, result),
				zap.String(logcontract.FieldErrorClass, errorClass),
				zap.String(logcontract.FieldErrorCode, errorCode),
				zap.Bool(logcontract.FieldRetryable, retryable),
				zap.String(logcontract.FieldRecoveryState, string(logcontract.RecoveryNone)),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", rec.status),
				zap.Int64("latency_ms", time.Since(start).Milliseconds()),
				zap.String("request_id", chimiddleware.GetReqID(r.Context())),
				zap.Int64("bytes_out", rec.bytes),
			}
			if rec.status >= http.StatusInternalServerError {
				logger.Error("access_log", fields...)
			} else {
				// 客户端失败仍由 result/error_class 明确标记，但不占用服务端异常队列。
				logger.Info("access_log", fields...)
			}
		})
	}
}

func classifyHTTPResult(status int) (result, errorClass, errorCode string, retryable bool) {
	if status < http.StatusBadRequest {
		return string(logcontract.ResultSuccess), string(logcontract.ErrorNone), "none", false
	}
	errorCode = "http.status_" + strconv.Itoa(status)
	switch status {
	case http.StatusUnauthorized:
		return string(logcontract.ResultDenied), string(logcontract.ErrorAuthentication), errorCode, false
	case http.StatusForbidden:
		return string(logcontract.ResultDenied), string(logcontract.ErrorAuthorization), errorCode, false
	case http.StatusPaymentRequired:
		return string(logcontract.ResultDenied), string(logcontract.ErrorInsufficientBalance), errorCode, false
	case http.StatusConflict:
		return string(logcontract.ResultClientFailure), string(logcontract.ErrorConflict), errorCode, false
	case http.StatusTooManyRequests:
		return string(logcontract.ResultDenied), string(logcontract.ErrorRateLimit), errorCode, true
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return string(logcontract.ResultTimeout), string(logcontract.ErrorTimeout), errorCode, true
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return string(logcontract.ResultServerFailure), string(logcontract.ErrorDependency), errorCode, true
	case http.StatusNotImplemented:
		return string(logcontract.ResultServerFailure), string(logcontract.ErrorUnknown), errorCode, false
	}
	if status >= http.StatusInternalServerError {
		return string(logcontract.ResultServerFailure), string(logcontract.ErrorUnknown), errorCode, true
	}
	return string(logcontract.ResultClientFailure), string(logcontract.ErrorValidation), errorCode, false
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.status = status
	r.wrote = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Flush() {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) ReadFrom(src io.Reader) (int64, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	if rf, ok := r.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		r.bytes += n
		return n, err
	}
	n, err := io.Copy(r.ResponseWriter, src)
	r.bytes += n
	return n, err
}
