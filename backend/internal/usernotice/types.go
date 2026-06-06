package usernotice

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

type Notification struct {
	ID             int64
	TenantID       int64
	UserID         int64
	Title          string
	Body           string
	Severity       Severity
	ReadAt         *time.Time
	CreatedByAdmin *int64
	CreatedAt      time.Time
}

type BroadcastInput struct {
	TenantID       int64
	Title          string
	Body           string
	Severity       Severity
	CreatedByAdmin *int64
}

type BroadcastResult struct {
	TenantID int64
	Inserted int64
}

type ListInput struct {
	TenantID   int64
	UserID     int64
	UnreadOnly bool
	Limit      int
	Offset     int
}

type MarkReadInput struct {
	TenantID int64
	UserID   int64
	ID       int64
}

type Store interface {
	BroadcastInsert(context.Context, Notification, int) (broadcastStoreResult, error)
	ListForUser(context.Context, ListInput) ([]Notification, error)
	MarkRead(context.Context, int64, int64, int64, time.Time) (Notification, error)
	UnreadCount(context.Context, int64, int64) (int64, error)
}

type broadcastStoreResult struct {
	Inserted int64
	Capped   bool
}

var (
	ErrInvalidInput           = errors.New("usernotice: invalid input")
	ErrNotFound               = errors.New("usernotice: not found")
	ErrStoreNotConfigured     = errors.New("usernotice: store not configured")
	ErrRecipientLimitExceeded = errors.New("usernotice: recipient limit exceeded")
)
