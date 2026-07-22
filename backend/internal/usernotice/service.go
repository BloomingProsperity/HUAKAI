package usernotice

import (
	"context"
	"strings"
	"time"
)

const (
	defaultListLimit               = 50
	maxListLimit                   = 100
	defaultBroadcastRecipientLimit = 10000
)

type Service struct {
	store                   Store
	now                     func() time.Time
	broadcastRecipientLimit int
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func NewService(store Store, opts ...Option) *Service {
	s := &Service{
		store:                   store,
		now:                     func() time.Time { return time.Now().UTC() },
		broadcastRecipientLimit: defaultBroadcastRecipientLimit,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) Broadcast(ctx context.Context, in BroadcastInput) (BroadcastResult, error) {
	if s == nil || s.store == nil {
		return BroadcastResult{}, ErrStoreNotConfigured
	}
	notice, err := normalizeBroadcastInput(in, s.now().UTC())
	if err != nil {
		return BroadcastResult{}, err
	}
	result, err := s.store.BroadcastInsert(ctx, notice, s.broadcastRecipientLimit)
	if err != nil {
		return BroadcastResult{}, err
	}
	if result.Capped {
		return BroadcastResult{}, ErrRecipientLimitExceeded
	}
	return BroadcastResult{TenantID: notice.TenantID, Inserted: result.Inserted}, nil
}

func (s *Service) ListForUser(ctx context.Context, in ListInput) ([]Notification, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.UserID <= 0 {
		return nil, ErrInvalidInput
	}
	if err := normalizePage(&in.Limit, &in.Offset); err != nil {
		return nil, err
	}
	return s.store.ListForUser(ctx, in)
}

func (s *Service) MarkRead(ctx context.Context, in MarkReadInput) (Notification, error) {
	if s == nil || s.store == nil {
		return Notification{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.UserID <= 0 || in.ID <= 0 {
		return Notification{}, ErrInvalidInput
	}
	return s.store.MarkRead(ctx, in.TenantID, in.UserID, in.ID, s.now().UTC())
}

func (s *Service) UnreadCount(ctx context.Context, tenantID, userID int64) (int64, error) {
	if s == nil || s.store == nil {
		return 0, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return 0, ErrInvalidInput
	}
	return s.store.UnreadCount(ctx, tenantID, userID)
}

func normalizeBroadcastInput(in BroadcastInput, now time.Time) (Notification, error) {
	notice := Notification{
		TenantID:       in.TenantID,
		Title:          strings.TrimSpace(in.Title),
		Body:           strings.TrimSpace(in.Body),
		Severity:       in.Severity,
		CreatedByAdmin: int64Ptr(in.CreatedByAdmin),
		CreatedAt:      now,
	}
	if notice.Severity == "" {
		notice.Severity = SeverityInfo
	}
	if notice.TenantID <= 0 || notice.Title == "" || notice.Body == "" {
		return Notification{}, ErrInvalidInput
	}
	switch notice.Severity {
	case SeverityInfo, SeverityWarning, SeverityCritical:
	default:
		return Notification{}, ErrInvalidInput
	}
	if notice.CreatedAt.IsZero() {
		return Notification{}, ErrInvalidInput
	}
	return notice, nil
}

func normalizePage(limit, offset *int) error {
	if *limit == 0 {
		*limit = defaultListLimit
	}
	if *limit < 0 || *limit > maxListLimit || *offset < 0 {
		return ErrInvalidInput
	}
	return nil
}

func utcTimePtr(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	t := in.UTC()
	return &t
}

func int64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}
