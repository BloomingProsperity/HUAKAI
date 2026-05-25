package hermes

import (
	"context"
	"errors"
	"testing"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestEnableForUserRejectsDedicatedProfileOwnedByAnotherUser(t *testing.T) {
	profileID := int64(99)
	store := &settingsStoreStub{
		profile: dbhermes.HermesApiProfile{
			ID: profileID, TenantID: 7, OwnerUserID: 100,
			ProfileKind: APISourceDedicatedGroup,
		},
	}
	service := NewService(store)

	err := service.EnableForUser(context.Background(), 7, 42, APISourceDedicatedGroup, &profileID)

	if !errors.Is(err, ErrProfileNotOwned) {
		t.Fatalf("err=%v want ErrProfileNotOwned", err)
	}
	if store.upsertCalled {
		t.Fatalf("UpsertSettings called for profile owned by another user")
	}
}

func TestEnableForUserWithAuditRollsBackSettingsWhenAuditInsertFails(t *testing.T) {
	auditErr := errors.New("audit sink down")
	baseStore := &settingsStoreStub{}
	txStore := &settingsStoreStub{auditErr: auditErr}
	transactor := &storeTransactor{store: txStore}
	service := NewService(baseStore)
	service.tx = transactor

	_, err := service.EnableForUserWithAudit(context.Background(), 7, 42, APISourceManaged, nil, testAuditFields(ActionEnable))

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

	_, err := service.CreateProfileWithAudit(context.Background(), 7, 42, "managed", APISourceManaged, nil, nil, testAuditFields(ActionProfileCreate))

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
			ProfileKind: APISourceDedicatedGroup,
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
		TenantID: 7, ActorUserID: 42, Action: action,
		SanitizedArgs: map[string]any{"source": "test"}, Result: AuditResultSuccess,
		CorrelationID: "corr-test", RequestID: "req-test",
	}
}

type settingsStoreStub struct {
	profile       dbhermes.HermesApiProfile
	profileInUse  bool
	auditErr      error
	createCalled  bool
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

func (s *settingsStoreStub) CreateProfile(context.Context, dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	s.createCalled = true
	return dbhermes.HermesApiProfile{
		ID: 123, TenantID: 7, OwnerUserID: 42, Name: "managed",
		ProfileKind: APISourceManaged,
	}, nil
}

func (s *settingsStoreStub) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	s.deleteCalled = true
	return 1, nil
}

func (s *settingsStoreStub) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	s.disableCalled = true
	return dbhermes.HermesSetting{TenantID: 7, UserID: 42, Enabled: false, APISource: APISourceManaged}, nil
}

func (s *settingsStoreStub) GetAPIKeyOwner(context.Context, dbhermes.GetAPIKeyOwnerParams) (int64, error) {
	return 0, nil
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

func (s *settingsStoreStub) UpsertSettings(context.Context, dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	s.upsertCalled = true
	return dbhermes.HermesSetting{TenantID: 7, UserID: 42, Enabled: true, APISource: APISourceManaged}, nil
}
