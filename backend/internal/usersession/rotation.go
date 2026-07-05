package usersession

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultSessionTTL = 15 * time.Minute
	DefaultRefreshTTL = 30 * 24 * time.Hour
	// DefaultDeviceConfirmationTTL: 新设备确认 token 默认有效期 (DevicePolicy=confirm 时)。
	DefaultDeviceConfirmationTTL = 24 * time.Hour
)

// UserGate 会话使用期的账号资格复核: Validate/Refresh 每次调用复核会话主体,
// 封禁/删除下一请求即生效, 不依赖各封禁入口记得主动吊销(主动吊销只是辅助)。
// 返回 nil 放行; ErrUserIneligible 拒并撤家族; 其余错误(后端瞬时故障)原样上抛。
type UserGate interface {
	CheckSessionUser(ctx context.Context, tenantID, userID int64) error
}

type Service struct {
	Store             Store
	SessionTTL        time.Duration
	RefreshTTL        time.Duration
	SigningKey        []byte
	MaxActiveFamilies int
	DevicePolicy      string
	// DeviceConfirmationTTL 是确认 token 的有效期; 0 用 DefaultDeviceConfirmationTTL。
	DeviceConfirmationTTL time.Duration
	Now                   func() time.Time
	// UserGate 非 nil 时 Validate/Refresh 复核账号资格 (生产 wiring 必注入; nil 仅限单测)。
	UserGate UserGate
}

func NewService(store Store) *Service {
	return &Service{
		Store:      store,
		SessionTTL: DefaultSessionTTL,
		RefreshTTL: DefaultRefreshTTL,
		Now:        time.Now,
	}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (IssuedTokens, error) {
	if s == nil || s.Store == nil {
		return IssuedTokens{}, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.UserID <= 0 {
		return IssuedTokens{}, ErrInvalidInput
	}
	if err := s.enforceDevicePolicy(ctx, in); err != nil {
		return IssuedTokens{}, err
	}
	now := s.now()
	family, err := s.Store.CreateFamily(ctx, in, now)
	if err != nil {
		return IssuedTokens{}, err
	}
	refreshRaw, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return IssuedTokens{}, err
	}
	refreshTTL := firstDuration(in.RefreshTTL, s.RefreshTTL, DefaultRefreshTTL)
	token := RefreshToken{
		ID:         uuid.NewString(),
		TenantID:   in.TenantID,
		FamilyID:   family.ID,
		TokenHash:  refreshHash,
		Generation: family.Generation,
		Status:     RefreshTokenStatusActive,
		ExpiresAt:  now.Add(refreshTTL),
		CreatedAt:  now,
	}
	if err := s.Store.InsertRefreshToken(ctx, token); err != nil {
		return IssuedTokens{}, err
	}
	return s.issuePair(ctx, family, refreshRaw, token.ExpiresAt, firstDuration(in.SessionTTL, s.SessionTTL, DefaultSessionTTL))
}

func (s *Service) Refresh(ctx context.Context, in RefreshInput) (IssuedTokens, error) {
	if s == nil || s.Store == nil {
		return IssuedTokens{}, ErrStoreNotConfigured
	}
	if strings.TrimSpace(in.RefreshToken) == "" {
		return IssuedTokens{}, ErrInvalidInput
	}
	hasCallerIdentity := in.TenantID > 0 || in.UserID > 0
	if hasCallerIdentity && (in.TenantID <= 0 || in.UserID <= 0) {
		return IssuedTokens{}, ErrInvalidInput
	}
	now := s.now()
	rec, err := s.Store.LookupRefreshToken(ctx, HashRefreshToken(in.RefreshToken))
	if err != nil {
		return IssuedTokens{}, err
	}
	if hasCallerIdentity && (rec.Token.TenantID != in.TenantID || rec.Family.TenantID != in.TenantID) {
		return IssuedTokens{}, ErrTokenNotFound
	}
	if hasCallerIdentity && rec.Family.UserID != in.UserID {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_token_cross_user_attempt", now)
		return IssuedTokens{}, ErrSessionUserMismatch
	}
	if rec.Family.Status != FamilyStatusActive && rec.Family.Status != FamilyStatusSuspicious {
		return IssuedTokens{}, ErrFamilyRevoked
	}
	if rec.Token.Status != RefreshTokenStatusActive {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_replay", now)
		return IssuedTokens{}, ErrRefreshReplay
	}
	if !rec.Token.ExpiresAt.After(now) {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_expired", now)
		return IssuedTokens{}, ErrTokenExpired
	}
	if drift := DetectDrift(rec.Family, in.IP, in.UserAgent); drift.Level == DriftHigh {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, drift.Reason, now)
		return IssuedTokens{}, ErrAnomalyRejected
	}
	// 账号资格复核: 封禁/删除的主体不得续期; 命中即撤整个家族 (机会式清理, 掐断 refresh 链)。
	if s.UserGate != nil {
		if err := s.UserGate.CheckSessionUser(ctx, rec.Family.TenantID, rec.Family.UserID); err != nil {
			if errors.Is(err, ErrUserIneligible) {
				_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "user_ineligible", now)
			}
			return IssuedTokens{}, err
		}
	}
	refreshRaw, refreshHash, err := GenerateRefreshToken()
	if err != nil {
		return IssuedTokens{}, err
	}
	newToken := RefreshToken{
		ID:         uuid.NewString(),
		TenantID:   rec.Token.TenantID,
		FamilyID:   rec.Token.FamilyID,
		TokenHash:  refreshHash,
		Generation: rec.Token.Generation + 1,
		Status:     RefreshTokenStatusActive,
		ExpiresAt:  now.Add(firstDuration(in.RefreshTTL, s.RefreshTTL, DefaultRefreshTTL)),
		CreatedAt:  now,
	}
	family, err := s.Store.RotateRefreshToken(ctx, rec.Token, newToken, now)
	if err != nil {
		if err == ErrRefreshReplay {
			_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "refresh_race_or_replay", now)
		}
		return IssuedTokens{}, err
	}
	return s.issuePair(ctx, family, refreshRaw, newToken.ExpiresAt, firstDuration(in.SessionTTL, s.SessionTTL, DefaultSessionTTL))
}

func GenerateRefreshToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := "husr_" + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashRefreshToken(token), nil
}

func HashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

func HashSessionToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

type signedSessionPayload struct {
	ID         string `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	UserID     int64  `json:"user_id"`
	FamilyID   string `json:"family_id"`
	Generation int    `json:"generation"`
	ExpiresAt  int64  `json:"exp"`
}

func (s *Service) issuePair(ctx context.Context, family SessionFamily, refreshRaw string, refreshExpires time.Time, sessionTTL time.Duration) (IssuedTokens, error) {
	if len(s.SigningKey) < 32 {
		return IssuedTokens{}, ErrSigningKeyMissing
	}
	now := s.now()
	tokenID := uuid.NewString()
	sessionExpiry := now.Add(sessionTTL)
	payload := signedSessionPayload{
		ID: tokenID, TenantID: family.TenantID, UserID: family.UserID,
		FamilyID: family.ID, Generation: family.Generation, ExpiresAt: sessionExpiry.Unix(),
	}
	sessionToken, err := s.signPayload(payload)
	if err != nil {
		return IssuedTokens{}, err
	}
	if err := s.Store.InsertSessionToken(ctx, SessionToken{
		ID: tokenID, TenantID: family.TenantID, FamilyID: family.ID,
		TokenHash: HashSessionToken(sessionToken), Generation: family.Generation,
		ExpiresAt: sessionExpiry, CreatedAt: now,
	}); err != nil {
		return IssuedTokens{}, err
	}
	return IssuedTokens{
		SessionToken:  sessionToken,
		RefreshToken:  refreshRaw,
		SessionExpiry: sessionExpiry,
		RefreshExpiry: refreshExpires,
		Family:        family,
		Generation:    family.Generation,
	}, nil
}

func (s *Service) Validate(ctx context.Context, token string, ip string, userAgent string) (ValidatedSession, error) {
	if s == nil || s.Store == nil {
		return ValidatedSession{}, ErrStoreNotConfigured
	}
	payload, err := s.verifyPayload(token)
	if err != nil {
		return ValidatedSession{}, err
	}
	now := s.now()
	if !time.Unix(payload.ExpiresAt, 0).After(now) {
		return ValidatedSession{}, ErrTokenExpired
	}
	rec, err := s.Store.LookupSessionToken(ctx, HashSessionToken(token))
	if err != nil {
		return ValidatedSession{}, err
	}
	if rec.Token.RevokedAt != nil {
		return ValidatedSession{}, ErrFamilyRevoked
	}
	if rec.Token.TenantID != payload.TenantID || rec.Family.TenantID != payload.TenantID || rec.Family.UserID != payload.UserID ||
		rec.Token.FamilyID != payload.FamilyID || rec.Family.ID != payload.FamilyID || rec.Token.Generation != payload.Generation {
		return ValidatedSession{}, ErrTokenNotFound
	}
	if !rec.Token.ExpiresAt.After(now) {
		return ValidatedSession{}, ErrTokenExpired
	}
	if rec.Family.Status != FamilyStatusActive && rec.Family.Status != FamilyStatusSuspicious {
		return ValidatedSession{}, ErrFamilyRevoked
	}
	if rec.Family.Generation != payload.Generation {
		return ValidatedSession{}, ErrTokenNotFound
	}
	if drift := DetectDrift(rec.Family, ip, userAgent); drift.Level == DriftHigh {
		_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, drift.Reason, now)
		return ValidatedSession{}, ErrAnomalyRejected
	}
	// 账号资格复核: bearer 是长效 token, 不复核则封禁/删除后既有会话能活到自然过期。
	// 命中即撤家族 —— 后续请求快速失败在 family revoked, 不再走本复核。
	if s.UserGate != nil {
		if err := s.UserGate.CheckSessionUser(ctx, payload.TenantID, payload.UserID); err != nil {
			if errors.Is(err, ErrUserIneligible) {
				_, _ = s.Store.RevokeFamily(ctx, rec.Family.TenantID, rec.Family.ID, "user_ineligible", now)
			}
			return ValidatedSession{}, err
		}
	}
	_ = s.Store.TouchSessionToken(ctx, rec.Token.TenantID, rec.Token.ID, now)
	return ValidatedSession{
		TenantID: payload.TenantID, UserID: payload.UserID, FamilyID: payload.FamilyID,
		TokenID: payload.ID, Generation: payload.Generation, ExpiresAt: rec.Token.ExpiresAt,
	}, nil
}

func (s *Service) signPayload(payload signedSessionPayload) (string, error) {
	if len(s.SigningKey) < 32 {
		return "", ErrSigningKeyMissing
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.SigningKey)
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return "hus_" + encoded + "." + sig, nil
}

func (s *Service) verifyPayload(token string) (signedSessionPayload, error) {
	if len(s.SigningKey) < 32 {
		return signedSessionPayload{}, ErrSigningKeyMissing
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "hus_") {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	rest := strings.TrimPrefix(token, "hus_")
	encoded, sig, ok := strings.Cut(rest, ".")
	if !ok || encoded == "" || sig == "" {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	mac := hmac.New(sha256.New, s.SigningKey)
	_, _ = mac.Write([]byte(encoded))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(actual, expected) {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	var payload signedSessionPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	if payload.ID == "" || payload.TenantID <= 0 || payload.UserID <= 0 || payload.FamilyID == "" || payload.Generation <= 0 || payload.ExpiresAt <= 0 {
		return signedSessionPayload{}, ErrTokenNotFound
	}
	return payload, nil
}

func (s *Service) enforceDevicePolicy(ctx context.Context, in CreateInput) error {
	// MaxActiveFamilies<=0 = 设备策略整体休眠 (默认), 直接放行, 零生产行为变更。
	if s.MaxActiveFamilies <= 0 {
		return nil
	}
	families, err := s.Store.ListActiveFamiliesForDevicePolicy(ctx, in.TenantID, in.UserID, s.MaxActiveFamilies)
	if err != nil {
		return err
	}
	if len(families) < s.MaxActiveFamilies {
		return nil
	}
	switch strings.TrimSpace(s.DevicePolicy) {
	case "revoke_oldest":
		oldest := families[0]
		_, err := s.Store.RevokeFamily(ctx, in.TenantID, oldest.ID, "device_limit_revoke_oldest", s.now())
		return err
	case "confirm":
		// 不再裸返回错误: 落一条 pending 确认记录并返回携带原文 token 的类型化错误,
		// 让 handler 据此发确认邮件; errors.Is(err, ErrDeviceConfirmationRequired) 仍为真 (Unwrap)。
		return s.requireDeviceConfirmation(ctx, in)
	default:
		return ErrDeviceLimitExceeded
	}
}

// requireDeviceConfirmation 生成一次性确认 token、落 pending 记录, 返回带原文 token 的类型化错误。
func (s *Service) requireDeviceConfirmation(ctx context.Context, in CreateInput) error {
	raw, hash, err := GenerateDeviceConfirmationToken()
	if err != nil {
		return err
	}
	now := s.now()
	ttl := s.DeviceConfirmationTTL
	if ttl <= 0 {
		ttl = DefaultDeviceConfirmationTTL
	}
	dc := DeviceConfirmation{
		TenantID:   in.TenantID,
		UserID:     in.UserID,
		TokenHash:  hash,
		DeviceInfo: normalizeDeviceInfo(in.DeviceInfo, in.UserAgent),
		IP:         IPClass(in.IP),
		UserAgent:  UserAgentClass(in.UserAgent),
		Status:     DeviceConfirmationStatusPending,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}
	if err := s.Store.CreateDeviceConfirmation(ctx, dc); err != nil {
		return err
	}
	return &DeviceConfirmationRequiredError{RawToken: raw, UserID: in.UserID}
}

// ConfirmDevice 校验用户出示的确认 token: 命中 pending 且未过期 → 条件 confirm → 撤最老 family 腾位。
// 全程条件化 / 幂等: 二次确认 (已 confirmed) 命中 0 行, 直接返回"已用"语义, 绝不重复撤多个 family。
func (s *Service) ConfirmDevice(ctx context.Context, tenantID int64, token string) error {
	if s == nil || s.Store == nil {
		return ErrStoreNotConfigured
	}
	if tenantID <= 0 || strings.TrimSpace(token) == "" {
		return ErrInvalidInput
	}
	now := s.now()
	dc, err := s.Store.GetDeviceConfirmationByTokenHash(ctx, tenantID, HashDeviceConfirmationToken(token))
	if err != nil {
		return err
	}
	// 只有仍 pending 的记录可被消费; 已 confirmed/expired 一律按"不存在/已用"挡掉。
	if dc.Status != DeviceConfirmationStatusPending {
		return ErrDeviceConfirmationNotFound
	}
	if !dc.ExpiresAt.After(now) {
		return ErrTokenExpired
	}
	// 条件 UPDATE pending→confirmed; 命中 0 行 = 并发下别处已消费 → 复用 refresh 的"已用"语义,
	// 绝不继续往下腾位 (否则同一 token 并发两次确认会撤两个 family)。
	ok, err := s.Store.MarkDeviceConfirmationConfirmed(ctx, dc.ID, now)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRefreshReplay
	}
	// 确认通过, 撤最老 family 腾出一个设备槽。最老 = ListActiveFamiliesForDevicePolicy 按
	// last_active_at ASC 的第 0 条。无活跃 family 时 (上限已被别处释放) 直接成功, 不报错。
	families, err := s.Store.ListActiveFamiliesForDevicePolicy(ctx, tenantID, dc.UserID, 1)
	if err != nil {
		return err
	}
	if len(families) == 0 {
		return nil
	}
	_, err = s.Store.RevokeFamily(ctx, tenantID, families[0].ID, "device_confirmation_revoke_oldest", now)
	return err
}

// GenerateDeviceConfirmationToken 生成一次性确认 token (镜像 email_verify: crypto/rand 32B→base64url 原文,
// sha256→hash)。返回 (原文, hash)。原文只经邮件交付, 库里只存 hash。
func GenerateDeviceConfirmationToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, HashDeviceConfirmationToken(token), nil
}

// HashDeviceConfirmationToken 计算确认 token 的 sha256 hash (与 HashRefreshToken 同形态, 独立命名以示用途)。
func HashDeviceConfirmationToken(token string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return sum[:]
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func firstDuration(values ...time.Duration) time.Duration {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}
