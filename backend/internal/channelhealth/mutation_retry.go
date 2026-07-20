package channelhealth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	healthMutationMaxAttempts = 5
	healthMutationBackoffBase = time.Millisecond
	healthMutationBackoffCap  = 20 * time.Millisecond
)

func (s *Service) withMutation(ctx context.Context, fn func(*Service) (Record, error)) (Record, error) {
	if s == nil || s.store == nil {
		return Record{}, errors.New("channelhealth: service not configured")
	}
	txs, ok := s.store.(transactionalStore)
	if !ok {
		return fn(s)
	}
	var out Record
	for attempt := 0; attempt < healthMutationMaxAttempts; attempt++ {
		// 每次重试都创建新事务；失败轮次的日志直接丢弃，只有提交成功才对外记录。
		var pending []transitionLogRecord
		err := txs.WithTx(ctx, func(store Store) error {
			txService := *s
			txService.store = store
			txService.pendingTransitionLogs = &pending
			var innerErr error
			out, innerErr = fn(&txService)
			return innerErr
		})
		if err == nil {
			for i := range pending {
				s.logTransition(ctx, pending[i])
			}
			return out, nil
		}
		if !isHealthMutationConflict(err) || attempt+1 >= healthMutationMaxAttempts {
			return out, err
		}
		backoff := healthMutationBackoffBase << attempt
		if backoff > healthMutationBackoffCap {
			backoff = healthMutationBackoffCap
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return out, ctx.Err()
		case <-timer.C:
		}
	}
	return out, errors.New("channelhealth: mutation retry exhausted")
}

func isHealthMutationConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}
