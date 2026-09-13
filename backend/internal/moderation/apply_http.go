package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
)

// ErrPolicyBlocked 表示审核引擎已拒绝请求，调用方应映射为稳定 403，不得改写为 5xx。
var ErrPolicyBlocked = errors.New("moderation: content policy violation")

// DecisionBlocks 判定审核结果是否阻断后续预扣费与出站。
func DecisionBlocks(decision Decision) bool {
	switch decision {
	case "", DecisionPass:
		return false
	default:
		return true
	}
}

// ScreenBody 执行审核但不写 HTTP。screener 为 nil 时放行。
func ScreenBody(ctx context.Context, screener Screener, req ScreenRequest) error {
	if screener == nil {
		return nil
	}
	result, err := screener.Screen(ctx, req)
	if err != nil {
		slog.ErrorContext(ctx, "moderation screen failed",
			"request_id", req.RequestID,
			"code", clienterr.CodeContentPolicyViolation,
			"err", err,
		)
		return ErrPolicyBlocked
	}
	if DecisionBlocks(result.Decision) {
		return ErrPolicyBlocked
	}
	return nil
}

// ApplyHTTP 在入口鉴权之后、预扣费与出站之前执行内容审核并写入稳定 403。
// screener 为 nil 时放行，对应测试桩与运行时关闭。
func ApplyHTTP(w http.ResponseWriter, ctx context.Context, screener Screener, req ScreenRequest) bool {
	if err := ScreenBody(ctx, screener, req); err != nil {
		writePolicyViolation(w)
		return false
	}
	return true
}

func writePolicyViolation(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	body, err := json.Marshal(map[string]map[string]string{
		"error": {
			"code":    clienterr.CodeContentPolicyViolation,
			"message": clienterr.MessageFor(clienterr.CodeContentPolicyViolation),
		},
	})
	if err != nil {
		body = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	_, _ = w.Write(body)
}
