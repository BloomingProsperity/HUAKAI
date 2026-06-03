package platformsettings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceGetAbsentKeyReturnsFailClosedDefault(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, WithNow(fixedNow))

	got, err := svc.Get(context.Background(), KeyRegistrationEnabled)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Key != KeyRegistrationEnabled || got.Value != "false" || got.Source != SourceDefault {
		t.Fatalf("setting=%+v want registration_enabled false/default", got)
	}
	if !got.UpdatedAt.IsZero() || got.UpdatedBy != "" {
		t.Fatalf("default metadata updated_at=%v updated_by=%q want empty", got.UpdatedAt, got.UpdatedBy)
	}
}

func TestTwoFactorSettingDefaultsOnAndValidatesBool(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, nil, WithNow(fixedNow))

	got, err := svc.Get(context.Background(), KeyTwoFactorEnabled)
	if err != nil {
		t.Fatalf("Get two_factor_enabled: %v", err)
	}
	if got.Value != "true" || got.Source != SourceDefault {
		t.Fatalf("two_factor_enabled default=%+v want true/default", got)
	}
	updated, err := svc.Upsert(context.Background(), UpsertInput{
		Key: KeyTwoFactorEnabled, Value: "false", UpdatedBy: "test",
	})
	if err != nil {
		t.Fatalf("Upsert false: %v", err)
	}
	if updated.Value != "false" || updated.Source != SourceDB {
		t.Fatalf("two_factor_enabled override=%+v want false/db", updated)
	}
	if _, err := ValidateValue(KeyTwoFactorEnabled, "yes"); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("ValidateValue yes err=%v want ErrInvalidValue", err)
	}
}

func TestServiceGetPresentKeyUsesDBAndCache(t *testing.T) {
	base := NewMemoryStore()
	if _, err := base.Upsert(context.Background(), GlobalScope, string(KeyPromoEnabled), "true", "seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store := &countingStore{Store: base}
	svc := NewService(store, nil, WithCacheTTL(time.Minute), WithNow(fixedNow))

	first, err := svc.Get(context.Background(), KeyPromoEnabled)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := svc.Get(context.Background(), KeyPromoEnabled)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if first.Value != "true" || first.Source != SourceDB {
		t.Fatalf("first setting=%+v want db true", first)
	}
	if second.Value != "true" || second.Source != SourceDB {
		t.Fatalf("second setting=%+v want cached db true", second)
	}
	if store.getCalls != 1 {
		t.Fatalf("store Get calls=%d want 1 cache hit on second read", store.getCalls)
	}
}

func TestServiceListIncludesAbsentKeysWithDefaults(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Upsert(context.Background(), GlobalScope, string(KeyPromoEnabled), "true", "seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := NewService(store, nil, WithNow(fixedNow))

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byKey := map[SettingKey]StoredSetting{}
	for _, item := range got {
		byKey[item.Key] = item
	}
	if len(byKey) != len(AllKeys()) {
		t.Fatalf("list returned %d unique keys want %d: %+v", len(byKey), len(AllKeys()), got)
	}
	if byKey[KeyPromoEnabled].Value != "true" || byKey[KeyPromoEnabled].Source != SourceDB {
		t.Fatalf("promo setting=%+v want db true", byKey[KeyPromoEnabled])
	}
	if byKey[KeyRegistrationEnabled].Value != "false" || byKey[KeyRegistrationEnabled].Source != SourceDefault {
		t.Fatalf("registration setting=%+v want default false", byKey[KeyRegistrationEnabled])
	}
}

func TestServiceUpsertRejectsUnknownKeyBeforeStore(t *testing.T) {
	store := &countingStore{Store: NewMemoryStore()}
	audit := &auditSpy{}
	svc := NewService(store, audit, WithNow(fixedNow))

	_, err := svc.Upsert(context.Background(), UpsertInput{
		Key:       SettingKey("smtp_password"),
		Value:     "do-not-store",
		UpdatedBy: "admin:1",
		ActorID:   "admin:1",
		ActorRole: "platform_admin",
	})
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err=%v want ErrUnknownKey", err)
	}
	if store.upsertCalls != 0 || audit.calls != 0 {
		t.Fatalf("unknown key touched store/audit: upserts=%d audits=%d", store.upsertCalls, audit.calls)
	}
}

func TestServiceUpsertRejectsInvalidValuesBeforeStore(t *testing.T) {
	cases := []struct {
		name  string
		key   SettingKey
		value string
	}{
		{name: "empty", key: KeyPromoEnabled, value: "  "},
		{name: "invalid bool", key: KeyRegistrationEnabled, value: "yes"},
		{name: "non-positive int", key: KeyStreamTimeoutSeconds, value: "0"},
		{name: "unsupported captcha provider", key: KeyCaptchaProvider, value: "hcaptcha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &countingStore{Store: NewMemoryStore()}
			svc := NewService(store, nil, WithNow(fixedNow))

			_, err := svc.Upsert(context.Background(), UpsertInput{
				Key:       tc.key,
				Value:     tc.value,
				UpdatedBy: "admin:1",
				ActorID:   "admin:1",
				ActorRole: "platform_admin",
			})
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("err=%v want ErrInvalidValue", err)
			}
			if store.upsertCalls != 0 {
				t.Fatalf("invalid value reached store upsert %d times", store.upsertCalls)
			}
		})
	}
}

func TestResponseHeaderFirewallSettingsAllowEmptyAndValidateHeaderLists(t *testing.T) {
	for _, key := range []SettingKey{KeyResponseHeaderDenyExtra, KeyResponseHeaderAllowOverride} {
		t.Run(string(key)+"/default", func(t *testing.T) {
			value, ok := DefaultValue(key)
			if !ok || value != "" {
				t.Fatalf("default value=%q ok=%v want empty default", value, ok)
			}
			got, err := ValidateValue(key, "")
			if err != nil {
				t.Fatalf("ValidateValue empty: %v", err)
			}
			if got != "" {
				t.Fatalf("empty setting normalized to %q want empty", got)
			}
		})
		t.Run(string(key)+"/valid", func(t *testing.T) {
			got, err := ValidateValue(key, "X-Internal-,Server")
			if err != nil {
				t.Fatalf("ValidateValue valid list: %v", err)
			}
			if got != "X-Internal-,Server" {
				t.Fatalf("normalized=%q want original comma list", got)
			}
		})
	}
}

func TestResponseHeaderFirewallSettingsRejectMalformedHeaderLists(t *testing.T) {
	longHeader := "X-" + strings.Repeat("A", 63)
	tooMany := strings.Repeat("X-A,", 20) + "X-A"
	cases := []struct {
		name  string
		value string
	}{
		{name: "space", value: "X-Internal, X-Debug"},
		{name: "empty item", value: "X-Internal,"},
		{name: "slash", value: "X/Internal"},
		{name: "too long", value: longHeader},
		{name: "too many", value: tooMany},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &countingStore{Store: NewMemoryStore()}
			svc := NewService(store, nil, WithNow(fixedNow))

			_, err := svc.Upsert(context.Background(), UpsertInput{
				Key:       KeyResponseHeaderDenyExtra,
				Value:     tc.value,
				UpdatedBy: "admin:1",
				ActorID:   "admin:1",
				ActorRole: "platform_admin",
			})
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("err=%v want ErrInvalidValue", err)
			}
			if store.upsertCalls != 0 {
				t.Fatalf("invalid response header list reached store upsert %d times", store.upsertCalls)
			}
		})
	}
}

func TestServiceUpsertCallsAuditWithOldValue(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Upsert(context.Background(), GlobalScope, string(KeyPromoEnabled), "false", "seed"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	audit := &auditSpy{}
	svc := NewService(store, audit, WithNow(fixedNow))

	got, err := svc.Upsert(context.Background(), UpsertInput{
		Key:       KeyPromoEnabled,
		Value:     "true",
		UpdatedBy: "admin:11",
		ActorID:   "admin:11",
		ActorRole: "platform_admin",
		Reason:    "rollout",
		RequestID: "req-123",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Value != "true" || got.Source != SourceDB || got.UpdatedBy != "admin:11" {
		t.Fatalf("updated setting=%+v want db true/admin:11", got)
	}
	if audit.calls != 1 {
		t.Fatalf("audit calls=%d want 1", audit.calls)
	}
	record := audit.last
	if record.Key != KeyPromoEnabled || record.OldValue != "false" || record.OldSource != SourceDB ||
		record.NewValue != "true" || record.ActorID != "admin:11" || record.ActorRole != "platform_admin" ||
		record.Reason != "rollout" || record.RequestID != "req-123" {
		t.Fatalf("audit record=%+v", record)
	}
}

func TestServiceUpsertUsesAtomicStoreWhenNoAuditSink(t *testing.T) {
	store := &atomicCountingStore{countingStore: &countingStore{Store: NewMemoryStore()}}
	svc := NewService(store, nil, WithNow(fixedNow))

	got, err := svc.Upsert(context.Background(), UpsertInput{
		Key:       KeyPromoEnabled,
		Value:     "true",
		UpdatedBy: "admin:11",
		ActorID:   "admin:11",
		ActorRole: "platform_admin",
	})

	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Value != "true" || got.Source != SourceDB {
		t.Fatalf("updated=%+v want db true", got)
	}
	if store.atomicCalls != 1 {
		t.Fatalf("atomic calls=%d want 1", store.atomicCalls)
	}
	if store.getCalls != 0 || store.upsertCalls != 0 {
		t.Fatalf("non-atomic path used get=%d upsert=%d", store.getCalls, store.upsertCalls)
	}
}

func TestAdminAuditPayloadOmitsCredentialShapedFields(t *testing.T) {
	payload, err := platformSettingAuditPayload(AuditParams{
		Key:       KeyCaptchaSiteKey,
		OldValue:  "old-public-site-key",
		OldSource: SourceDB,
		NewValue:  "new-public-site-key",
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("payload json: %v bytes=%s", err, string(payload))
	}
	if AdminAuditActionUpsert != "update_platform_settings" {
		t.Fatalf("audit action=%q must match migration 0077 allow-list", AdminAuditActionUpsert)
	}
	forbidden := []string{"setting_value", "secret", "password", "key_hash"}
	for _, key := range forbidden {
		if _, ok := decoded[key]; ok {
			t.Fatalf("payload contains forbidden field %q: %v", key, decoded)
		}
	}
}

func TestServiceNilReturnsStoreNotConfigured(t *testing.T) {
	var svc *Service
	if _, err := svc.Get(context.Background(), KeyPromoEnabled); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil Get err=%v want ErrStoreNotConfigured", err)
	}
	if _, err := svc.List(context.Background()); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil List err=%v want ErrStoreNotConfigured", err)
	}
	if _, err := svc.Upsert(context.Background(), UpsertInput{Key: KeyPromoEnabled, Value: "true"}); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil Upsert err=%v want ErrStoreNotConfigured", err)
	}
}

func TestPostgresStoreNilPoolReturnsStoreNotConfigured(t *testing.T) {
	store := NewPostgresStore(nil)
	if _, _, err := store.Get(context.Background(), GlobalScope, string(KeyPromoEnabled)); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil-pool Get err=%v want ErrStoreNotConfigured", err)
	}
	if _, err := store.List(context.Background(), GlobalScope); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil-pool List err=%v want ErrStoreNotConfigured", err)
	}
	if _, err := store.Upsert(context.Background(), GlobalScope, string(KeyPromoEnabled), "true", "admin:1"); !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil-pool Upsert err=%v want ErrStoreNotConfigured", err)
	}
}

type countingStore struct {
	Store
	getCalls    int
	listCalls   int
	upsertCalls int
}

func (s *countingStore) Get(ctx context.Context, scope, key string) (StoredSetting, bool, error) {
	s.getCalls++
	return s.Store.Get(ctx, scope, key)
}

func (s *countingStore) List(ctx context.Context, scope string) ([]StoredSetting, error) {
	s.listCalls++
	return s.Store.List(ctx, scope)
}

func (s *countingStore) Upsert(ctx context.Context, scope, key, value, updatedBy string) (StoredSetting, error) {
	s.upsertCalls++
	return s.Store.Upsert(ctx, scope, key, value, updatedBy)
}

type atomicCountingStore struct {
	*countingStore
	atomicCalls int
}

func (s *atomicCountingStore) UpsertWithAudit(ctx context.Context, in UpsertInput) (StoredSetting, error) {
	s.atomicCalls++
	return s.countingStore.Store.Upsert(ctx, GlobalScope, string(in.Key), in.Value, in.UpdatedBy)
}

type auditSpy struct {
	calls int
	last  AuditParams
	err   error
}

func (s *auditSpy) WriteAdminAudit(_ context.Context, params AuditParams) error {
	s.calls++
	s.last = params
	return s.err
}

func fixedNow() time.Time {
	return time.Date(2026, 6, 3, 4, 5, 6, 0, time.UTC)
}
