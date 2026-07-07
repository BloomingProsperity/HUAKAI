package rate

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
)

const (
	defaultModelCooldownDuration = 5 * time.Minute
	modelCooldownActor           = "system:gateway"
	modelCooldownSourceLayer     = "gateway_upstream_error"
)

var ErrModelCooldownInvalidInput = errors.New("rate: invalid model cooldown input")

type ModelCooldownInput struct {
	TenantID          int64
	ProviderAccountID int64
	ModelKey          string
	ResetAt           time.Time
	Reason            Reason
	StatusCode        int
	UpstreamRequestID string
}

type modelCooldownQueries interface {
	SetProviderAccountModelRateLimit(context.Context, dbbilling.SetProviderAccountModelRateLimitParams) error
}

type ModelCooldownService struct {
	q               modelCooldownQueries
	now             func() time.Time
	defaultCooldown time.Duration
}

type ModelCooldownOption func(*ModelCooldownService)

func NewModelCooldownService(q modelCooldownQueries, opts ...ModelCooldownOption) *ModelCooldownService {
	s := &ModelCooldownService{
		q:               q,
		now:             time.Now,
		defaultCooldown: defaultModelCooldownDuration,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithNow(fn func() time.Time) ModelCooldownOption {
	return func(s *ModelCooldownService) {
		if fn != nil {
			s.now = fn
		}
	}
}

func WithDefaultCooldown(d time.Duration) ModelCooldownOption {
	return func(s *ModelCooldownService) {
		if d > 0 {
			s.defaultCooldown = d
		}
	}
}

func (s *ModelCooldownService) RecordModelRateLimit(ctx context.Context, in ModelCooldownInput) error {
	if s == nil || s.q == nil {
		return ErrModelCooldownInvalidInput
	}
	modelKey := strings.TrimSpace(in.ModelKey)
	if in.TenantID == 0 || in.ProviderAccountID == 0 || modelKey == "" {
		return ErrModelCooldownInvalidInput
	}
	reason := in.Reason
	if reason == "" {
		reason = ReasonModelLimitExceeded
	}
	resetAt := in.ResetAt
	if resetAt.IsZero() {
		resetAt = s.now().UTC().Add(s.defaultCooldown)
	}
	return s.q.SetProviderAccountModelRateLimit(ctx, dbbilling.SetProviderAccountModelRateLimitParams{
		TenantID:           in.TenantID,
		ProviderAccountID:  in.ProviderAccountID,
		ModelKey:           modelKey,
		ResetAt:            pgtype.Timestamptz{Time: resetAt.UTC(), Valid: true},
		Reason:             string(reason),
		UpstreamStatusCode: int32(in.StatusCode),
		UpstreamRequestID:  strings.TrimSpace(in.UpstreamRequestID),
		SourceLayer:        modelCooldownSourceLayer,
		ActorID:            modelCooldownActor,
	})
}
