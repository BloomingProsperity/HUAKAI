package eventbus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeHandlerErrorMapsOwnDeadlineToHandlerTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	r := &handlerRunner{handler: HandlerFunc{HandlerID: HandlerBillingPersister}}
	err := r.normalizeHandlerError(ctx, ctx.Err())
	if !errors.Is(err, ErrHandlerTimeout) {
		t.Fatalf("normalizeHandlerError err=%v want ErrHandlerTimeout", err)
	}
}

func TestNormalizeHandlerErrorLeavesExternalDeadlineAlone(t *testing.T) {
	r := &handlerRunner{handler: HandlerFunc{HandlerID: HandlerBillingPersister}}
	err := r.normalizeHandlerError(context.Background(), context.DeadlineExceeded)
	if errors.Is(err, ErrHandlerTimeout) {
		t.Fatalf("normalizeHandlerError rewrote external deadline: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("normalizeHandlerError err=%v want context deadline", err)
	}
}
