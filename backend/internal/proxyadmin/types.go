package proxyadmin

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput  = errors.New("proxyadmin: invalid input")
	ErrInvalidStatus = errors.New("proxyadmin: invalid status")
	ErrBackend       = errors.New("proxyadmin: backend failure")
)

type Proxy struct {
	ID           int64
	TenantID     int64
	Name         string
	Protocol     string
	Host         string
	Port         int32
	AuthUsername *string
	Status       string
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
