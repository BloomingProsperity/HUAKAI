package hermes

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestExternalProfile加密落库且读取只返回掩码(t *testing.T) {
	store := &settingsStoreStub{}
	service := NewService(store).WithProfileCredentialKeys(testProfileKeys(t))

	profile, err := service.CreateProfile(context.Background(), 7, 42, "外部模型", "https://model.example.com/v1/", "sk-live-secret-1234")
	if err != nil {
		t.Fatalf("创建外部模型配置：%v", err)
	}
	if bytes.Contains(store.createArg.EncryptedApiKey, []byte("sk-live-secret-1234")) || len(store.createArg.EncryptedApiKey) == 0 {
		t.Fatalf("数据库载荷没有正确加密：%q", store.createArg.EncryptedApiKey)
	}
	if profile.BaseURL != "https://model.example.com/v1" || profile.APIKeyMasked != "****1234" || profile.CredentialVersion != 1 {
		t.Fatalf("公开投影=%+v", profile)
	}
	resolved, err := service.ResolveProfileCredential(context.Background(), profile.ID, 7)
	if err != nil {
		t.Fatalf("解密外部模型配置：%v", err)
	}
	if string(resolved.APIKey) != "sk-live-secret-1234" || resolved.BaseURL != "https://model.example.com/v1" {
		t.Fatalf("解密结果不符合预期：url=%q key=%q", resolved.BaseURL, resolved.APIKey)
	}
}

func TestExternalProfile拒绝私网与携带用户信息的URL(t *testing.T) {
	for _, rawURL := range []string{
		"http://model.example.com/v1",
		"https://user:pass@model.example.com/v1",
		"https://127.0.0.1/v1",
		"https://169.254.169.254/latest/meta-data",
		"https://model.example.com/v1?next=https://evil.example",
	} {
		if _, err := NormalizeExternalBaseURL(rawURL); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("URL %q err=%v，期望拒绝", rawURL, err)
		}
	}
}

func TestEnableForUserRejectsExternalProfileOwnedByAnotherUser(t *testing.T) {
	profileID := int64(99)
	store := &settingsStoreStub{
		profile: dbhermes.HermesApiProfile{
			ID: profileID, TenantID: 7, OwnerUserID: 100,
			ProfileKind: APISourceExternal,
		},
	}
	service := NewService(store)

	err := service.EnableForUser(context.Background(), 7, 42, APISourceExternal, &profileID, "gpt-4o")

	if !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("err=%v want ErrProfileNotOwned", err)
	}
	if store.upsertCalled {
		t.Fatalf("UpsertSettings called for profile owned by another user")
	}
}

func TestEnableForUserWithAuditRollsBackSettingsWhenAuditInsertFails(t *testing.T) {
	auditErr := errors.New("audit sink down")
	profileID := int64(99)
	baseStore := &settingsStoreStub{}
	txStore := &settingsStoreStub{auditErr: auditErr, profile: dbhermes.HermesApiProfile{ID: profileID, TenantID: 7, OwnerUserID: 42, ProfileKind: APISourceExternal}}
	transactor := &storeTransactor{store: txStore}
	service := NewService(baseStore)
	service.tx = transactor

	_, err := service.EnableForUserWithAudit(context.Background(), 7, 42, APISourceExternal, &profileID, "gpt-4o", testAuditFields(ActionEnable))

	if !errors.Is(err, ErrAuditRecordFailed) {
		t.Fatalf("err=%v want ErrAuditRecordFailed", err)
	}
	if !txStore.upsertCalled || !txStore.auditCalled {
		t.Fatalf("transaction should attempt settings upsert and audit insert: upsert=%v audit=%v", txStore.upsertCalled, txStore.auditCalled)
	}
	if baseStore.upsertCalled {
		t.Fatalf("settings upsert used non-transactional store")
	}
	if transactor.commitCount != 0 || transactor.rollbackCount != 1 {
		t.Fatalf("transaction outcome commit=%d rollback=%d want commit=0 rollback=1", transactor.commitCount, transactor.rollbackCount)
	}
}

func TestDisableForUserWithAuditRollsBackSettingsWhenAuditInsertFails(t *testing.T) {
	auditErr := errors.New("audit sink down")
	baseStore := &settingsStoreStub{}
	txStore := &settingsStoreStub{auditErr: auditErr}
	transactor := &storeTransactor{store: txStore}
	service := NewService(baseStore)
	service.tx = transactor

	_, err := service.DisableForUserWithAudit(context.Background(), 7, 42, testAuditFields(ActionDisable))

	if !errors.Is(err, ErrAuditRecordFailed) {
		t.Fatalf("err=%v want ErrAuditRecordFailed", err)
	}
	if !txStore.disableCalled || !txStore.auditCalled {
		t.Fatalf("transaction should attempt disable and audit insert: disable=%v audit=%v", txStore.disableCalled, txStore.auditCalled)
	}
	if baseStore.disableCalled {
		t.Fatalf("disable used non-transactional store")
	}
	if transactor.commitCount != 0 || transactor.rollbackCount != 1 {
		t.Fatalf("transaction outcome commit=%d rollback=%d want commit=0 rollback=1", transactor.commitCount, transactor.rollbackCount)
	}
}

func TestCreateProfileWithAuditRollsBackProfileWhenAuditInsertFails(t *testing.T) {
	auditErr := errors.New("audit sink down")
	baseStore := &settingsStoreStub{}
	txStore := &settingsStoreStub{auditErr: auditErr}
	transactor := &storeTransactor{store: txStore}
	service := NewService(baseStore)
	service.tx = transactor
	service.WithProfileCredentialKeys(testProfileKeys(t))

	_, err := service.CreateProfileWithAudit(context.Background(), 7, 42, "external", "https://api.example.com/v1", "sk-test-secret", testAuditFields(ActionProfileCreate))

	if !errors.Is(err, ErrAuditRecordFailed) {
		t.Fatalf("err=%v want ErrAuditRecordFailed", err)
	}
	if !txStore.createCalled || !txStore.auditCalled {
		t.Fatalf("transaction should attempt profile create and audit insert: create=%v audit=%v", txStore.createCalled, txStore.auditCalled)
	}
	if baseStore.createCalled {
		t.Fatalf("profile create used non-transactional store")
	}
	if transactor.commitCount != 0 || transactor.rollbackCount != 1 {
		t.Fatalf("transaction outcome commit=%d rollback=%d want commit=0 rollback=1", transactor.commitCount, transactor.rollbackCount)
	}
}

func TestDeleteProfileWithAuditRejectsProfileInUseBeforeDelete(t *testing.T) {
	profileID := int64(99)
	baseStore := &settingsStoreStub{}
	txStore := &settingsStoreStub{
		profile: dbhermes.HermesApiProfile{
			ID: profileID, TenantID: 7, OwnerUserID: 42,
			ProfileKind: APISourceExternal,
		},
		profileInUse: true,
	}
	transactor := &storeTransactor{store: txStore}
	service := NewService(baseStore)
	service.tx = transactor

	err := service.DeleteProfileWithAudit(context.Background(), profileID, 7, 42, testAuditFields(ActionProfileRotate))

	if !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("err=%v want ErrProfileInUse", err)
	}
	if txStore.deleteCalled {
		t.Fatalf("DeleteProfile called even though settings still reference profile")
	}
	if txStore.auditCalled {
		t.Fatalf("success audit inserted even though delete was rejected")
	}
	if transactor.commitCount != 0 || transactor.rollbackCount != 1 {
		t.Fatalf("transaction outcome commit=%d rollback=%d want commit=0 rollback=1", transactor.commitCount, transactor.rollbackCount)
	}
}

func testAuditFields(action string) AuditFields {
	return AuditFields{
		TenantID: 7, ActorSource: "token", ActorID: 42, ActorRole: "platform_admin", Action: action,
		SanitizedArgs: map[string]any{"source": "test"}, Result: AuditResultSuccess,
		CorrelationID: "corr-test", RequestID: "req-test",
	}
}

type settingsStoreStub struct {
	profile       dbhermes.HermesApiProfile
	profileInUse  bool
	auditErr      error
	createCalled  bool
	createArg     dbhermes.CreateProfileParams
	deleteCalled  bool
	disableCalled bool
	upsertCalled  bool
	auditCalled   bool
}

type storeTransactor struct {
	store         Store
	commitCount   int
	rollbackCount int
}

func (t *storeTransactor) withTx(ctx context.Context, fn func(Store) error) error {
	err := fn(t.store)
	if err != nil {
		t.rollbackCount++
		return err
	}
	t.commitCount++
	return nil
}

func (s *settingsStoreStub) AppendMessage(context.Context, dbhermes.AppendMessageParams) (int64, error) {
	return 1, nil
}

func (s *settingsStoreStub) CreateConversation(context.Context, dbhermes.CreateConversationParams) (int64, error) {
	return 1, nil
}

func (s *settingsStoreStub) CreateProfile(_ context.Context, arg dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	s.createCalled = true
	s.createArg = arg
	s.profile = dbhermes.HermesApiProfile{
		ID: 123, TenantID: arg.TenantID, OwnerUserID: arg.OwnerUserID, Name: arg.Name,
		ProfileKind: arg.ProfileKind, BaseUrl: arg.BaseUrl,
		EncryptedApiKey: arg.EncryptedApiKey, EncryptionScheme: arg.EncryptionScheme,
		KeyID: arg.KeyID, Nonce: arg.Nonce, AadHash: arg.AadHash,
		ApiKeyFingerprint: arg.ApiKeyFingerprint, ApiKeyHint: arg.ApiKeyHint,
		CredentialVersion: arg.CredentialVersion, SecretBindingID: arg.SecretBindingID,
	}
	return s.profile, nil
}

func (s *settingsStoreStub) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	s.deleteCalled = true
	return 1, nil
}

func (s *settingsStoreStub) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	s.disableCalled = true
	return dbhermes.HermesSetting{TenantID: 7, UserID: 42, Enabled: false, APISource: APISourceExternal}, nil
}

func (s *settingsStoreStub) GetConversation(context.Context, dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	return dbhermes.HermesConversation{}, nil
}

func (s *settingsStoreStub) ListConversationsByOwner(context.Context, dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	return nil, nil
}

func (s *settingsStoreStub) ListMessagesByConversation(context.Context, dbhermes.ListMessagesByConversationParams) ([]dbhermes.ListMessagesByConversationRow, error) {
	return nil, nil
}

func (s *settingsStoreStub) GetProfile(context.Context, dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	return s.profile, nil
}

func (s *settingsStoreStub) GetSettings(context.Context, dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *settingsStoreStub) InsertAuditEvent(context.Context, dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	s.auditCalled = true
	if s.auditErr != nil {
		return dbhermes.HermesAuditEvent{}, s.auditErr
	}
	return dbhermes.HermesAuditEvent{}, nil
}

func (s *settingsStoreStub) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *settingsStoreStub) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *settingsStoreStub) ProfileInUse(context.Context, dbhermes.ProfileInUseParams) (bool, error) {
	return s.profileInUse, nil
}

func (s *settingsStoreStub) SoftDeleteConversation(context.Context, dbhermes.SoftDeleteConversationParams) (int64, error) {
	return 0, nil
}

func (s *settingsStoreStub) UpdateConversationLastMessageAt(context.Context, dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	return 1, nil
}

func (s *settingsStoreStub) UpsertSettings(_ context.Context, arg dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	s.upsertCalled = true
	return dbhermes.HermesSetting{TenantID: 7, UserID: 42, Enabled: true, APISource: APISourceExternal, ProfileID: arg.ProfileID, ModelKey: arg.ModelKey}, nil
}

func testProfileKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("创建测试密钥：%v", err)
	}
	return keys
}
