// Package config 从环境变量加载 HUAKAI gateway 的运行时配置。
// YAML 支持暂缓, 等到出现多部署场景的需求时再做。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyns"
)

// Config 是 gateway 启动时所需全部配置的类型化快照。
// 所有字段都来自 env 变量;缺少必填字段 → 返回类型化错误。
// 对安全敏感的值绝不使用静默默认值。
type Config struct {
	// DatabaseURL 是 PostgreSQL 的 DSN。必填。
	DatabaseURL string

	// DBMaxConns 可选地覆盖 Postgres 连接池的最大连接数。
	// 为零时沿用 db 包的默认值(16);可由运维按扩容需求调整。
	DBMaxConns int32
	// DBMinConns 可选地覆盖连接池的最小连接数。为零时沿用默认值(2)。
	DBMinConns int32
	// DBMaxConnLifetime 可选地覆盖连接池中连接的最大存活时长
	//(HUAKAI_DB_MAX_CONN_LIFETIME_SECONDS)。为零时沿用默认值(30m)。
	DBMaxConnLifetime time.Duration
	// DBMaxConnIdleTime 可选地覆盖连接池中连接的最大空闲时长
	//(HUAKAI_DB_MAX_CONN_IDLE_TIME_SECONDS)。为零时沿用默认值(5m)。
	DBMaxConnIdleTime time.Duration

	// Listen 是 HTTP 绑定地址(例如 ":8080", 测试中用 ":0")。
	Listen string

	// BillingPolicyVersion 会记录到每一条 claim 行上。
	BillingPolicyVersion string

	// RequestClass 给 claim 打标, 供下游策略路由使用。默认 "standard"。
	RequestClass string

	// TransportSidecarSocket 让 mimicry 传输模式指向本地 TLS sidecar 的 Unix
	// socket。为空时沿用现有的 Go uTLS 路径。
	TransportSidecarSocket string
	// TransportSidecarFallback 是显式的 opt-in。默认 false, 当 Rust sidecar 已配置
	// 但不可用时, 让生产保持 fail-closed。
	TransportSidecarFallback bool
	// TransportSidecarForceH1 控制 Rust sidecar 握手是否只广告 ALPN=http/1.1。
	// nil(env 未设)时沿用 transport 层默认(默认强制 H1,与 Go uTLS 路一致);
	// 非 nil 时由运维 HUAKAI_TRANSPORT_FORCE_H1 显式覆盖(仅 "false" 关)。
	TransportSidecarForceH1 *bool

	// QuotaEnforce 把配额预留/结清路径接入 chat 准入。
	// 默认 false, 不改动热路径。
	QuotaEnforce bool
	// Budget 接入每分钟 RPM/TPM 预算跟踪。默认关闭, 不改动热路径;
	// 启用时失败模式默认为 memory_fallback。
	Budget BudgetConfig

	// VendorOAuth 持有运维提供的、供 vendor refresher 使用的 OAuth 刷新配置。
	// TokenURL 为空表示该 vendor refresher 未接线。
	VendorOAuth VendorOAuthConfigs

	// CredentialAcqBootstrap*TTL 可选地覆盖凭证获取的 OAuth bootstrap 窗口。
	// 为零时取 credentialacq 包的默认值。
	CredentialAcqBootstrapShortTTL time.Duration
	CredentialAcqBootstrapLongTTL  time.Duration

	// PaymentHMACSecrets 把支付 provider 名映射到 webhook HMAC secret。
	// 取值来自 HUAKAI_PAYMENT_HMAC_SECRETS, 绝不可写入日志。
	PaymentHMACSecrets map[string]string
	PaymentEnableMock  bool
	// 淘宝/闲鱼 manual-redirect 支付: 默认关闭。启用后下单返回 checkout_url + 订单号,
	// 用户到淘宝/闲鱼扫码/点链接付款, 管理员手动确认入账(无程序回调)。
	PaymentTaobaoEnabled         bool
	PaymentTaobaoCheckoutURL     string
	PaymentExpireSweepInterval   time.Duration
	PaymentExpireSweepBatchLimit int
	// APIKeyExpirySweep* 接入入站 API-key 显示状态过期 worker。
	// 默认启用, 配有限制的 ticker;设 enabled=false 可暂停它。
	APIKeyExpirySweepEnabled    bool
	APIKeyExpirySweepInterval   time.Duration
	APIKeyExpirySweepBatchLimit int

	// CacheAnthropicAutoBreakpoints 在实时的 Anthropic Messages 出站路径上开启
	// 自动的 cache_control 断点规划(HUAKAI_CACHE_ANTHROPIC_AUTO_BREAKPOINTS)。
	// 默认 false, 让出站 body 逐字节保持原样。为 true 时, dispatcher 仅对那些
	// 未携带客户端自带 cache_control 的 anthropic_messages 请求注入临时断点;
	// 自行管理缓存的客户端绝不受影响。
	CacheAnthropicAutoBreakpoints bool

	// AlertingEvalEnabled 接入告警规则评估器的后台循环。
	// 默认 false, 让新建的告警引擎仅保持 CRUD, 直到运维显式开启实时评估。
	AlertingEvalEnabled bool
	// AlertingEvalInterval 限制评估器 ticker 的频率。默认 60s。
	AlertingEvalInterval time.Duration
}

type BudgetConfig struct {
	Enabled    bool
	FailMode   string
	RedisURL   string
	DefaultRPM int64
	DefaultTPM int64
}

const (
	DefaultBillingPolicyVersion         = "1.0"
	VendorOAuthCursor                   = "cursor"
	VendorOAuthWindsurf                 = "windsurf"
	VendorOAuthOpenAICodex              = "openai_codex"
	VendorOAuthKiro                     = "kiro"
	VendorOAuthGemini                   = "gemini"
	DefaultPaymentExpireSweepInterval   = time.Minute
	DefaultPaymentExpireSweepBatchLimit = 200
	DefaultAPIKeyExpirySweepInterval    = 5 * time.Minute
	DefaultAPIKeyExpirySweepBatchLimit  = 500
	vendorOAuthAuthURL                  = "AUTH_URL"
	vendorOAuthTokenURL                 = "TOKEN_URL"
	vendorOAuthClientID                 = "CLIENT_ID"
	vendorOAuthClientSecret             = "CLIENT_SECRET"
	vendorOAuthScope                    = "SCOPE"
)

// VendorOAuth 是一份运维提供的 OAuth client 配置。
type VendorOAuth struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
	AuthURL      string
}

// VendorOAuthConfigs 以 vendor 名为键:
// cursor、windsurf、openai_codex、kiro、gemini。
type VendorOAuthConfigs map[string]VendorOAuth

// ErrMissingRequired 表示有一个或多个必填 env 变量未设置。
var ErrMissingRequired = errors.New("config: missing required env var")

// Load 把 env 变量读入 Config。必填变量:HUAKAI_DATABASE_URL。
//
// 已移除 Smoke* 字段——改用基于 api_keys 表的入站鉴权
//(auth.APIKeyResolver)。要回滚到 env 注入式鉴权需改代码回退
//(没有 build-tag 这类逃生出口)。
func Load() (*Config, error) {
	// 客户 API key 前缀(HUAKAI_API_KEY_PREFIX,默认 hk):若运维设了非法值,启动期
	// fail-loud,免得静默回落默认后所有客户端被拒还查不出原因。空=用默认,无误。
	if err := apikeyns.ConfiguredBaseError(); err != nil {
		return nil, err
	}
	paymentHMACSecrets, err := loadPaymentHMACSecrets()
	if err != nil {
		return nil, err
	}
	paymentEnableMock, err := envBool("HUAKAI_PAYMENT_ENABLE_MOCK")
	if err != nil {
		return nil, err
	}
	paymentTaobaoEnabled, err := envBool("HUAKAI_PAYMENT_TAOBAO_ENABLED")
	if err != nil {
		return nil, err
	}
	paymentExpireSweepInterval, err := envOptionalDuration("HUAKAI_PAYMENT_EXPIRE_SWEEP_INTERVAL")
	if err != nil {
		return nil, err
	}
	paymentExpireSweepBatchLimit, err := envPositiveIntDefault("HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT", DefaultPaymentExpireSweepBatchLimit)
	if err != nil {
		return nil, err
	}
	apiKeyExpirySweepEnabled, err := envBoolDefault("HUAKAI_API_KEY_EXPIRY_SWEEP_ENABLED", true)
	if err != nil {
		return nil, err
	}
	apiKeyExpirySweepInterval, err := envPositiveDurationDefault("HUAKAI_API_KEY_EXPIRY_SWEEP_INTERVAL", DefaultAPIKeyExpirySweepInterval)
	if err != nil {
		return nil, err
	}
	apiKeyExpirySweepBatchLimit, err := envPositiveIntDefault("HUAKAI_API_KEY_EXPIRY_SWEEP_BATCH_LIMIT", DefaultAPIKeyExpirySweepBatchLimit)
	if err != nil {
		return nil, err
	}
	transportSidecarFallback, err := envBool("HUAKAI_TRANSPORT_SIDECAR_FALLBACK")
	if err != nil {
		return nil, err
	}
	// BILL-121/123:既然引擎已被证明安全, 配额强制执行现在默认开启——它在未配置
	// 任何策略时直接 no-op(跳过评估), 让 observe 模式策略保持非阻塞, 并在基础设施
	// 错误时 fail OPEN, 所以默认开启不会阻断一个未配置的部署。运维仍可设
	// HUAKAI_QUOTA_ENFORCE=false 作为逃生出口。
	quotaEnforce, err := envBoolDefault("HUAKAI_QUOTA_ENFORCE", true)
	if err != nil {
		return nil, err
	}
	cacheAnthropicAutoBreakpoints, err := envBool("HUAKAI_CACHE_ANTHROPIC_AUTO_BREAKPOINTS")
	if err != nil {
		return nil, err
	}
	alertingEvalEnabled, err := envBool("HUAKAI_ALERTING_EVAL_ENABLED")
	if err != nil {
		return nil, err
	}
	alertingEvalInterval, err := envOptionalDurationSeconds("HUAKAI_ALERTING_EVAL_INTERVAL_SECONDS")
	if err != nil {
		return nil, err
	}
	if alertingEvalInterval == 0 {
		alertingEvalInterval = time.Minute
	}
	budgetCfg, err := loadBudgetConfig()
	if err != nil {
		return nil, err
	}
	credentialAcqBootstrapShortTTL, err := envOptionalDurationSeconds("HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_SHORT_TTL_SECONDS")
	if err != nil {
		return nil, err
	}
	credentialAcqBootstrapLongTTL, err := envOptionalDurationSeconds("HUAKAI_CREDENTIAL_ACQ_BOOTSTRAP_LONG_TTL_SECONDS")
	if err != nil {
		return nil, err
	}
	dbMaxConns, err := envOptionalInt32("HUAKAI_DB_MAX_CONNS")
	if err != nil {
		return nil, err
	}
	dbMinConns, err := envOptionalInt32("HUAKAI_DB_MIN_CONNS")
	if err != nil {
		return nil, err
	}
	dbMaxConnLifetime, err := envOptionalDurationSeconds("HUAKAI_DB_MAX_CONN_LIFETIME_SECONDS")
	if err != nil {
		return nil, err
	}
	dbMaxConnIdleTime, err := envOptionalDurationSeconds("HUAKAI_DB_MAX_CONN_IDLE_TIME_SECONDS")
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		DatabaseURL:                    os.Getenv("HUAKAI_DATABASE_URL"),
		Listen:                         envDefault("HUAKAI_ADDR", ":8080"),
		BillingPolicyVersion:           envDefault("HUAKAI_BILLING_POLICY_VERSION", DefaultBillingPolicyVersion),
		RequestClass:                   envDefault("HUAKAI_REQUEST_CLASS", "standard"),
		TransportSidecarSocket:         os.Getenv("HUAKAI_TRANSPORT_SIDECAR_SOCKET"),
		TransportSidecarFallback:       transportSidecarFallback,
		TransportSidecarForceH1:        envOptionalForceH1("HUAKAI_TRANSPORT_FORCE_H1"),
		QuotaEnforce:                   quotaEnforce,
		Budget:                         budgetCfg,
		VendorOAuth:                    loadVendorOAuthConfigs(),
		CredentialAcqBootstrapShortTTL: credentialAcqBootstrapShortTTL,
		CredentialAcqBootstrapLongTTL:  credentialAcqBootstrapLongTTL,
		PaymentHMACSecrets:             paymentHMACSecrets,
		PaymentEnableMock:              paymentEnableMock,
		PaymentTaobaoEnabled:           paymentTaobaoEnabled,
		PaymentTaobaoCheckoutURL:       strings.TrimSpace(os.Getenv("HUAKAI_PAYMENT_TAOBAO_CHECKOUT_URL")),
		PaymentExpireSweepInterval:     paymentExpireSweepInterval,
		PaymentExpireSweepBatchLimit:   paymentExpireSweepBatchLimit,
		APIKeyExpirySweepEnabled:       apiKeyExpirySweepEnabled,
		APIKeyExpirySweepInterval:      apiKeyExpirySweepInterval,
		APIKeyExpirySweepBatchLimit:    apiKeyExpirySweepBatchLimit,
		CacheAnthropicAutoBreakpoints:  cacheAnthropicAutoBreakpoints,
		AlertingEvalEnabled:            alertingEvalEnabled,
		AlertingEvalInterval:           alertingEvalInterval,
		DBMaxConns:                     dbMaxConns,
		DBMinConns:                     dbMinConns,
		DBMaxConnLifetime:              dbMaxConnLifetime,
		DBMaxConnIdleTime:              dbMaxConnIdleTime,
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("%w: HUAKAI_DATABASE_URL", ErrMissingRequired)
	}
	return cfg, nil
}

func loadBudgetConfig() (BudgetConfig, error) {
	enabled, err := envBool("HUAKAI_BUDGET_ENABLED")
	if err != nil {
		return BudgetConfig{}, err
	}
	failMode := strings.TrimSpace(os.Getenv("HUAKAI_BUDGET_FAIL_MODE"))
	if failMode == "" {
		failMode = "memory_fallback"
	}
	switch failMode {
	case "open", "closed", "memory_fallback":
	default:
		return BudgetConfig{}, fmt.Errorf("HUAKAI_BUDGET_FAIL_MODE must be open, closed, or memory_fallback, got %q", failMode)
	}
	rpm, err := envNonNegativeInt64("HUAKAI_BUDGET_DEFAULT_RPM")
	if err != nil {
		return BudgetConfig{}, err
	}
	tpm, err := envNonNegativeInt64("HUAKAI_BUDGET_DEFAULT_TPM")
	if err != nil {
		return BudgetConfig{}, err
	}
	return BudgetConfig{
		Enabled:    enabled,
		FailMode:   failMode,
		RedisURL:   firstNonEmptyEnv("HUAKAI_BUDGET_REDIS_URL", "HUAKAI_REDIS_URL"),
		DefaultRPM: rpm,
		DefaultTPM: tpm,
	}, nil
}

func (configs VendorOAuthConfigs) Configured() VendorOAuthConfigs {
	out := VendorOAuthConfigs{}
	for _, vendor := range []string{
		VendorOAuthCursor,
		VendorOAuthWindsurf,
		VendorOAuthOpenAICodex,
		VendorOAuthKiro,
		VendorOAuthGemini,
	} {
		cfg := configs[vendor].normalized()
		if cfg.TokenURL == "" {
			continue
		}
		out[vendor] = cfg
	}
	return out
}

func (cfg VendorOAuth) normalized() VendorOAuth {
	return VendorOAuth{
		TokenURL:     strings.TrimSpace(cfg.TokenURL),
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Scope:        strings.TrimSpace(cfg.Scope),
		AuthURL:      strings.TrimSpace(cfg.AuthURL),
	}
}

func loadVendorOAuthConfigs() VendorOAuthConfigs {
	return VendorOAuthConfigs{
		VendorOAuthCursor:      loadVendorOAuth("HUAKAI_CURSOR_OAUTH"),
		VendorOAuthWindsurf:    loadVendorOAuth("HUAKAI_WINDSURF_OAUTH"),
		VendorOAuthOpenAICodex: loadVendorOAuth("HUAKAI_OPENAI_CODEX_OAUTH"),
		VendorOAuthKiro:        loadVendorOAuth("HUAKAI_KIRO_OAUTH"),
		VendorOAuthGemini:      loadVendorOAuth("HUAKAI_GEMINI_OAUTH"),
	}
}

func loadVendorOAuth(prefix string) VendorOAuth {
	return VendorOAuth{
		AuthURL:      envTrim(prefix + "_" + vendorOAuthAuthURL),
		TokenURL:     envTrim(prefix + "_" + vendorOAuthTokenURL),
		ClientID:     envTrim(prefix + "_" + vendorOAuthClientID),
		ClientSecret: envTrim(prefix + "_" + vendorOAuthClientSecret),
		Scope:        envTrim(prefix + "_" + vendorOAuthScope),
	}
}

func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func envTrim(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func loadPaymentHMACSecrets() (map[string]string, error) {
	raw := strings.TrimSpace(os.Getenv("HUAKAI_PAYMENT_HMAC_SECRETS"))
	secrets := map[string]string{}
	if raw == "" {
		return secrets, nil
	}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, secret, ok := strings.Cut(item, "=")
		if !ok {
			name, secret, ok = strings.Cut(item, ":")
		}
		name = strings.ToLower(strings.TrimSpace(name))
		secret = strings.TrimSpace(secret)
		if !ok || name == "" || secret == "" {
			return nil, fmt.Errorf("HUAKAI_PAYMENT_HMAC_SECRETS entry %q must be provider=secret", item)
		}
		secrets[name] = secret
	}
	return secrets, nil
}

func envBool(name string) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", name, raw, err)
	}
	return v, nil
}

func envBoolDefault(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q: %w", name, raw, err)
	}
	return v, nil
}

// envOptionalForceH1 解析 HUAKAI_TRANSPORT_FORCE_H1,语义与 mimicry.forceH1Enabled
// 完全一致:env 未设时返回 nil(由 transport 层 env 默认决定,默认开),已设时返回
// 显式 *bool——非 "false" 一律视为开,仅显式 "false" 关。返回指针是为了区分"未配置"
// 与"显式 false",避免运维想关却被默认开覆盖。
func envOptionalForceH1(name string) *bool {
	raw, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	enabled := strings.TrimSpace(raw) != "false"
	return &enabled
}

func envOptionalDurationSeconds(name string) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be positive seconds, got %q: %w", name, raw, err)
		}
		return 0, fmt.Errorf("%s must be positive seconds, got %q", name, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envPositiveDurationDefault(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("%s must be positive duration, got %q", name, raw)
		}
		return d, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 60s or positive seconds, got %q: %w", name, raw, err)
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be positive duration, got %q", name, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envOptionalDuration(name string) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" || raw == "0" {
		return 0, nil
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return 0, fmt.Errorf("%s must be non-negative duration, got %q", name, raw)
		}
		return d, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration like 60s or positive seconds, got %q: %w", name, raw, err)
	}
	if seconds < 0 {
		return 0, fmt.Errorf("%s must be non-negative duration, got %q", name, raw)
	}
	return time.Duration(seconds) * time.Second, nil
}

func envNonNegativeInt64(name string) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be non-negative integer, got %q: %w", name, raw, err)
		}
		return 0, fmt.Errorf("%s must be non-negative integer, got %q", name, raw)
	}
	return value, nil
}

func envPositiveIntDefault(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be positive integer, got %q: %w", name, raw, err)
		}
		return 0, fmt.Errorf("%s must be positive integer, got %q", name, raw)
	}
	return value, nil
}

// envOptionalInt32 从 env 解析一个非负的 int32。未设置/为空 -> 0, 让调用方把 0
// 当作"使用包默认值"。非法或越界 -> 返回类型化错误, 让配置错误在启动时显式失败,
// 而不是静默降级。
func envOptionalInt32(name string) (int32, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q: %w", name, raw, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d", name, v)
	}
	return int32(v), nil
}
