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
	store        Store
	audit        AuditSink
	cache        sync.Map
	lastKnown    sync.Map
	cacheTTL     time.Duration
	now          func() time.Time
	secretCipher SecretCipher
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
		return s.lastKnownOrDefault(key), nil
	}
	s.cacheSetting(setting)
	s.rememberLastKnown(setting)
	return setting, nil
}

// GetAuthoritative 读取可用于资金、权限等关键决策的设置。有效期内的缓存仍可使用；
// 缓存未命中时必须从存储确认，存储故障不会降级到历史值或默认值。
func (s *Service) GetAuthoritative(ctx context.Context, key SettingKey) (StoredSetting, error) {
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
	s.rememberLastKnown(setting)
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
	if err := s.validateSettingCombination(ctx, key, value); err != nil {
		return StoredSetting{}, err
	}
	updatedBy := firstNonEmpty(in.UpdatedBy, in.ActorID, "system")
	in.Key = key
	// secret key 值加密后落库(at-rest);value/in.Value 同步为密文,两条 upsert 路径都存密文。
	value, err = s.encryptSecretValue(ctx, key, value)
	if err != nil {
		return StoredSetting{}, err
	}
	in.Value = value
	in.UpdatedBy = updatedBy
	if atomic, ok := s.store.(AtomicStore); ok && s.audit == nil {
		updated, err := atomic.UpsertWithAudit(ctx, in)
		if err != nil {
			return StoredSetting{}, err
		}
		// 先解密(updated 来自库为密文)再 normalize:normalize 内的 ValidateValue 须对明文跑;
		// 缓存的也是明文,与 Get/readFresh 一致。
		if updated.Value, err = s.decryptSecretValue(ctx, key, updated.Value); err != nil {
			return StoredSetting{}, err
		}
		updated, err = normalizeStoredSetting(updated, SourceDB)
		if err != nil {
			return StoredSetting{}, err
		}
		s.cacheSetting(updated)
		s.rememberLastKnown(updated)
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
	// 先解密(库里为密文)再 normalize(ValidateValue 须对明文跑);审计对 secret key 另有脱敏、缓存存明文。
	if updated.Value, err = s.decryptSecretValue(ctx, key, updated.Value); err != nil {
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
	s.rememberLastKnown(updated)
	return updated, nil
}

func (s *Service) validateSettingCombination(ctx context.Context, key SettingKey, value string) error {
	switch key {
	case KeyEmailDomainAllowlistEnabled:
		if value != "true" {
			return nil
		}
		allowlist, err := s.readFresh(ctx, KeyEmailDomainAllowlist)
		if err != nil {
			return err
		}
		if !csvSettingHasItems(allowlist.Value) {
			return fmt.Errorf("%w: %s requires non-empty %s", ErrInvalidValue, key, KeyEmailDomainAllowlist)
		}
	case KeyEmailDomainAllowlist:
		if csvSettingHasItems(value) {
			return nil
		}
		enabled, err := s.readFresh(ctx, KeyEmailDomainAllowlistEnabled)
		if err != nil {
			return err
		}
		if enabled.Value == "true" {
			return fmt.Errorf("%w: %s cannot be empty while %s is true", ErrInvalidValue, key, KeyEmailDomainAllowlistEnabled)
		}
	}
	return nil
}

func csvSettingHasItems(value string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.TrimSpace(part) != "" {
			return true
		}
	}
	return false
}

func (s *Service) RefreshAll(ctx context.Context) error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	for _, key := range orderedSettingKeys {
		setting, err := s.readFresh(ctx, key)
		if err != nil {
			continue
		}
		s.rememberLastKnown(setting)
	}
	return nil
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

func (s *Service) rememberLastKnown(setting StoredSetting) {
	s.lastKnown.Store(setting.Key, setting)
}

func (s *Service) lastKnownOrDefault(key SettingKey) StoredSetting {
	raw, ok := s.lastKnown.Load(key)
	if !ok {
		return defaultSetting(key)
	}
	setting, ok := raw.(StoredSetting)
	if !ok {
		s.lastKnown.Delete(key)
		return defaultSetting(key)
	}
	return setting
}

func (s *Service) readFresh(ctx context.Context, key SettingKey) (StoredSetting, error) {
	row, found, err := s.store.Get(ctx, GlobalScope, string(key))
	if err != nil {
		return StoredSetting{}, err
	}
	if !found {
		return defaultSetting(key), nil
	}
	// 先解密再 normalize:secret 值在库里是密文(带前缀),而 normalizeStoredSetting 会对值跑 ValidateValue
	// (secret key 如审核 keys 要求是 JSON 数组),故必须先解回明文再校验/规整。存量明文(无前缀)原样返回。
	row.Value, err = s.decryptSecretValue(ctx, key, row.Value)
	if err != nil {
		return StoredSetting{}, err
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
	// 带版本前缀的 secret 值是 at-rest 密文,值校验只能对明文跑:JSON 结构的 secret(审核 keys /
	// OAuth secrets)按密文解析必挂,会让原子写路径存不进、List 遇密文行整页报错。明文在加密前
	// 已校验、解密读路径还会再 normalize,密文这里只归一元数据。
	if IsSecretKey(key) && strings.HasPrefix(row.Value, secretEncPrefix) {
		row.Scope = GlobalScope
		row.Key = key
		row.Source = source
		return row, nil
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
