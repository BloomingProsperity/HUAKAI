package proxyadmin

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput  = errors.New("proxyadmin: invalid input")
	ErrInvalidStatus = errors.New("proxyadmin: invalid status")
	ErrBackend       = errors.New("proxyadmin: backend failure")
	ErrNotFound      = errors.New("proxyadmin: proxy not found")
)

// Proxy is the secret-free projection of a proxy row. It deliberately omits
// auth_secret: the encrypted credential is write-only and is never returned by
// any read path (list/get), so a proxy secret cannot leak through this surface.
type Proxy struct {
	ID           int64
	TenantID     int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	Status       string
	LastCheckAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type CreateInput struct {
	TenantID     int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	AuthSecret   *string
	Status       string
}

type UpdateInput struct {
	TenantID     int64
	ID           int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	AuthSecret   *string
}
