package apikeyexpiry

import (
	"context"
	"fmt"
)

const defaultBatchLimit int32 = 500

type ExpiryQueries interface {
	ExpireActiveAPIKeys(ctx context.Context, batchLimit int32) (int64, error)
}

type Service struct {
	queries    ExpiryQueries
	batchLimit int32
}

type Option func(*Service)

func WithBatchLimit(limit int32) Option {
	return func(s *Service) {
		s.batchLimit = limit
	}
}

func NewService(queries ExpiryQueries, opts ...Option) *Service {
	s := &Service{
		queries:    queries,
		batchLimit: defaultBatchLimit,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.batchLimit <= 0 {
		s.batchLimit = defaultBatchLimit
	}
	return s
}

func (s *Service) SweepExpiredKeys(ctx context.Context) (int64, error) {
	if s == nil || s.queries == nil {
		return 0, nil
	}
	var total int64
	limit := s.batchLimit
	if limit <= 0 {
		limit = defaultBatchLimit
	}
	for {
		changed, err := s.queries.ExpireActiveAPIKeys(ctx, limit)
		total += changed
		if err != nil {
			return total, fmt.Errorf("sweep expired api keys: %w", err)
		}
		if changed == 0 {
			return total, nil
		}
	}
}
