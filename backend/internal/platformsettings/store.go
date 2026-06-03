package platformsettings

import (
	"context"
	"time"
)

type StoredSetting struct {
	ID        int64
	Scope     string
	Key       SettingKey
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
	Source    string
}

type Store interface {
	Get(ctx context.Context, scope, key string) (StoredSetting, bool, error)
	List(ctx context.Context, scope string) ([]StoredSetting, error)
	Upsert(ctx context.Context, scope, key, value, updatedBy string) (StoredSetting, error)
}

type AtomicStore interface {
	Store
	UpsertWithAudit(ctx context.Context, input UpsertInput) (StoredSetting, error)
}

type UpsertInput struct {
	Key       SettingKey
	Value     string
	UpdatedBy string
	ActorID   string
	ActorRole string
	Reason    string
	RequestID string
}

type AuditParams struct {
	ActorID   string
	ActorRole string
	Key       SettingKey
	OldValue  string
	OldSource string
	NewValue  string
	Reason    string
	RequestID string
	TargetID  int64
}

type AuditSink interface {
	WriteAdminAudit(ctx context.Context, params AuditParams) error
}
