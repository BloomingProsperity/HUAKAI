package hermes

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestEnableForUser_TenantIsolation(t *testing.T) {
	// Regression: enabling Hermes for tenant A must not update the same user id in tenant B.
	store := &hermesStoreSpy{}

	row, err := enableForUserWithStore(context.Background(), store, 7, 1, APISourceManaged, nil)

	if err != nil {
		t.Fatalf("EnableForUser: %v", err)
	}
	// Mutation check: 删除 UpsertSettingsParams.TenantID 或改成 0,此断言会让跨租户写入 mutant 失败。
	if !store.upsertCalled || store.upsertArg.TenantID != 7 || store.upsertArg.UserID != 1 {
		t.Fatalf("upsert tenant/user=(%d,%d) called=%v want (7,1)", store.upsertArg.TenantID, store.upsertArg.UserID, store.upsertCalled)
	}
	if row.TenantID != 7 || row.UserID != 1 {
		t.Fatalf("returned row tenant/user=(%d,%d) want (7,1)", row.TenantID, row.UserID)
	}
}

func TestGetSettings_TenantScoped(t *testing.T) {
	// Regression: settings reads must include tenant id, or same user id can read another tenant's Hermes state.
	store := &hermesStoreSpy{}
	service := NewService(store)

	got, err := service.GetSettings(context.Background(), 7, 42)

	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	// Mutation check: 将 GetSettingsParams.TenantID 改成 0 或只按 user_id 查询,此断言会失败。
	if !store.getSettingsCalled || store.getSettingsArg.TenantID != 7 || store.getSettingsArg.UserID != 42 {
		t.Fatalf("get settings tenant/user=(%d,%d) called=%v want (7,42)", store.getSettingsArg.TenantID, store.getSettingsArg.UserID, store.getSettingsCalled)
	}
	if got.TenantID != 7 || got.UserID != 42 {
		t.Fatalf("settings tenant/user=(%d,%d) want (7,42)", got.TenantID, got.UserID)
	}
}

func TestRecordAudit_TenantTagged(t *testing.T) {
	// Regression: audit rows must carry tenant_id so operations evidence cannot be mixed across tenants.
	store := &hermesStoreSpy{}
	service := NewService(store)

	err := service.RecordAudit(
		context.Background(), 7, 42, ActionEnable,
		map[string]any{"api_key": "sk-secret-123"}, AuditResultSuccess,
		"corr-tenant", "req-tenant",
	)

	if err != nil {
		t.Fatalf("RecordAudit: %v", err)
	}
	// Mutation check: 删除 InsertAuditEventParams.TenantID 赋值,tenant_id 会变 0 并触发此断言。
	if !store.auditCalled || store.auditArg.TenantID != 7 || store.auditArg.ActorUserID != 42 {
		t.Fatalf("audit tenant/actor=(%d,%d) called=%v want (7,42)", store.auditArg.TenantID, store.auditArg.ActorUserID, store.auditCalled)
	}
	var args map[string]any
	if err := json.Unmarshal(store.auditArg.SanitizedArgs, &args); err != nil {
		t.Fatalf("audit sanitized args json: %v", err)
	}
	if args["api_key"] != "[REDACTED]" {
		t.Fatalf("audit api_key=%q want [REDACTED]", args["api_key"])
	}
}

type hermesStoreSpy struct {
	appendCalled bool
	appendArg    dbhermes.AppendMessageParams

	conversationID     int64
	conversationRow    dbhermes.HermesConversation
	conversationErr    error
	getConversationArg dbhermes.GetConversationParams

	listConversationsCalled bool
	listConversationsArg    dbhermes.ListConversationsByOwnerParams
	listConversationsRows   []dbhermes.HermesConversation
	listConversationsErr    error

	listMessagesCalled bool
	listMessagesArg    dbhermes.ListMessagesByConversationParams
	listMessagesRows   []dbhermes.HermesMessage
	listMessagesErr    error

	softDeleteCalled bool
	softDeleteArg    dbhermes.SoftDeleteConversationParams
	softDeleteRows   int64
	softDeleteErr    error

	createCalled bool
	createArg    dbhermes.CreateProfileParams
	createErr    error

	deleteCalled bool
	deleteArg    dbhermes.DeleteProfileParams
	deleteRows   int64
	deleteErr    error

	disableCalled bool
	disableArg    dbhermes.DisableHermesParams

	getAPIKeyOwner int64
	getAPIKeyErr   error

	getProfileCalled bool
	getProfileArg    dbhermes.GetProfileParams
	getProfileRow    dbhermes.HermesApiProfile
	getProfileErr    error

	getSettingsCalled bool
	getSettingsArg    dbhermes.GetSettingsParams
	getSettingsRow    dbhermes.HermesSetting
	getSettingsErr    error

	auditCalled bool
	auditArg    dbhermes.InsertAuditEventParams
	auditErr    error

	profileInUseCalled bool
	profileInUseArg    dbhermes.ProfileInUseParams
	profileInUse       bool
	profileInUseErr    error

	upsertCalled bool
	upsertArg    dbhermes.UpsertSettingsParams
	upsertErr    error
}

func (s *hermesStoreSpy) AppendMessage(_ context.Context, arg dbhermes.AppendMessageParams) (int64, error) {
	s.appendCalled = true
	s.appendArg = arg
	return 1, nil
}

func (s *hermesStoreSpy) CreateConversation(_ context.Context, arg dbhermes.CreateConversationParams) (int64, error) {
	if s.conversationID == 0 {
		s.conversationID = 1
	}
	return s.conversationID, nil
}

func (s *hermesStoreSpy) CreateProfile(_ context.Context, arg dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	s.createCalled = true
	s.createArg = arg
	if s.createErr != nil {
		return dbhermes.HermesApiProfile{}, s.createErr
	}
	return dbhermes.HermesApiProfile{
		ID: 123, TenantID: arg.TenantID, OwnerUserID: arg.OwnerUserID,
		Name: arg.Name, ProfileKind: arg.ProfileKind,
		APIKeyID: arg.APIKeyID, PoolGroupID: arg.PoolGroupID,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}, nil
}

func (s *hermesStoreSpy) DeleteProfile(_ context.Context, arg dbhermes.DeleteProfileParams) (int64, error) {
	s.deleteCalled = true
	s.deleteArg = arg
	if s.deleteErr != nil {
		return 0, s.deleteErr
	}
	return s.deleteRows, nil
}

func (s *hermesStoreSpy) DisableHermes(_ context.Context, arg dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	s.disableCalled = true
	s.disableArg = arg
	return dbhermes.HermesSetting{
		TenantID: arg.TenantID, UserID: arg.UserID,
		Enabled: false, APISource: APISourceManaged,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}, nil
}

func (s *hermesStoreSpy) GetAPIKeyOwner(context.Context, dbhermes.GetAPIKeyOwnerParams) (int64, error) {
	if s.getAPIKeyErr != nil {
		return 0, s.getAPIKeyErr
	}
	return s.getAPIKeyOwner, nil
}

func (s *hermesStoreSpy) GetConversation(_ context.Context, arg dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	s.getConversationArg = arg
	if s.conversationErr != nil {
		return dbhermes.HermesConversation{}, s.conversationErr
	}
	if s.conversationRow.ID != 0 {
		return s.conversationRow, nil
	}
	return dbhermes.HermesConversation{ID: arg.ID, TenantID: arg.TenantID, OwnerUserID: 42}, nil
}

func (s *hermesStoreSpy) ListConversationsByOwner(_ context.Context, arg dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	s.listConversationsCalled = true
	s.listConversationsArg = arg
	if s.listConversationsErr != nil {
		return nil, s.listConversationsErr
	}
	return s.listConversationsRows, nil
}

func (s *hermesStoreSpy) ListMessagesByConversation(_ context.Context, arg dbhermes.ListMessagesByConversationParams) ([]dbhermes.HermesMessage, error) {
	s.listMessagesCalled = true
	s.listMessagesArg = arg
	if s.listMessagesErr != nil {
		return nil, s.listMessagesErr
	}
	return s.listMessagesRows, nil
}

func (s *hermesStoreSpy) GetProfile(_ context.Context, arg dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	s.getProfileCalled = true
	s.getProfileArg = arg
	if s.getProfileErr != nil {
		return dbhermes.HermesApiProfile{}, s.getProfileErr
	}
	if s.getProfileRow.ID != 0 {
		return s.getProfileRow, nil
	}
	return dbhermes.HermesApiProfile{
		ID: arg.ID, TenantID: arg.TenantID, OwnerUserID: 42,
		Name: "profile", ProfileKind: APISourceManaged,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}, nil
}

func (s *hermesStoreSpy) GetSettings(_ context.Context, arg dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	s.getSettingsCalled = true
	s.getSettingsArg = arg
	if s.getSettingsErr != nil {
		return dbhermes.HermesSetting{}, s.getSettingsErr
	}
	if s.getSettingsRow.TenantID != 0 {
		return s.getSettingsRow, nil
	}
	return dbhermes.HermesSetting{
		TenantID: arg.TenantID, UserID: arg.UserID,
		Enabled: true, APISource: APISourceManaged,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}, nil
}

func (s *hermesStoreSpy) InsertAuditEvent(_ context.Context, arg dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	s.auditCalled = true
	s.auditArg = arg
	if s.auditErr != nil {
		return dbhermes.HermesAuditEvent{}, s.auditErr
	}
	return dbhermes.HermesAuditEvent{
		ID: 500, Ts: arg.Ts, TenantID: arg.TenantID, ActorUserID: arg.ActorUserID,
		Action: arg.Action, SanitizedArgs: arg.SanitizedArgs, Result: arg.Result,
		CorrelationID: arg.CorrelationID, RequestID: arg.RequestID,
	}, nil
}

func (s *hermesStoreSpy) SoftDeleteConversation(_ context.Context, arg dbhermes.SoftDeleteConversationParams) (int64, error) {
	s.softDeleteCalled = true
	s.softDeleteArg = arg
	if s.softDeleteErr != nil {
		return 0, s.softDeleteErr
	}
	return s.softDeleteRows, nil
}

func (s *hermesStoreSpy) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *hermesStoreSpy) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *hermesStoreSpy) ProfileInUse(_ context.Context, arg dbhermes.ProfileInUseParams) (bool, error) {
	s.profileInUseCalled = true
	s.profileInUseArg = arg
	if s.profileInUseErr != nil {
		return false, s.profileInUseErr
	}
	return s.profileInUse, nil
}

func (s *hermesStoreSpy) UpdateConversationLastMessageAt(context.Context, dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	return 1, nil
}

func (s *hermesStoreSpy) UpsertSettings(_ context.Context, arg dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	s.upsertCalled = true
	s.upsertArg = arg
	if s.upsertErr != nil {
		return dbhermes.HermesSetting{}, s.upsertErr
	}
	return dbhermes.HermesSetting{
		TenantID: arg.TenantID, UserID: arg.UserID,
		Enabled: arg.Enabled, APISource: arg.APISource, ProfileID: arg.ProfileID,
		CreatedAt: testPGTime(), UpdatedAt: testPGTime(),
	}, nil
}

func testPGTime() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true}
}
