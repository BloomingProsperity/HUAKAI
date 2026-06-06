package announcement

import (
	"context"
	"errors"
	"time"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Announcement struct {
	ID             int64
	TenantID       int64
	Title          string
	Body           string
	Severity       Severity
	Active         bool
	PublishedAt    time.Time
	ExpiresAt      *time.Time
	CreatedByAdmin *int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateInput struct {
	TenantID       int64
	Title          string
	Body           string
	Severity       Severity
	Active         *bool
	PublishedAt    *time.Time
	ExpiresAt      *time.Time
	CreatedByAdmin *int64
}

type UpdateInput struct {
	TenantID     int64
	ID           int64
	Title        *string
	Body         *string
	Severity     *Severity
	Active       *bool
	PublishedAt  *time.Time
	ExpiresAt    *time.Time
	ExpiresAtSet bool
}

type ListActiveInput struct {
	TenantID int64
	Now      time.Time
	Limit    int
	Offset   int
}

type ListAdminInput struct {
	TenantID int64
	Limit    int
	Offset   int
}

type Store interface {
	Create(context.Context, Announcement) (Announcement, error)
	Update(context.Context, Announcement) (Announcement, error)
	Delete(context.Context, int64, int64) error
	Get(context.Context, int64, int64) (Announcement, error)
	ListActive(context.Context, ListActiveInput) ([]Announcement, error)
	ListAllAdmin(context.Context, ListAdminInput) ([]Announcement, error)
}

var (
	ErrInvalidInput       = errors.New("announcement: invalid input")
	ErrNotFound           = errors.New("announcement: not found")
	ErrStoreNotConfigured = errors.New("announcement: store not configured")
)
