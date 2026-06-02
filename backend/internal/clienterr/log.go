package clienterr

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

func LogInternal(ctx context.Context, requestID, code string, err error) {
	if err == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = privacy.LogSystem(ctx, privacy.SystemEvent{
		Severity:   privacy.SeverityError,
		Component:  "clienterr",
		RequestID:  requestID,
		ErrorClass: privacy.ErrorClassFor(ctx, err),
		Attrs: map[string]any{
			"event_class": code,
		},
	})
}
