package passkey

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type CeremonyEngine interface {
	BeginRegistration(context.Context, Config, WebAuthnUser, []CredentialRecord) (CeremonyOptions, []byte, error)
	FinishRegistration(context.Context, Config, WebAuthnUser, []byte, []byte) (VerifiedCredential, error)
	BeginDiscoverableLogin(context.Context, Config) (CeremonyOptions, []byte, error)
	FinishDiscoverableLogin(context.Context, Config, []byte, []byte, DiscoverableResolver) (DiscoverableLoginResult, error)
}

type DiscoverableResolver func(context.Context, []byte, []byte) (ResolvedCredential, error)

type ResolvedCredential struct {
	User              WebAuthnUser
	MatchedCredential CredentialRecord
}

type VerifiedCredential struct {
	CredentialID    []byte
	PublicKey       []byte
	SignCount       uint32
	AAGUID          []byte
	AttestationType string
	Transports      []string
	CloneWarning    bool
}

type DiscoverableLoginResult struct {
	User              WebAuthnUser
	Credential        VerifiedCredential
	MatchedCredential CredentialRecord
	AssertedSignCount uint32
}

type Service struct {
	store   Store
	users   UserReader
	configs ConfigSource
	engine  CeremonyEngine
	now     func() time.Time
}

type Option func(*Service)

func NewService(store Store, users UserReader, configs ConfigSource, opts ...Option) *Service {
	s := &Service{
		store:   store,
		users:   users,
		configs: configs,
		engine:  NewWebAuthnEngine(),
		now:     func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func WithCeremonyEngine(engine CeremonyEngine) Option {
	return func(s *Service) {
		if engine != nil {
			s.engine = engine
		}
	}
}

func WithNow(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = func() time.Time { return now().UTC() }
		}
	}
}

func (s *Service) RegisterBegin(ctx context.Context, in RegisterBeginInput) (BeginResponse, error) {
	if err := s.ready(); err != nil {
		return BeginResponse{}, err
	}
	if in.TenantID <= 0 || in.User.ID <= 0 || in.User.TenantID != in.TenantID {
		return BeginResponse{}, ErrInvalidInput
	}
	cfg, err := s.registrationConfig(ctx)
	if err != nil {
		return BeginResponse{}, err
	}
	credentials, err := s.store.ListCredentials(ctx, in.TenantID, in.User.ID)
	if err != nil {
		return BeginResponse{}, err
	}
	user := WebAuthnUser{User: in.User, Credentials: credentials}
	options, sessionData, err := s.engine.BeginRegistration(ctx, cfg, user, credentials)
	if err != nil {
		return BeginResponse{}, err
	}
	now := s.now()
	session := CeremonySession{
		ID: uuid.NewString(), TenantID: in.TenantID, UserID: in.User.ID, Purpose: PurposeRegister,
		SessionData: sessionData, ExpiresAt: now.Add(cfg.ttl()), CreatedAt: now,
	}
	if err := s.store.SaveCeremonySession(ctx, session); err != nil {
		return BeginResponse{}, err
	}
	return BeginResponse{SessionID: session.ID, PublicKey: optionsJSON(options), ExpiresAt: session.ExpiresAt}, nil
}

func (s *Service) RegisterFinish(ctx context.Context, in RegisterFinishInput) (CredentialRecord, error) {
	if err := s.ready(); err != nil {
		return CredentialRecord{}, err
	}
	if in.TenantID <= 0 || in.User.ID <= 0 || in.User.TenantID != in.TenantID || in.SessionID == "" || len(in.CredentialJSON) == 0 {
		return CredentialRecord{}, ErrInvalidInput
	}
	cfg, err := s.registrationConfig(ctx)
	if err != nil {
		return CredentialRecord{}, err
	}
	session, err := s.store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{
		ID: in.SessionID, TenantID: in.TenantID, UserID: in.User.ID, Purpose: PurposeRegister, Now: s.now(),
	})
	if err != nil {
		return CredentialRecord{}, err
	}
	credentials, err := s.store.ListCredentials(ctx, in.TenantID, in.User.ID)
	if err != nil {
		return CredentialRecord{}, err
	}
	verified, err := s.engine.FinishRegistration(ctx, cfg, WebAuthnUser{User: in.User, Credentials: credentials}, session.SessionData, in.CredentialJSON)
	if err != nil {
		return CredentialRecord{}, err
	}
	return s.store.SaveCredential(ctx, credentialRecordFromVerified(in.TenantID, in.User.ID, verified, cleanName(in.Name), s.now()))
}

func (s *Service) LoginBegin(ctx context.Context, in LoginBeginInput) (BeginResponse, error) {
	if err := s.ready(); err != nil {
		return BeginResponse{}, err
	}
	if in.TenantID <= 0 {
		return BeginResponse{}, ErrInvalidInput
	}
	cfg, err := s.loginConfig(ctx)
	if err != nil {
		return BeginResponse{}, err
	}
	options, sessionData, err := s.engine.BeginDiscoverableLogin(ctx, cfg)
	if err != nil {
		return BeginResponse{}, err
	}
	now := s.now()
	session := CeremonySession{
		ID: uuid.NewString(), TenantID: in.TenantID, Purpose: PurposeLogin,
		SessionData: sessionData, ExpiresAt: now.Add(cfg.ttl()), CreatedAt: now,
	}
	if err := s.store.SaveCeremonySession(ctx, session); err != nil {
		return BeginResponse{}, err
	}
	return BeginResponse{SessionID: session.ID, PublicKey: optionsJSON(options), ExpiresAt: session.ExpiresAt}, nil
}

func (s *Service) LoginFinish(ctx context.Context, in LoginFinishInput) (LoginFinishResult, error) {
	if err := s.ready(); err != nil {
		return LoginFinishResult{}, err
	}
	if in.TenantID <= 0 || in.SessionID == "" || len(in.CredentialJSON) == 0 {
		return LoginFinishResult{}, ErrInvalidInput
	}
	cfg, err := s.loginConfig(ctx)
	if err != nil {
		return LoginFinishResult{}, err
	}
	session, err := s.store.ConsumeCeremonySession(ctx, ConsumeCeremonyInput{
		ID: in.SessionID, TenantID: in.TenantID, Purpose: PurposeLogin, Now: s.now(),
	})
	if err != nil {
		return LoginFinishResult{}, err
	}
	result, err := s.engine.FinishDiscoverableLogin(ctx, cfg, session.SessionData, in.CredentialJSON, s.discoverableResolver(in.TenantID))
	if err != nil {
		return LoginFinishResult{}, err
	}
	// 账号资格门:ceremony 已验证 credential 归属,但签发 session 前必须复用与密码/social 登录
	// 一致的账号状态门(EnsureLoginEligible)。否则被管理员禁用/删除/锁定/待重置的用户,凭既有
	// passkey 仍能登录拿到 session,绕过账号停用控制(auth-core 访问控制不变量)。放在引擎返回后
	// 而非 resolver 内,避免依赖 webauthn 引擎是否原样透传 resolver 错误。
	if err := userauth.EnsureLoginEligible(result.User.User, s.now()); err != nil {
		return LoginFinishResult{}, err
	}
	stored := result.MatchedCredential
	if len(stored.CredentialID) == 0 {
		stored = result.UserCredential()
	}
	if signCountRegressed(stored.SignCount, result.AssertedSignCount) {
		_ = s.store.FlagCredentialCloneWarning(ctx, in.TenantID, stored.CredentialID, s.now())
		return LoginFinishResult{}, ErrCloneDetected
	}
	updated, err := s.store.UpdateCredentialUsage(ctx, in.TenantID, stored.CredentialID, result.Credential.SignCount, result.Credential.CloneWarning, s.now())
	if err != nil {
		// 并发竞态:两个登录都过了上面的应用层 signCountRegressed,其中一个在
		// store 的 CAS 处失败返回 ErrCloneDetected。与上面单请求路径对齐——也置位
		// clone_warning,否则这条防克隆信号在竞态路径下丢失(可观测性缺口)。
		if errors.Is(err, ErrCloneDetected) {
			_ = s.store.FlagCredentialCloneWarning(ctx, in.TenantID, stored.CredentialID, s.now())
		}
		return LoginFinishResult{}, err
	}
	return LoginFinishResult{User: result.User.User, Credential: updated}, nil
}

func (r DiscoverableLoginResult) UserCredential() CredentialRecord {
	for _, credential := range r.User.Credentials {
		if bytes.Equal(credential.CredentialID, r.Credential.CredentialID) {
			return credential
		}
	}
	return CredentialRecord{}
}

func (s *Service) ListCredentials(ctx context.Context, tenantID, userID int64) ([]CredentialSummary, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrInvalidInput
	}
	records, err := s.store.ListCredentials(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return summaries(records), nil
}

func (s *Service) DeleteCredential(ctx context.Context, in DeleteCredentialInput) error {
	if err := s.ready(); err != nil {
		return err
	}
	if in.TenantID <= 0 || in.UserID <= 0 || in.ID <= 0 {
		return ErrInvalidInput
	}
	return s.store.DeleteCredential(ctx, in.TenantID, in.UserID, in.ID)
}

func (s *Service) StoreCredential(ctx context.Context, record CredentialRecord) (CredentialRecord, error) {
	if err := s.ready(); err != nil {
		return CredentialRecord{}, err
	}
	return s.store.SaveCredential(ctx, record)
}

func (s *Service) CheckOrigin(ctx context.Context, origin string, registration bool) error {
	cfg, err := s.config(ctx)
	if err != nil {
		return err
	}
	if registration {
		if err := cfg.registrationReady(); err != nil {
			return err
		}
	} else if err := cfg.loginReady(); err != nil {
		return err
	}
	if !cfg.originAllowed(origin) {
		return ErrOriginNotAllowed
	}
	return nil
}

func (s *Service) discoverableResolver(tenantID int64) DiscoverableResolver {
	return func(ctx context.Context, rawID, userHandle []byte) (ResolvedCredential, error) {
		record, err := s.store.GetCredentialByCredentialID(ctx, tenantID, rawID)
		if err != nil {
			return ResolvedCredential{}, err
		}
		user, err := s.users.GetUserByID(ctx, record.TenantID, record.UserID)
		if err != nil {
			return ResolvedCredential{}, err
		}
		if len(userHandle) > 0 && !bytes.Equal(userHandle, WebAuthnUserHandle(user.TenantID, user.ID)) {
			return ResolvedCredential{}, ErrCredentialOwnerMismatch
		}
		credentials, err := s.store.ListCredentials(ctx, record.TenantID, record.UserID)
		if err != nil {
			return ResolvedCredential{}, err
		}
		return ResolvedCredential{
			User:              WebAuthnUser{User: user, Credentials: credentials},
			MatchedCredential: record,
		}, nil
	}
}

func (s *Service) ready() error {
	if s == nil || s.store == nil {
		return ErrStoreNotConfigured
	}
	if s.users == nil {
		return ErrUserStoreNotConfigured
	}
	if s.configs == nil {
		return ErrConfigNotConfigured
	}
	if s.engine == nil {
		return ErrCeremonyEngineUnavailable
	}
	return nil
}

func (s *Service) config(ctx context.Context) (Config, error) {
	cfg, err := s.configs.Config(ctx)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (s *Service) loginConfig(ctx context.Context) (Config, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return Config{}, err
	}
	return cfg, cfg.loginReady()
}

func (s *Service) registrationConfig(ctx context.Context) (Config, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return Config{}, err
	}
	return cfg, cfg.registrationReady()
}

func (s *Service) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func credentialRecordFromVerified(tenantID, userID int64, verified VerifiedCredential, name string, now time.Time) CredentialRecord {
	return CredentialRecord{
		TenantID: tenantID, UserID: userID, CredentialID: append([]byte(nil), verified.CredentialID...),
		PublicKey: append([]byte(nil), verified.PublicKey...), SignCount: verified.SignCount,
		AAGUID: append([]byte(nil), verified.AAGUID...), AttestationType: verified.AttestationType,
		Transports: append([]string(nil), verified.Transports...), CloneWarning: verified.CloneWarning,
		Name: cleanName(name), CreatedAt: now.UTC(),
	}
}

func signCountRegressed(stored, asserted uint32) bool {
	if stored == 0 && asserted == 0 {
		return false
	}
	return asserted <= stored
}

func optionsJSON(options CeremonyOptions) json.RawMessage {
	if len(options) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(options)
}

// AdminClearCredentials 为 admin 账号找回强制删除某用户的所有 passkey,
// 复用 owner 维度的 list+delete 原语; 返回清除的数量。AUTH-098。
func (s *Service) AdminClearCredentials(ctx context.Context, tenantID, userID int64) (int, error) {
	summaries, err := s.ListCredentials(ctx, tenantID, userID)
	if err != nil {
		return 0, err
	}
	cleared := 0
	for _, c := range summaries {
		if err := s.DeleteCredential(ctx, DeleteCredentialInput{TenantID: tenantID, UserID: userID, ID: c.ID}); err != nil {
			if err == ErrCredentialNotFound {
				continue
			}
			return cleared, err
		}
		cleared++
	}
	return cleared, nil
}
