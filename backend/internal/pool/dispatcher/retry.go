package dispatcher

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

const slotAcquireSerializableAttempts = 3

func canFallbackAfterPASRError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, ErrPASRPostMutationFail)
}

func retrySerializableSlotAcquire(ctx context.Context, fn func(context.Context) (*AcquireResult, error)) (*AcquireResult, error) {
	var lastErr error
	for attempt := 0; attempt < slotAcquireSerializableAttempts; attempt++ {
		res, err := fn(ctx)
		if err == nil || !isSerializationFailure(err) {
			return res, err
		}
		lastErr = err
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
	}
	return nil, lastErr
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}
