package platformsettings

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultCacheTTL = 30 * time.Second

type Service struct {
	store    Store
	audit    AuditSink
	cache    sync.Map
	cacheTTL time.Duration
	now      func() time.Time
}

type Option func(*Service)

func NewService(store Store, audit AuditSink, opts ...Option) *Service {
	s := &Service{
		store:    store,
		audit:    audit,
		cacheTTL: defaultCacheTTL,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithCacheTTL(ttl time.Duration) Option {
	return func(s *Service) {
		if ttl > 0 {
			s.cacheTTL = ttl
		}
	}
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func (s *Service) Get(ctx context.Context, key SettingKey) (StoredSetting, error) {
	if s == nil || s.store == nil {
		return StoredSetting{}, ErrStoreNotConfigured
	}
	if !IsAllowedKey(key) {
		return StoredSetting{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
	}
	if cached, ok := s.getCached(key); ok {
		return cached, nil
	}
	setting, err := s.readFresh(ctx, key)
	if err != nil {
		return StoredSetting{}, err
	}
	s.cacheSetting(setting)
	return setting, nil
}

func (s *Service) List(ctx context.Context) ([]StoredSetting, error) {
	if s == nil || s.store == nil {
		return nil, ErrStoreNotConfigured
	}
	rows, err := s.store.List(ctx, GlobalScope)
	if err != nil {
		return nil, err
	}
	byKey := make(map[SettingKey]StoredSetting, len(rows))
	for _, row := range rows {
		setting, err := normalizeStoredSetting(row, SourceDB)
		if err != nil {
			return nil, err
		}
		byKey[setting.Key] = setting
	}
	out := make([]StoredSetting, 0, len(orderedSettingKeys))
	for _, key := range orderedSettingKeys {
		if row, ok := byKey[key]; ok {
			out = append(out, row)
		} else {
			out = append(out, defaultSetting(key))
		}
	}
	return out, nil
}

func (s *Service) Upsert(ctx context.Context, in UpsertInput) (StoredSetting, error) {
	if s == nil || s.store == nil {
		return StoredSetting{}, ErrStoreNotConfigured
	}
	key, err := ParseKey(string(in.Key))
	if err != nil {
		return StoredSetting{}, err
	}
	value, err := ValidateValue(key, in.Value)
	if err != nil {
		return StoredSetting{}, err
	}
	updatedBy := firstNonEmpty(in.UpdatedBy, in.ActorID, "system")
	in.Key = key
	in.Value = value
	in.UpdatedBy = updatedBy
	if atomic, ok := s.store.(AtomicStore); ok && s.audit == nil {
		updated, err := atomic.UpsertWithAudit(ctx, in)
		if err != nil {
			return StoredSetting{}, err
		}
		updated, err = normalizeStoredSetting(updated, SourceDB)
		if err != nil {
			return StoredSetting{}, err
		}
		s.cacheSetting(updated)
		return updated, nil
	}
	oldSetting, err := s.readFresh(ctx, key)
	if err != nil {
		return StoredSetting{}, err
	}
	updated, err := s.store.Upsert(ctx, GlobalScope, string(key), value, updatedBy)
	if err != nil {
		return StoredSetting{}, err
	}
	updated, err = normalizeStoredSetting(updated, SourceDB)
	if err != nil {
		return StoredSetting{}, err
	}
	if s.audit != nil {
		if err := s.audit.WriteAdminAudit(ctx, auditParamsFromInput(in, oldSetting, updated)); err != nil {
			s.cache.Delete(key)
			return StoredSetting{}, err
		}
	}
	s.cacheSetting(updated)
	return updated, nil
}

type cachedSetting struct {
	value     StoredSetting
	expiresAt time.Time
}

func (s *Service) getCached(key SettingKey) (StoredSetting, bool) {
	raw, ok := s.cache.Load(key)
	if !ok {
		return StoredSetting{}, false
	}
	entry, ok := raw.(cachedSetting)
	if !ok || !s.now().Before(entry.expiresAt) {
		s.cache.Delete(key)
		return StoredSetting{}, false
	}
	return entry.value, true
}

func (s *Service) cacheSetting(setting StoredSetting) {
	s.cache.Store(setting.Key, cachedSetting{
		value:     setting,
		expiresAt: s.now().Add(s.cacheTTL),
	})
}

func (s *Service) readFresh(ctx context.Context, key SettingKey) (StoredSetting, error) {
	row, found, err := s.store.Get(ctx, GlobalScope, string(key))
	if err != nil {
		return StoredSetting{}, err
	}
	if !found {
		return defaultSetting(key), nil
	}
	return normalizeStoredSetting(row, SourceDB)
}

func defaultSetting(key SettingKey) StoredSetting {
	value, _ := DefaultValue(key)
	return StoredSetting{Scope: GlobalScope, Key: key, Value: value, Source: SourceDefault}
}

func normalizeStoredSetting(row StoredSetting, source string) (StoredSetting, error) {
	key, err := ParseKey(string(row.Key))
	if err != nil {
		return StoredSetting{}, err
	}
	value, err := ValidateValue(key, row.Value)
	if err != nil {
		return StoredSetting{}, err
	}
	row.Scope = GlobalScope
	row.Key = key
	row.Value = value
	row.Source = source
	return row, nil
}

func auditParamsFromInput(in UpsertInput, oldSetting, updated StoredSetting) AuditParams {
	actorID := firstNonEmpty(in.ActorID, in.UpdatedBy, "system")
	return AuditParams{
		ActorID:   actorID,
		ActorRole: strings.TrimSpace(in.ActorRole),
		Key:       updated.Key,
		OldValue:  oldSetting.Value,
		OldSource: oldSetting.Source,
		NewValue:  updated.Value,
		Reason:    strings.TrimSpace(in.Reason),
		RequestID: strings.TrimSpace(in.RequestID),
		TargetID:  updated.ID,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
