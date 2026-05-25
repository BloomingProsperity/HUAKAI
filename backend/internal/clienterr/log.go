package clienterr

import (
	"context"
	"log/slog"
)

func LogInternal(ctx context.Context, requestID, code string, err error) {
	if err == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	slog.Default().LogAttrs(ctx, slog.LevelError, "internal error for public response",
		slog.String("request_id", requestID),
		slog.String("public_code", code),
		slog.Any("err", err),
	)
}
