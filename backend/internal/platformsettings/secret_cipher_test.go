package platformsettings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbplatformsettings "github.com/BloomingProsperity/HUAKAI/internal/db/platformsettings"
)

// fakeSecretCipher 是可逆的测试用密码器:密文 = "C(" + aad + ")" + plaintext,解密时校验 aad 并剥壳。
// 它足以验证「加密后落库、读出解密、aad 绑定 key」的往返正确性(不依赖真 AES)。
type fakeSecretCipher struct{}

func reverseString(s string) string {
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func (fakeSecretCipher) EncryptString(_ context.Context, plaintext, aad string) (string, error) {
	// 反转明文以隐藏子串(模拟真加密的「密文不含明文」性质),使「无明文泄露」断言有意义。
	return "C(" + aad + ")" + reverseString(plaintext), nil
}
func (fakeSecretCipher) DecryptString(_ context.Context, ciphertext, aad string) (string, error) {
	prefix := "C(" + aad + ")"
	if !strings.HasPrefix(ciphertext, prefix) {
		return "", ErrInvalidValue // aad 不匹配(密文被搬到别的 key)→ 拒绝
	}
	return reverseString(strings.TrimPrefix(ciphertext, prefix)), nil
}

// TestSecretSettingEncryptedAtRest 锁定 secret 设置的 at-rest 加密往返:
// ① Upsert 后库里存的是带前缀的密文(非明文);② Get 读出解密回明文(server 消费方拿明文);
// ③ 非 secret key 不受影响(明文进出);④ aad 绑定 key(密文含 key)。
// 变异:Upsert 不加密(encryptSecretValue 直接返回 value)→ 库里存明文,断言①(库存密文)RED。
func TestSecretSettingEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, nil, WithSecretCipher(fakeSecretCipher{}))

	// moderation_external_api_keys 是既有 secret key(JSON 字符串数组)。
	const secretKey = KeyModerationExternalAPIKeys
	const plaintext = `["sk-super-secret"]`
	if _, err := svc.Upsert(ctx, UpsertInput{Key: secretKey, Value: plaintext, UpdatedBy: "t"}); err != nil {
		t.Fatalf("upsert secret: %v", err)
	}

	// ① 库里(绕过 service)存的必须是带前缀的密文,不是明文。
	raw, found, err := store.Get(ctx, GlobalScope, string(secretKey))
	if err != nil || !found {
		t.Fatalf("store.Get: found=%v err=%v", found, err)
	}
	if !strings.HasPrefix(raw.Value, secretEncPrefix) {
		t.Fatalf("库里 secret 应为带前缀密文,实为 %q", raw.Value)
	}
	if strings.Contains(raw.Value, "sk-super-secret") {
		t.Fatalf("库里 secret 明文泄露:%q", raw.Value)
	}
	// ② 密文里应含 aad(=key),证明绑定。
	if !strings.Contains(raw.Value, string(secretKey)) {
		t.Fatalf("密文应绑定 key(aad),实为 %q", raw.Value)
	}

	// ③ Get 读出解密回明文(server 消费方语义)。
	got, err := svc.Get(ctx, secretKey)
	if err != nil {
		t.Fatalf("svc.Get: %v", err)
	}
	if got.Value != plaintext {
		t.Fatalf("Get 应解密回明文 %q,实得 %q", plaintext, got.Value)
	}

	// ④ 非 secret key 不加密(明文进出)。
	if _, err := svc.Upsert(ctx, UpsertInput{Key: KeySiteName, Value: "MySite", UpdatedBy: "t"}); err != nil {
		t.Fatalf("upsert non-secret: %v", err)
	}
	rawName, _, _ := store.Get(ctx, GlobalScope, string(KeySiteName))
	if rawName.Value != "MySite" {
		t.Fatalf("非 secret key 不应加密,库里应为明文 MySite,实得 %q", rawName.Value)
	}
}

// fakeAtomicQueries 以内存 map 模拟 platform_settings 表与审计表,把真实的 auditedUpsert
// (生产原子路径核心)接进无 PG 的单测。
type fakeAtomicQueries struct {
	rows  map[string]dbplatformsettings.PlatformSetting
	audit []admindb.InsertAdminAuditEventParams
}

func atomicRowKey(scope, key string) string { return scope + "\x00" + key }

func (q *fakeAtomicQueries) AcquirePlatformSettingLock(context.Context, dbplatformsettings.AcquirePlatformSettingLockParams) error {
	return nil
}

func (q *fakeAtomicQueries) GetPlatformSettingForUpdate(_ context.Context, arg dbplatformsettings.GetPlatformSettingForUpdateParams) (dbplatformsettings.PlatformSetting, error) {
	row, ok := q.rows[atomicRowKey(arg.Scope, arg.SettingKey)]
	if !ok {
		return dbplatformsettings.PlatformSetting{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeAtomicQueries) GetPlatformSetting(_ context.Context, arg dbplatformsettings.GetPlatformSettingParams) (dbplatformsettings.PlatformSetting, error) {
	row, ok := q.rows[atomicRowKey(arg.Scope, arg.SettingKey)]
	if !ok {
		return dbplatformsettings.PlatformSetting{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *fakeAtomicQueries) ListPlatformSettingsByScope(_ context.Context, scope string) ([]dbplatformsettings.PlatformSetting, error) {
	out := make([]dbplatformsettings.PlatformSetting, 0, len(q.rows))
	for _, row := range q.rows {
		if row.Scope == scope {
			out = append(out, row)
		}
	}
	return out, nil
}

func (q *fakeAtomicQueries) UpsertPlatformSetting(_ context.Context, arg dbplatformsettings.UpsertPlatformSettingParams) (dbplatformsettings.PlatformSetting, error) {
	mapKey := atomicRowKey(arg.Scope, arg.SettingKey)
	row, ok := q.rows[mapKey]
	if !ok {
		row = dbplatformsettings.PlatformSetting{ID: int64(len(q.rows) + 1), Scope: arg.Scope, SettingKey: arg.SettingKey}
	}
	row.SettingValue = arg.SettingValue
	row.UpdatedBy = arg.UpdatedBy
	q.rows[mapKey] = row
	return row, nil
}

func (q *fakeAtomicQueries) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	q.audit = append(q.audit, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(q.audit))}, nil
}

// fakeAtomicSettingsStore 复刻 PostgresStore 的 Store+AtomicStore 拓扑(UpsertWithAudit 走真实
// auditedUpsert),使 Service.Upsert 进入生产用的原子分支。
type fakeAtomicSettingsStore struct {
	q *fakeAtomicQueries
}

func (s *fakeAtomicSettingsStore) Get(ctx context.Context, scope, key string) (StoredSetting, bool, error) {
	row, err := s.q.GetPlatformSetting(ctx, dbplatformsettings.GetPlatformSettingParams{Scope: scope, SettingKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredSetting{}, false, nil
	}
	if err != nil {
		return StoredSetting{}, false, err
	}
	return storedSettingFromDB(row), true, nil
}

func (s *fakeAtomicSettingsStore) List(ctx context.Context, scope string) ([]StoredSetting, error) {
	rows, err := s.q.ListPlatformSettingsByScope(ctx, scope)
	if err != nil {
		return nil, err
	}
	out := make([]StoredSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, storedSettingFromDB(row))
	}
	return out, nil
}

func (s *fakeAtomicSettingsStore) Upsert(ctx context.Context, scope, key, value, updatedBy string) (StoredSetting, error) {
	row, err := s.q.UpsertPlatformSetting(ctx, dbplatformsettings.UpsertPlatformSettingParams{
		Scope: scope, SettingKey: key, SettingValue: value, UpdatedBy: updatedBy,
	})
	if err != nil {
		return StoredSetting{}, err
	}
	return storedSettingFromDB(row), nil
}

func (s *fakeAtomicSettingsStore) UpsertWithAudit(ctx context.Context, in UpsertInput) (StoredSetting, error) {
	return auditedUpsert(ctx, s.q, s.q, in)
}

// TestAtomicUpsertStoresEncryptedStructuredSecret 锁定生产原子路径(AtomicStore + cipher +
// audit=nil,与 wiring 的 PostgresStore 拓扑一致)能存进 JSON 结构的 secret:store 层归一
// (readPreviousSetting / writePlatformSetting)不得对密文再跑值校验。
// 变异:去掉 normalizeStoredSetting 的密文跳过守卫 → ValidateValue 把 "encv1:..." 按 JSON
// 解析必失败,首次写入与覆盖写全报错 → 本测试 RED。
func TestAtomicUpsertStoresEncryptedStructuredSecret(t *testing.T) {
	ctx := context.Background()
	q := &fakeAtomicQueries{rows: map[string]dbplatformsettings.PlatformSetting{}}
	svc := NewService(&fakeAtomicSettingsStore{q: q}, nil, WithSecretCipher(fakeSecretCipher{}))

	const plaintext = `["sk-atomic-secret"]`
	got, err := svc.Upsert(ctx, UpsertInput{Key: KeyModerationExternalAPIKeys, Value: plaintext, UpdatedBy: "t"})
	if err != nil {
		t.Fatalf("原子路径首次写入 secret: %v", err)
	}
	if got.Value != plaintext {
		t.Fatalf("返回值应为解密明文 %q,实得 %q", plaintext, got.Value)
	}
	raw := q.rows[atomicRowKey(GlobalScope, string(KeyModerationExternalAPIKeys))]
	if !strings.HasPrefix(raw.SettingValue, secretEncPrefix) || strings.Contains(raw.SettingValue, "sk-atomic-secret") {
		t.Fatalf("库里应为带前缀密文且不含明文,实为 %q", raw.SettingValue)
	}

	// 覆盖写:readPreviousSetting 归一「已是密文」的旧行,同样不得挂。
	if _, err := svc.Upsert(ctx, UpsertInput{Key: KeyModerationExternalAPIKeys, Value: `["sk-rotated"]`, UpdatedBy: "t"}); err != nil {
		t.Fatalf("原子路径覆盖写 secret(旧行为密文): %v", err)
	}

	// 审计两条,secret 新旧值全 [redacted],密文/明文都不落审计。
	if len(q.audit) != 2 {
		t.Fatalf("应写 2 条审计,实 %d", len(q.audit))
	}
	for i, entry := range q.audit {
		var payload map[string]string
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatalf("审计#%d payload 解析: %v", i, err)
		}
		if payload["new_value"] != "[redacted]" {
			t.Fatalf("审计#%d new_value 应脱敏,实 %q", i, payload["new_value"])
		}
		if strings.Contains(string(entry.Payload), "sk-atomic-secret") || strings.Contains(string(entry.Payload), "sk-rotated") || strings.Contains(string(entry.Payload), secretEncPrefix) {
			t.Fatalf("审计#%d payload 泄露明文或密文: %s", i, entry.Payload)
		}
	}

	// 读回:解密成轮换后的明文。
	final, err := svc.Get(ctx, KeyModerationExternalAPIKeys)
	if err != nil {
		t.Fatalf("Get 轮换后 secret: %v", err)
	}
	if final.Value != `["sk-rotated"]` {
		t.Fatalf("Get 应解密回轮换后明文,实得 %q", final.Value)
	}
}

// TestListToleratesEncryptedSecretRows 锁定 List 整页可用性:库里存在 secret 密文行时 List
// 不得报错(否则管理台设置页整体不可用),密文行原样返回(对客户端的脱敏由 handler 的
// value_configured 负责)。变异:去掉密文跳过守卫 → List 对密文行值校验失败 → RED。
func TestListToleratesEncryptedSecretRows(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.Upsert(ctx, GlobalScope, string(KeyModerationExternalAPIKeys), secretEncPrefix+"opaque-blob", "t"); err != nil {
		t.Fatalf("seed 密文行: %v", err)
	}
	svc := NewService(store, nil, WithSecretCipher(fakeSecretCipher{}))
	rows, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List 遇密文行不得报错: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.Key == KeyModerationExternalAPIKeys {
			found = true
			if !strings.HasPrefix(row.Value, secretEncPrefix) {
				t.Fatalf("List 应原样返回密文行,实得 %q", row.Value)
			}
		}
	}
	if !found {
		t.Fatal("List 结果缺少 secret 行")
	}
}

// TestNormalizeCiphertextGuardIsNarrow 收窄密文跳过守卫的边界:只有「secret 键 + 版本前缀」
// 才跳过值校验;非 secret 键带同样前缀、或 secret 键的坏明文,仍必须被拒。
// 变异:守卫放宽(丢 IsSecretKey 或丢前缀判定)→ 对应用例通过归一 → RED。
func TestNormalizeCiphertextGuardIsNarrow(t *testing.T) {
	if _, err := normalizeStoredSetting(StoredSetting{Key: KeyStreamTimeoutSeconds, Value: secretEncPrefix + "junk"}, SourceDB); err == nil {
		t.Fatal("非 secret 键带密文前缀应校验失败")
	}
	if _, err := normalizeStoredSetting(StoredSetting{Key: KeyModerationExternalAPIKeys, Value: "not-json"}, SourceDB); err == nil {
		t.Fatal("secret 键坏明文应校验失败")
	}
}

// TestClearedSecretNotEncrypted 锁定「清空≠已配置」:secret 清空(空输入校验归一为空容器哨兵)
// 不得加密落库,否则 List 路径(不解密)把已清空误判为已配置。
// 变异:encryptSecretValue 不再跳过空容器 → 库里是密文、指示为已配置 → RED。
func TestClearedSecretNotEncrypted(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	svc := NewService(store, nil, WithSecretCipher(fakeSecretCipher{}))
	if _, err := svc.Upsert(ctx, UpsertInput{Key: KeyModerationExternalAPIKeys, Value: "", UpdatedBy: "t"}); err != nil {
		t.Fatalf("清空 secret: %v", err)
	}
	raw, _, err := store.Get(ctx, GlobalScope, string(KeyModerationExternalAPIKeys))
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if raw.Value != "[]" {
		t.Fatalf("清空哨兵应明文落库为 \"[]\",实得 %q", raw.Value)
	}
	if HasConfiguredSecretValue(KeyModerationExternalAPIKeys, raw.Value) {
		t.Fatal("清空后不得再显示已配置")
	}
}

// TestSecretSettingPlaintextCompat 锁定存量明文兼容:未加密(无前缀)的旧 secret 值按明文读,不报错。
func TestSecretSettingPlaintextCompat(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	// 直接往库里塞一条无前缀的明文 secret(模拟存量/env 迁移前)。
	if _, err := store.Upsert(ctx, GlobalScope, string(KeyModerationExternalAPIKeys), `["legacy-plain"]`, "t"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	svc := NewService(store, nil, WithSecretCipher(fakeSecretCipher{}))
	got, err := svc.Get(ctx, KeyModerationExternalAPIKeys)
	if err != nil {
		t.Fatalf("Get legacy plaintext: %v", err)
	}
	if got.Value != `["legacy-plain"]` {
		t.Fatalf("存量明文应原样读出,实得 %q", got.Value)
	}
}
