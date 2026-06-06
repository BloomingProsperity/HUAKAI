package announcement

import (
	"context"
	"strings"
	"time"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
)

type Service struct {
	store Store
	now   func() time.Time
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
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Announcement, error) {
	if s == nil || s.store == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	now := s.now().UTC()
	ann, err := normalizeCreateInput(in, now)
	if err != nil {
		return Announcement{}, err
	}
	return s.store.Create(ctx, ann)
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (Announcement, error) {
	if s == nil || s.store == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.ID <= 0 {
		return Announcement{}, ErrInvalidInput
	}
	current, err := s.store.Get(ctx, in.TenantID, in.ID)
	if err != nil {
		return Announcement{}, err
	}
	if in.Title != nil {
		current.Title = strings.TrimSpace(*in.Title)
	}
	if in.Body != nil {
		current.Body = strings.TrimSpace(*in.Body)
	}
	if in.Severity != nil {
		current.Severity = *in.Severity
	}
	if in.Active != nil {
		current.Active = *in.Active
	}
	if in.PublishedAt != nil {
		current.PublishedAt = in.PublishedAt.UTC()
	}
	if in.ExpiresAtSet {
		current.ExpiresAt = utcTimePtr(in.ExpiresAt)
	}
	if err := validateAnnouncement(current); err != nil {
		return Announcement{}, err
	}
	return s.store.Update(ctx, current)
}

func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	return s.store.Delete(ctx, tenantID, id)
}

func (s *Service) Get(ctx context.Context, tenantID, id int64) (Announcement, error) {
	if s == nil || s.store == nil {
		return Announcement{}, ErrStoreNotConfigured
	}
	if tenantID <= 0 || id <= 0 {
		return Announcement{}, ErrInvalidInput
	}
	return s.store.Get(ctx, tenantID, id)
}

func (s *Service) ListActive(ctx context.Context, in ListActiveInput) ([]Announcement, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if in.Now.IsZero() {
		in.Now = s.now().UTC()
	} else {
		in.Now = in.Now.UTC()
	}
	if err := normalizePage(&in.Limit, &in.Offset); err != nil {
		return nil, err
	}
	return s.store.ListActive(ctx, in)
}

func (s *Service) ListAllAdmin(ctx context.Context, in ListAdminInput) ([]Announcement, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 {
		return nil, ErrInvalidInput
	}
	if err := normalizePage(&in.Limit, &in.Offset); err != nil {
		return nil, err
	}
	return s.store.ListAllAdmin(ctx, in)
}

func normalizeCreateInput(in CreateInput, now time.Time) (Announcement, error) {
	ann := Announcement{
		TenantID:       in.TenantID,
		Title:          strings.TrimSpace(in.Title),
		Body:           strings.TrimSpace(in.Body),
		Severity:       in.Severity,
		Active:         true,
		PublishedAt:    now,
		ExpiresAt:      utcTimePtr(in.ExpiresAt),
		CreatedByAdmin: int64Ptr(in.CreatedByAdmin),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if ann.Severity == "" {
		ann.Severity = SeverityInfo
	}
	if in.Active != nil {
		ann.Active = *in.Active
	}
	if in.PublishedAt != nil {
		ann.PublishedAt = in.PublishedAt.UTC()
	}
	if err := validateAnnouncement(ann); err != nil {
		return Announcement{}, err
	}
	return ann, nil
}

func validateAnnouncement(ann Announcement) error {
	if ann.TenantID <= 0 || strings.TrimSpace(ann.Title) == "" || strings.TrimSpace(ann.Body) == "" {
		return ErrInvalidInput
	}
	switch ann.Severity {
	case SeverityInfo, SeverityWarning, SeverityCritical:
	default:
		return ErrInvalidInput
	}
	if ann.PublishedAt.IsZero() {
		return ErrInvalidInput
	}
	if ann.ExpiresAt != nil && !ann.ExpiresAt.After(ann.PublishedAt) {
		return ErrInvalidInput
	}
	return nil
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
