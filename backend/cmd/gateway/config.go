package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	runtimeconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	mailinfra "github.com/BloomingProsperity/HUAKAI/internal/email"
	"github.com/BloomingProsperity/HUAKAI/internal/eventbus"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp"
	obsoutbox "github.com/BloomingProsperity/HUAKAI/internal/obs/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

type Config = runtimeconfig.Config

func loadGatewayConfig(logger *zap.Logger) (*Config, error) {
	cfg, err := runtimeconfig.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	logger.Info("config loaded",
		zap.String("listen", cfg.Listen),
		zap.String("auth_mode", "api_keys_table"),
	)
	return cfg, nil
}

func buildAuthEmailSender(_ *Config, store mailinfra.SettingsStore, keys credentialstore.KeyProvider, logger *zap.Logger, outbox obsoutbox.Outbox) (gatewayhttp.AuthEmailSender, error) {
	sender, err := mailinfra.BuildEmailSender(context.Background(), store, keys, mailinfra.WithOutbox(outbox))
	if err != nil {
		return nil, err
	}
	if logger != nil && strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_DEV_AUTH_RETURN_TOKEN")), "true") {
		logger.Warn("dev mode, do not enable in production", zap.String("env", "HUAKAI_DEV_AUTH_RETURN_TOKEN"))
	}
	return sender, nil
}

func releaseModeProduction() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE")), "production")
}

// validateReleaseMode 在启动时校验 HUAKAI_RELEASE_MODE。
// 历史约定:空/未设 = dev(测试与本地依赖此默认)。问题(S1-019):设置了但拼错的值
// (如 "prod"、"Production!"、"prd")会被 releaseModeProduction() 的精确匹配判 false,
// 静默降级为 dev —— 绕过 postgres ledger / 持久私钥 / email / channelhealth signer 等全部
// production fail-closed 门控,而服务照常启动、无任何报错提示运维。这里改为:非空但非已知
// 取值即启动失败,杜绝"想上生产却因打错字静默跑在 dev"。空值仍按 dev 放行,不破坏既有约定。
func validateReleaseMode() error {
	raw := strings.TrimSpace(os.Getenv("HUAKAI_RELEASE_MODE"))
	switch strings.ToLower(raw) {
	case "", "dev", "development", "test", "production":
		return nil
	default:
		return fmt.Errorf("HUAKAI_RELEASE_MODE=%q 不是已知取值（production / development / dev / test / 空=dev）；拒绝静默降级为 dev", raw)
	}
}

func releaseMode() eventbus.ReleaseMode {
	if releaseModeProduction() {
		return eventbus.ReleaseModeProduction
	}
	return eventbus.ReleaseModeDev
}

// validateDevAuthTokenFlag 在启动时 fail-closed:HUAKAI_DEV_AUTH_RETURN_TOKEN=true 会让公开的
// 注册/密码重置接口把一次性明文 secret 直接回写进 JSON 响应体(addDevAuthToken),仅供本地/CI 调试。
// 生产环境(HUAKAI_RELEASE_MODE=production)绝不能开启,否则每次注册/重置都会泄露令牌(S1-018)。
// 与 S1-019 同族:把该 dev 开关纳入与 audit/email/channelhealth 一致的启动门控,带病配置直接拒启,
// 而非此前仅打一条 warning 仍照常 boot 并泄露。
func validateDevAuthTokenFlag() error {
	if releaseModeProduction() && strings.EqualFold(strings.TrimSpace(os.Getenv("HUAKAI_DEV_AUTH_RETURN_TOKEN")), "true") {
		return fmt.Errorf("HUAKAI_DEV_AUTH_RETURN_TOKEN 不得在 production 下为 true（会在注册/重置响应中泄露明文一次性令牌）")
	}
	return nil
}

func requireProductionChannelHealthSigner(signer *sign.Signer) error {
	if !releaseModeProduction() {
		return nil
	}
	if signer == nil {
		return fmt.Errorf("production 模式要求 channelhealth audit signer：请设置 HUAKAI_AUDIT_PRIVATE_KEY_PATH")
	}
	return nil
}

func loadCredentialKeyProvider() (credentialstore.KeyProvider, error) {
	keyID := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_ID"))
	if keyID == "" {
		keyID = "local-v1"
	}
	raw := strings.TrimSpace(os.Getenv("HUAKAI_CREDENTIAL_KEY_B64"))
	if raw == "" {
		return nil, fmt.Errorf("%w: HUAKAI_CREDENTIAL_KEY_B64", credentialstore.ErrKeyUnavailable)
	}
	material, err := credentialstore.DecodeKeyMaterial(raw)
	if err != nil {
		return nil, err
	}
	return credentialstore.NewStaticKeyProvider(keyID, material)
}

func loadSessionSigningKey() ([]byte, error) {
	b64Names := []string{"HUAKAI_SESSION_SIGNING_KEY_B64", "HUAKAI_SESSION_HMAC_KEY_B64"}
	for _, name := range b64Names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		for _, decode := range []func(string) ([]byte, error){
			base64.StdEncoding.DecodeString,
			base64.RawStdEncoding.DecodeString,
			base64.RawURLEncoding.DecodeString,
		} {
			key, err := decode(raw)
			if err == nil && len(key) >= 32 {
				return key, nil
			}
		}
		return nil, fmt.Errorf("%s must decode to at least 32 bytes", name)
	}
	if raw := strings.TrimSpace(os.Getenv("HUAKAI_SESSION_SIGNING_KEY_HEX")); raw != "" {
		key, err := hex.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("decode HUAKAI_SESSION_SIGNING_KEY_HEX: %w", err)
		}
		if len(key) < 32 {
			return nil, fmt.Errorf("HUAKAI_SESSION_SIGNING_KEY_HEX must decode to at least 32 bytes")
		}
		return key, nil
	}
	return nil, fmt.Errorf("HUAKAI_SESSION_SIGNING_KEY_B64 or HUAKAI_SESSION_SIGNING_KEY_HEX is required")
}

func buildUserOAuthService(logger *zap.Logger) *userauth.OAuthService {
	providers := make([]userauth.OAuthProvider, 0, 2)
	if p := buildOAuthProvider(logger, userauth.OAuthConfig{
		Provider:     userauth.SocialProviderGoogle,
		ClientID:     os.Getenv("HUAKAI_GOOGLE_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("HUAKAI_GOOGLE_OAUTH_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("HUAKAI_GOOGLE_OAUTH_REDIRECT_URI"),
		AuthURL:      os.Getenv("HUAKAI_GOOGLE_OAUTH_AUTH_URL"),
		TokenURL:     envDefault("HUAKAI_GOOGLE_OAUTH_TOKEN_URL", "https://oauth2.googleapis.com/token"),
		JWKSURL:      envDefault("HUAKAI_GOOGLE_OAUTH_JWKS_URL", "https://www.googleapis.com/oauth2/v3/certs"),
		Issuer:       envDefault("HUAKAI_GOOGLE_OAUTH_ISSUER", "https://accounts.google.com"),
	}); p != nil {
		providers = append(providers, p)
	}
	if p := buildOAuthProvider(logger, userauth.OAuthConfig{
		Provider:     userauth.SocialProviderGitHub,
		ClientID:     os.Getenv("HUAKAI_GITHUB_OAUTH_CLIENT_ID"),
		ClientSecret: os.Getenv("HUAKAI_GITHUB_OAUTH_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("HUAKAI_GITHUB_OAUTH_REDIRECT_URI"),
		AuthURL:      os.Getenv("HUAKAI_GITHUB_OAUTH_AUTH_URL"),
		TokenURL:     envDefault("HUAKAI_GITHUB_OAUTH_TOKEN_URL", "https://github.com/login/oauth/access_token"),
		UserURL:      envDefault("HUAKAI_GITHUB_OAUTH_USER_URL", "https://api.github.com/user"),
		EmailsURL:    envDefault("HUAKAI_GITHUB_OAUTH_EMAILS_URL", "https://api.github.com/user/emails"),
	}); p != nil {
		providers = append(providers, p)
	}
	return userauth.NewOAuthService(providers...)
}

func buildOAuthProvider(logger *zap.Logger, cfg userauth.OAuthConfig) userauth.OAuthProvider {
	if strings.TrimSpace(cfg.ClientID) == "" {
		if logger != nil {
			logger.Info("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.String("reason", "client_id_missing"))
		}
		return nil
	}
	p, err := userauth.NewOAuthHTTPProvider(cfg, http.DefaultClient)
	if err != nil {
		if logger != nil {
			logger.Warn("user oauth provider disabled", zap.String("provider", cfg.Provider), zap.Error(err))
		}
		return nil
	}
	return p
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// buildAuditLedger 根据 HUAKAI_AUDIT_LEDGER_BACKEND 构建 audit ledger。
//   - memory (默认 / dev): 内存版，重启丢失，打 warn 日志
//   - postgres: PostgresLedger，持久化；production 模式强制使用此后端
func buildAuditLedger(_ context.Context, pgPool *pgxpool.Pool, signer *sign.Signer, logger *zap.Logger) (auditledger.Ledger, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_LEDGER_BACKEND")))
	isProd := releaseModeProduction()
	if isProd && backend != "postgres" {
		return nil, fmt.Errorf("production 模式要求 HUAKAI_AUDIT_LEDGER_BACKEND=postgres，当前值: %q", backend)
	}
	if backend == "postgres" {
		l, err := auditledger.NewPostgresLedger(pgPool, signer)
		if err != nil {
			return nil, fmt.Errorf("NewPostgresLedger: %w", err)
		}
		logger.Info("audit ledger backend: postgres（持久化）")
		return l, nil
	}
	// 默认 memory（dev 模式）
	logger.Warn("audit ledger backend: memory — 重启后 audit 链丢失，仅适用于开发环境")
	return auditledger.NewMemoryLedger(signer)
}

func loadAuditSigner(logger *zap.Logger) (*sign.Signer, error) {
	path := strings.TrimSpace(os.Getenv("HUAKAI_AUDIT_PRIVATE_KEY_PATH"))
	isProd := releaseModeProduction()
	if path == "" {
		if isProd {
			return nil, fmt.Errorf("production 模式要求持久私钥：请设置 HUAKAI_AUDIT_PRIVATE_KEY_PATH")
		}
		logger.Warn("using ephemeral key — restart loses chain")
		return sign.GenerateKey()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read HUAKAI_AUDIT_PRIVATE_KEY_PATH: %w", err)
	}
	signer, err := parseAuditPrivateKey(raw)
	if err != nil {
		return nil, err
	}
	logger.Info("audit private key loaded", zap.String("path", path), zap.String("fingerprint", signer.Fingerprint()))
	return signer, nil
}

func parseAuditPrivateKey(raw []byte) (*sign.Signer, error) {
	if len(raw) == ed25519.PrivateKeySize {
		return sign.NewSignerFromKey(ed25519.PrivateKey(raw))
	}
	trimmed := strings.TrimSpace(string(raw))
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse audit PEM private key: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, sign.ErrInvalidPrivateKey
		}
		return sign.NewSignerFromKey(priv)
	}
	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		hex.DecodeString,
	} {
		decoded, err := decode(trimmed)
		if err == nil && len(decoded) == ed25519.PrivateKeySize {
			return sign.NewSignerFromKey(ed25519.PrivateKey(decoded))
		}
	}
	return nil, sign.ErrInvalidPrivateKey
}
