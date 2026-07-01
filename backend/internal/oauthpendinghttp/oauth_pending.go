// Package oauthpendinghttp 实现「社交登录无已验证邮箱 → 补邮箱建号」流程的服务端(独立成包,
// 避免继续膨胀 gatewayhttp god package,符合 §13 职责拆分)。
//
// 背景:QQ / 无公开验证邮箱的 GitHub 等 OAuth 登录只给身份不给已验证邮箱,而建号必须要邮箱。
// 原先这类登录成为死胡同。本包把它接通成三步:
//   ① OAuth 回调发现待补邮箱 → gatewayhttp 用 MintPendingToken 签发 pending_token(载已校验身份)
//   ② POST /v1/auth/oauth-pending/send-code {pending_token,email} → 发一次性码到该邮箱,返回 challenge_token
//   ③ POST /v1/auth/oauth-pending/complete {challenge_token,code} → 验码 → 建号 + 建会话
//
// 安全模型(全程无状态、不落库):
//   - pending/challenge 两 token 都用 HMAC(派生密钥) 签名 → 无密钥造不出身份/绑定;
//   - 邮箱所有权码 = 8 位 base32(2^40 种),**码本身不入 token**;challenge_token 只带
//     codeBinding=HMAC(key, tenant|provider|subject|email|code),持 token 也反推不出码;
//   - 防在线爆破:2^40 码空间 + OAuth 端点限流(cmd/gateway rate_limit) + 10 分 TTL,窗口内命中概率可忽略;
//   - 防离线爆破(即便 token 泄露):codeBinding 用服务端密钥且不落库;
//   - 重放已用码:重放 → 账号已建 → CompleteSocialSignupWithVerifiedEmail 走既有绑定直接登录,无害;
//   - 抢注他人邮箱:CompleteSocialSignupWithVerifiedEmail 已有 GetUserByEmail→ErrEmailExists 挡住;
//   - 密钥用会话签名密钥经 HMAC 域分隔派生(DeriveKey),不新增 env、与会话密钥隔离。
package oauthpendinghttp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/clientip"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

const (
	pendingTokenTTL   = 15 * time.Minute
	challengeTokenTTL = 10 * time.Minute
	kindPending       = "oauth_pending_v1"
	kindChallenge     = "oauth_challenge_v1"
	codeBindingPrefix = "huakai-oauth-codebind-v1:"
	pendingKeyLabel   = "huakai-oauth-pending-v1"
)

var errPendingToken = errors.New("oauthpendinghttp: token invalid or expired")

// EmailCodeSender 给裸邮箱发一次性验证码(账号尚未建、无 User)。由 email.AuthSender 实现。
type EmailCodeSender interface {
	SendOAuthEmailCode(ctx context.Context, tenantID int64, email, code string) error
}

// Deps 是本包路由的依赖。Key 为空则整流程停用(端点返 503)。
type Deps struct {
	Auth        *userauth.Service
	Sessions    *usersession.Service
	EmailSender EmailCodeSender
	ClientIP    *clientip.Resolver
	Key         []byte
	// RecordEvent 可选:记录安全事件(nil 则不记)。
	RecordEvent func(ctx context.Context, eventType string, tenantID, userID int64, provider, outcome, reasonClass string)
}

func (d Deps) recordEvent(ctx context.Context, eventType string, tenantID, userID int64, provider, outcome, reasonClass string) {
	if d.RecordEvent != nil {
		d.RecordEvent(ctx, eventType, tenantID, userID, provider, outcome, reasonClass)
	}
}

// MountRoutes 在传入的路由(应为 /v1/auth 组)上挂载补邮箱两端点(相对路径)。
func MountRoutes(r chi.Router, d Deps) {
	r.Post("/oauth-pending/send-code", newSendCodeHandler(d))
	r.Post("/oauth-pending/complete", newCompleteHandler(d))
}

// DeriveKey 从会话签名密钥经 HMAC 域分隔派生本流程专用密钥。sessionKey 为空返回 nil(流程停用)。
func DeriveKey(sessionKey []byte) []byte {
	if len(sessionKey) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, sessionKey)
	mac.Write([]byte(pendingKeyLabel))
	return mac.Sum(nil)
}

type pendingClaims struct {
	TenantID    int64  `json:"t"`
	Provider    string `json:"p"`
	Subject     string `json:"s"`
	DisplayName string `json:"d"`
	Kind        string `json:"k"`
	Exp         int64  `json:"e"`
}

type challengeClaims struct {
	TenantID    int64  `json:"t"`
	Provider    string `json:"p"`
	Subject     string `json:"s"`
	DisplayName string `json:"d"`
	Email       string `json:"m"`
	CodeBinding string `json:"c"`
	Kind        string `json:"k"`
	Exp         int64  `json:"e"`
}

func signToken(key []byte, payload []byte) string {
	p := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyToken(key []byte, token string) ([]byte, error) {
	if len(key) == 0 {
		return nil, errPendingToken
	}
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, errPendingToken
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return nil, errPendingToken
	}
	return base64.RawURLEncoding.DecodeString(parts[0])
}

// MintPendingToken 供 gatewayhttp 的 OAuth 回调在「待补邮箱」分支签发 pending_token。
func MintPendingToken(key []byte, identity userauth.VerifiedIdentity, tenantID int64, now time.Time) (string, error) {
	if len(key) == 0 {
		return "", errPendingToken
	}
	payload, err := json.Marshal(pendingClaims{
		TenantID: tenantID, Provider: identity.Provider, Subject: identity.Subject,
		DisplayName: identity.DisplayName, Kind: kindPending,
		Exp: now.UTC().Add(pendingTokenTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	return signToken(key, payload), nil
}

func verifyPendingToken(key []byte, token string, now time.Time) (pendingClaims, error) {
	payload, err := verifyToken(key, token)
	if err != nil {
		return pendingClaims{}, err
	}
	var c pendingClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return pendingClaims{}, errPendingToken
	}
	if c.Kind != kindPending || c.TenantID <= 0 || strings.TrimSpace(c.Provider) == "" || strings.TrimSpace(c.Subject) == "" {
		return pendingClaims{}, errPendingToken
	}
	if now.UTC().Unix() >= c.Exp {
		return pendingClaims{}, errPendingToken
	}
	return c, nil
}

// codeBinding = HMAC(key, prefix ‖ 各字段「8 字节大端长度前缀 + 字节」编码)。
// 用长度前缀分隔字段而非 "|" 拼接,消除分隔符歧义类:email 允许含 "|"(looksLikeEmail 不挡)、
// subject 由上游给不做限制,"|" 拼接下不同的 (subject,email) 边界可折叠成同一字符串产生碰撞;
// 长度前缀让每个字段的边界不可挪移,任意字段值都不会与相邻字段串到一起。
func codeBinding(key []byte, tenantID int64, provider, subject, email, code string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(codeBindingPrefix))
	writeField := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		mac.Write(n[:])
		mac.Write([]byte(s))
	}
	writeField(itoa(tenantID))
	writeField(strings.ToLower(strings.TrimSpace(provider)))
	writeField(strings.TrimSpace(subject))
	writeField(normalizeEmail(email))
	writeField(normalizeCode(code))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mintChallengeToken(key []byte, c pendingClaims, email, code string, now time.Time) (string, error) {
	payload, err := json.Marshal(challengeClaims{
		TenantID: c.TenantID, Provider: c.Provider, Subject: c.Subject, DisplayName: c.DisplayName,
		Email:       normalizeEmail(email),
		CodeBinding: codeBinding(key, c.TenantID, c.Provider, c.Subject, email, code),
		Kind:        kindChallenge, Exp: now.UTC().Add(challengeTokenTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	return signToken(key, payload), nil
}

func verifyChallengeToken(key []byte, token string, now time.Time) (challengeClaims, error) {
	payload, err := verifyToken(key, token)
	if err != nil {
		return challengeClaims{}, err
	}
	var c challengeClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return challengeClaims{}, errPendingToken
	}
	if c.Kind != kindChallenge || c.TenantID <= 0 || strings.TrimSpace(c.Provider) == "" ||
		strings.TrimSpace(c.Subject) == "" || strings.TrimSpace(c.Email) == "" || strings.TrimSpace(c.CodeBinding) == "" {
		return challengeClaims{}, errPendingToken
	}
	if now.UTC().Unix() >= c.Exp {
		return challengeClaims{}, errPendingToken
	}
	return c, nil
}

func generateCode() (string, error) {
	raw := make([]byte, 5)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.EncodeToString(raw), nil
}

func normalizeCode(code string) string {
	r := strings.NewReplacer(" ", "", "-", "", "\t", "")
	return strings.ToUpper(r.Replace(strings.TrimSpace(code)))
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

// looksLikeEmail 是发码前的轻量邮箱格式闸(挡明显垃圾,真正归一/校验在 service 层)。
func looksLikeEmail(email string) bool {
	at := strings.IndexByte(email, '@')
	if at <= 0 || at != strings.LastIndexByte(email, '@') || at == len(email)-1 {
		return false
	}
	return strings.IndexByte(email[at+1:], '.') > 0 && !strings.ContainsAny(email, " \t\r\n")
}

// ---- 请求 / 响应 + glue(本包自足,不依赖 gatewayhttp)----

type sendCodeRequest struct {
	PendingToken string `json:"pending_token"`
	Email        string `json:"email"`
}

type completeRequest struct {
	ChallengeToken string         `json:"challenge_token"`
	Code           string         `json:"code"`
	DeviceInfo     map[string]any `json:"device_info,omitempty"`
}

func newSendCodeHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// EmailSender 缺失(邮件通道未配)= 码无法投递,fail-closed 停用发码:绝不签发用户永远无法
		// 完成的 challenge_token,否则会把用户静默引入「等一个永不到达的码」死局(而非报错引导)。
		if d.Auth == nil || len(d.Key) == 0 || d.EmailSender == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth pending flow not configured")
			return
		}
		var req sendCodeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		claims, err := verifyPendingToken(d.Key, req.PendingToken, d.Auth.Clock())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "oauth_pending_token_invalid", "pending token is invalid or expired")
			return
		}
		email := normalizeEmail(req.Email)
		if !looksLikeEmail(email) {
			writeJSONError(w, http.StatusBadRequest, "invalid_email", "a valid email is required")
			return
		}
		code, err := generateCode()
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "failed to generate code")
			return
		}
		challengeToken, err := mintChallengeToken(d.Key, claims, email, code, d.Auth.Clock())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "failed to issue challenge")
			return
		}
		// EmailSender 已由入口守卫保证非 nil;发码失败即 fail-closed 报错,不返 challenge_token。
		if err := d.EmailSender.SendOAuthEmailCode(r.Context(), claims.TenantID, email, code); err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "email_send_failed", "failed to send verification code")
			return
		}
		d.recordEvent(r.Context(), "oauth_pending_email_code_sent", claims.TenantID, 0, claims.Provider, "success", "")
		writeJSON(w, http.StatusOK, map[string]any{"status": "code_sent", "challenge_token": challengeToken})
	}
}

func newCompleteHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Sessions == nil || len(d.Key) == 0 {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "oauth pending flow not configured")
			return
		}
		var req completeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		claims, err := verifyChallengeToken(d.Key, req.ChallengeToken, d.Auth.Clock())
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "oauth_challenge_token_invalid", "challenge token is invalid or expired")
			return
		}
		// 重算码指纹与 token 内绑定做**恒时**比对:相等才证明持有发到邮箱的那个码。
		want := codeBinding(d.Key, claims.TenantID, claims.Provider, claims.Subject, claims.Email, req.Code)
		if !hmac.Equal([]byte(want), []byte(claims.CodeBinding)) {
			d.recordEvent(r.Context(), "oauth_pending_email_code_failed", claims.TenantID, 0, claims.Provider, "failure", "code_mismatch")
			writeJSONError(w, http.StatusBadRequest, "oauth_code_invalid", "verification code is incorrect")
			return
		}
		identity := userauth.VerifiedIdentity{
			Provider: claims.Provider, Subject: claims.Subject,
			DisplayName: claims.DisplayName, Email: claims.Email, EmailVerified: true,
		}
		user, err := d.Auth.CompleteSocialSignupWithVerifiedEmail(r.Context(), claims.TenantID, identity, claims.Email)
		if err != nil {
			writeSignupError(w, err)
			return
		}
		var ip string
		if d.ClientIP != nil {
			ip = d.ClientIP.ClientIP(r)
		}
		tokens, err := d.Sessions.Create(r.Context(), usersession.CreateInput{
			TenantID: user.TenantID, UserID: user.ID, DeviceInfo: req.DeviceInfo,
			IP: ip, UserAgent: r.UserAgent(), AuthMethod: claims.Provider,
		})
		if err != nil {
			// 全新账号首个设备,设备策略不会触发确认;此处为真实后端故障。
			writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "failed to issue session")
			return
		}
		d.recordEvent(r.Context(), "oauth_pending_email_signup_succeeded", user.TenantID, user.ID, claims.Provider, "success", "")
		writeJSON(w, http.StatusOK, map[string]any{"user": publicUser(user), "session": tokens})
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// writeSignupError 映射 CompleteSocialSignupWithVerifiedEmail 的错误到 HTTP(与 gatewayhttp.writeAuthError 同类语义)。
func writeSignupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userauth.ErrEmailExists):
		writeJSONError(w, http.StatusConflict, "email_exists", "an account with this email already exists")
	case errors.Is(err, userauth.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_auth_request", "auth request is invalid")
	case errors.Is(err, userauth.ErrRegistrationDisabled):
		writeJSONError(w, http.StatusForbidden, "registration_disabled", "public registration is disabled")
	case errors.Is(err, userauth.ErrInviteRequired):
		writeJSONError(w, http.StatusForbidden, "invite_required", "invite code is required")
	case errors.Is(err, userauth.ErrSocialLoginRejected):
		writeJSONError(w, http.StatusForbidden, "social_login_rejected", "social identity signup is not permitted")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "auth_backend_error", "auth backend transient failure")
	}
}

func publicUser(user userauth.User) map[string]any {
	return map[string]any{
		"id": user.ID, "tenant_id": user.TenantID, "email": user.Email,
		"display_name": user.DisplayName, "email_verified": user.EmailVerified,
		"social_login_provider": user.SocialLoginProvider, "status": user.Status,
		"created_at": user.CreatedAt, "updated_at": user.UpdatedAt,
	}
}
