package credentialworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	appconfig "github.com/BloomingProsperity/HUAKAI/internal/config"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	providerantigravity "github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
	providercopilot "github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	providerwindsurf "github.com/BloomingProsperity/HUAKAI/internal/provider/windsurf"
)

var (
	ErrNoRefreshRequired          = errors.New("credentialworker: no refresh required")
	ErrOperatorOAuthConfigMissing = errors.New("credentialworker: operator OAuth config required")
)

const (
	geminiOAuthTokenURLEnv     = "HUAKAI_GEMINI_OAUTH_TOKEN_URL"
	geminiOAuthClientIDEnv     = "HUAKAI_GEMINI_OAUTH_CLIENT_ID"
	geminiOAuthClientSecretEnv = credentialacq.GeminiPublicCLISecretEnv

	// ineffectiveRefreshBackoff 在以下情形施加:刷新"成功"但得到的 token 仍然立即
	// 又到了需要刷新的时刻(上游返回了一个接近过期的 token),或者根本不需要刷新。
	// 它防止对上游 provider 形成紧密的重试循环。
	ineffectiveRefreshBackoff = credentialstore.IneffectiveRefreshBackoff
)

type ModeRefreshInput struct {
	CredentialID      int64
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	AuthMode          string
	Payload           []byte
	Now               time.Time
	ProbeModel        string
}

type ModeRefreshResult struct {
	Payload         []byte
	AccessExpiresAt time.Time
	Outcome         string
}

type ModeRefreshAdapter interface {
	RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error)
}

type ModeAdapterRegistry struct {
	adapters map[string]ModeRefreshAdapter
}

func NewModeAdapterRegistry() *ModeAdapterRegistry {
	return &ModeAdapterRegistry{adapters: map[string]ModeRefreshAdapter{}}
}

func DefaultModeAdapterRegistry() *ModeAdapterRegistry {
	// 生产:operator OAuth 路径不注入 client(nil),经 GeminiRefresh.httpClient() 回退到
	// auth.NewSSRFProtectedOAuthClient(拨号层校验目标 IP、禁代理、禁 3xx)。
	// newDefaultModeAdapterRegistryWithProjectResolver 的 operatorOAuthClient 仅供测试注入
	// mock —— SSRF 防护拨号会丢弃自定义 RoundTripper,无法用 http.DefaultClient mock 驱动
	// operator OAuth 刷新逻辑。
	return DefaultModeAdapterRegistryWithProjectResolver(&providerantigravity.ProjectResolver{})
}

func DefaultModeAdapterRegistryWithProjectResolver(resolver adapters.ProjectIDResolver) *ModeAdapterRegistry {
	return newDefaultModeAdapterRegistryWithProjectResolver(nil, resolver, nil)
}

// DefaultModeAdapterRegistryWithRuntimeOAuth 把生产 OAuth 配置接入模式刷新器。
func DefaultModeAdapterRegistryWithRuntimeOAuth(configs appconfig.VendorOAuthConfigs) *ModeAdapterRegistry {
	return newDefaultModeAdapterRegistryWithProjectResolver(nil, &providerantigravity.ProjectResolver{}, configs)
}

// DefaultModeAdapterRegistryWithRuntimeDependencies 注入生产刷新所需的模式级依赖。
func DefaultModeAdapterRegistryWithRuntimeDependencies(resolver adapters.ProjectIDResolver, configs appconfig.VendorOAuthConfigs, anthropicOAuthClient *http.Client) *ModeAdapterRegistry {
	return newDefaultModeAdapterRegistryWithRuntimeDependencies(nil, resolver, configs, anthropicOAuthClient)
}

func newDefaultModeAdapterRegistryWithProjectResolver(operatorOAuthClient *http.Client, projectResolver adapters.ProjectIDResolver, configs appconfig.VendorOAuthConfigs) *ModeAdapterRegistry {
	return newDefaultModeAdapterRegistryWithRuntimeDependencies(operatorOAuthClient, projectResolver, configs, nil)
}

func newDefaultModeAdapterRegistryWithRuntimeDependencies(operatorOAuthClient *http.Client, projectResolver adapters.ProjectIDResolver, configs appconfig.VendorOAuthConfigs, anthropicOAuthClient *http.Client) *ModeAdapterRegistry {
	r := NewModeAdapterRegistry()
	register := func(vendor, authMode string, adapter ModeRefreshAdapter) {
		_ = r.Register(vendor, authMode, adapter)
	}
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey, staticModeAdapter{})
	anthropicRefresh := legacyOAuthModeAdapter{providerName: "anthropic", adapter: adapters.AnthropicRefresh{HTTPClient: anthropicOAuthClient}}
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, anthropicRefresh)
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode, anthropicRefresh)
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken, staticModeAdapter{})
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeBedrock, staticModeAdapter{})
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeVertexAnthropic, vertexSAModeAdapter{})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, staticModeAdapter{})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, newOpenAIChatGPTBuiltinOAuthModeAdapter())
	codexBuiltinRefresh := legacyOAuthModeAdapter{
		providerName: "codex",
		adapter: adapters.NewCodexRefresh(
			credentialacq.OpenAICodexOAuthTokenURL,
			credentialacq.OpenAICodexOAuthClientID,
			credentialacq.OpenAICodexOAuthRefreshScope,
			operatorOAuthClient,
		),
	}
	var codexOperatorRefresh ModeRefreshAdapter
	if cfg, ok := configs.Configured()[appconfig.VendorOAuthOpenAICodex]; ok {
		codexOperatorRefresh = legacyOAuthModeAdapter{
			providerName: "codex",
			adapter:      adapters.NewCodexRefresh(cfg.TokenURL, cfg.ClientID, cfg.Scope, operatorOAuthClient),
		}
	}
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth, codexOAuthModeAdapter{
		builtin: codexBuiltinRefresh, operator: codexOperatorRefresh,
	})
	// codex_web_oauth(authorization-code/PKCE 浏览器获取)与 codex_cli_oauth(device-code)凭据
	// 形状一致(access_token + refresh_token + id_token),共享同一 codex refresh adapter,避免 web
	// 获取的 token 因无 refresh 绑定而静默过期(auth/可用性回退)。
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth, codexBuiltinRefresh)
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent, newCodexAgentModeAdapter())
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeAzure, mockTokenExchangeAdapter{providerName: "azure"})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken, legacyOAuthModeAdapter{providerName: "openai", adapter: adapters.OpenAIRefresh{}})
	register(credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey, staticModeAdapter{})
	register(credentialstore.VendorGemini, credentialstore.AuthModeVertexSA, vertexSAModeAdapter{})
	register(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, newGeminiBuiltinClientOAuthModeAdapter("code_assist"))
	register(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne, newGeminiBuiltinClientOAuthModeAdapter("google_one"))
	// 两种 Antigravity 存储形态共享内置公开 OAuth profile；endpoint/client/scope
	// 不从凭据 payload 取值，项目标识由统一解析器补全。
	antigravityOAuth := providerantigravity.DefaultOAuthConfig()
	antigravityRefresh := legacyOAuthModeAdapter{
		providerName: "antigravity",
		adapter: adapters.AntigravityRefresh{
			Gemini: adapters.GeminiRefresh{
				Endpoint: antigravityOAuth.TokenURL, ClientID: antigravityOAuth.ClientID,
				ClientSecret: antigravityOAuth.ClientSecret, HTTPClient: operatorOAuthClient,
				TierCacheTTL: 24 * time.Hour,
			},
			ProjectResolver: projectResolver,
		},
	}
	register(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity, antigravityRefresh)
	register(credentialstore.VendorGemini, credentialstore.AuthModeOAuth, operatorOAuthModeAdapter{
		providerName: "gemini",
		configVendor: appconfig.VendorOAuthGemini,
		tokenURLName: geminiOAuthTokenURLEnv,
		clientIDName: geminiOAuthClientIDEnv,
		client:       operatorOAuthClient,
		newAdapter: func(cfg operatorOAuthConfig) RefreshAdapter {
			return adapters.GeminiRefresh{
				Endpoint: cfg.TokenEndpoint, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
				HTTPClient: cfg.HTTPClient, TierCacheTTL: 24 * time.Hour,
			}
		},
	})
	register(credentialstore.VendorCopilot, credentialstore.AuthModeCopilotOAuth, copilotOAuthModeAdapter{})
	register(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth, antigravityRefresh)
	var windsurfRefresh ModeRefreshAdapter = windsurfManualModeAdapter{adapter: adapters.WindsurfManualTokenRefresh{}}
	if cfg, ok := configs.Configured()[appconfig.VendorOAuthWindsurf]; ok {
		windsurfRefresh = legacyOAuthModeAdapter{
			providerName: credentialstore.VendorWindsurf,
			adapter: providerwindsurf.RefreshAdapter{
				TokenURL: cfg.TokenURL, ClientID: cfg.ClientID, Scope: cfg.Scope, HTTPClient: operatorOAuthClient,
			},
		}
	}
	register(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth, windsurfRefresh)
	register(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, builtinRefreshTokenModeAdapter{
		providerName: "grok", tokenURL: credentialacq.XAIOAuthTokenURL, clientID: credentialacq.XAIOAuthClientID,
	})
	register(credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth, builtinRefreshTokenModeAdapter{
		providerName: "kimi", tokenURL: credentialacq.KimiOAuthTokenURL, clientID: credentialacq.KimiOAuthClientID,
	})
	// 官 key 厂商(2026-07-02 接入):静态 api_key 无刷新语义,与 anthropic/openai 的 api_key 同构。
	for _, vendor := range []string{
		credentialstore.VendorGrok, credentialstore.VendorDeepSeek, credentialstore.VendorKimi,
		credentialstore.VendorQwen, credentialstore.VendorGLM, credentialstore.VendorYi,
		credentialstore.VendorBaichuan, credentialstore.VendorDoubao, credentialstore.VendorMiniMax,
		credentialstore.VendorErnie, credentialstore.VendorHunyuan, credentialstore.VendorStep,
	} {
		register(vendor, credentialstore.AuthModeAPIKey, staticModeAdapter{})
	}
	return r
}

func (r *ModeAdapterRegistry) Register(vendor, authMode string, adapter ModeRefreshAdapter) error {
	if r == nil {
		return errors.New("credentialworker: mode adapter registry is nil")
	}
	if adapter == nil {
		return errors.New("credentialworker: mode adapter is nil")
	}
	key := credentialstore.ModeKey(vendor, authMode)
	if key == "" {
		return errors.New("credentialworker: mode adapter key is empty")
	}
	if _, exists := r.adapters[key]; exists {
		return fmt.Errorf("credentialworker: mode adapter already registered: %s", key)
	}
	r.adapters[key] = adapter
	return nil
}

func (r *ModeAdapterRegistry) Lookup(vendor, authMode string) (ModeRefreshAdapter, bool) {
	if r == nil {
		return nil, false
	}
	adapter, ok := r.adapters[credentialstore.ModeKey(vendor, authMode)]
	return adapter, ok
}

func (r *ModeAdapterRegistry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		out = append(out, key)
	}
	sortStrings(out)
	return out
}

type AccountCredentialRefresher struct {
	store               accountCredentialRefreshStore
	registry            *ModeAdapterRegistry
	now                 func() time.Time
	requireAccountLease bool
}

func NewAccountCredentialRefresher(store *credentialstore.Store, registry *ModeAdapterRegistry) *AccountCredentialRefresher {
	if registry == nil {
		registry = DefaultModeAdapterRegistry()
	}
	return &AccountCredentialRefresher{store: postgresAccountCredentialRefreshStore{store: store}, registry: registry, now: time.Now, requireAccountLease: true}
}

func (r *AccountCredentialRefresher) Refresh(ctx context.Context, accountID int64) error {
	return r.refresh(ctx, 0, accountID)
}

func (r *AccountCredentialRefresher) RefreshForProvider(ctx context.Context, providerID, accountID int64) error {
	return r.refresh(ctx, providerID, accountID)
}

// RecoverAgentTask 在调度器持有的跨实例账号槽内比较调用方实际使用过的凭据版本；
// 版本已前进时复用赢家结果，只有仍是旧版本时才登记新任务。
func (r *AccountCredentialRefresher) RecoverAgentTask(ctx context.Context, tenantID, accountID int64, expectedCredentialVersion int) error {
	if r == nil || r.store == nil || tenantID <= 0 || accountID <= 0 || expectedCredentialVersion <= 0 {
		return errors.New("credentialworker: agent task recovery input invalid")
	}
	probe, err := r.store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(probe.PlaintextPayload)
	if err := validateAgentTaskRecoveryRecord(probe, tenantID); err != nil {
		return err
	}
	if r.requireAccountLease {
		if err := auth.RequireRefreshAccountLease(ctx, accountID); err != nil {
			return err
		}
	}
	if int(probe.CredentialVersion) > expectedCredentialVersion {
		return nil
	}
	if int(probe.CredentialVersion) < expectedCredentialVersion {
		return errors.New("credentialworker: agent task credential version regressed")
	}
	return r.refreshLockedRecord(ctx, &shortAccountRefreshStore{store: r.store}, accountID, probe)
}

func validateAgentTaskRecoveryRecord(rec credentialstore.CredentialRecord, tenantID int64) error {
	if rec.TenantID != tenantID || rec.Vendor != credentialstore.VendorOpenAI || rec.AuthMode != credentialstore.AuthModeCodexAgent {
		return errors.New("credentialworker: account is not an agent identity credential")
	}
	return nil
}

func (r *AccountCredentialRefresher) refresh(ctx context.Context, _ int64, accountID int64) error {
	if r == nil || r.store == nil {
		return errors.New("credentialworker: account credential store missing")
	}
	rec, err := r.store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	if r.requireAccountLease {
		if err := auth.RequireRefreshAccountLease(ctx, accountID); err != nil {
			return err
		}
	}
	return r.refreshLockedRecord(ctx, &shortAccountRefreshStore{store: r.store}, accountID, rec)
}

func (r *AccountCredentialRefresher) refreshLockedRecord(ctx context.Context, txStore accountCredentialRefreshTxStore, accountID int64, rec credentialstore.CredentialRecord) error {
	defer privacy.Zeroize(rec.PlaintextPayload)
	adapter, ok := r.registry.Lookup(rec.Vendor, rec.AuthMode)
	if !ok {
		err := fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrProviderAdapterMissing, rec.Vendor, rec.AuthMode, accountID)
		// 不得吞掉失败状态持久化错误。若 SaveRefreshFailure 写失败,凭据的 refresh-failure 状态
		// (冷却/重试计数/失败原因)未落库,调度器会按陈旧状态反复重试或漏报;用 errors.Join 同时上抛
		// adapter-missing 与持久化错误,与本包 anthropicoauth refresher 的处理对齐。
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, "adapter_missing", r.now().Add(providerFailureCooldown(rec.Vendor))); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	result, err := adapter.RefreshCredential(ctx, ModeRefreshInput{
		CredentialID: rec.ID, TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID,
		Vendor: rec.Vendor, AuthMode: rec.AuthMode, Payload: rec.PlaintextPayload, Now: r.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, ErrNoRefreshRequired) {
			// 不需要刷新,但我们仍然限流下一次尝试,以避免紧密的重试循环。
			// 我们只设置 next_attempt_at,不改动 state、failure_class 或 failure_count。
			if throttleErr := txStore.SetNextAttemptThrottle(ctx, rec, r.now().Add(ineffectiveRefreshBackoff)); throttleErr != nil {
				return throttleErr
			}
			return nil
		}
		emitGeminiFallbackAudit(ctx, txStore, rec, err, false)
		failureClass := classifyModeRefreshError(err)
		// 见上,刷新失败时的状态持久化错误必须上抛,不能静默吞掉。
		if saveErr := txStore.SaveRefreshFailure(ctx, rec, failureClass, nextModeRefreshAttempt(err, rec.Vendor, r.now())); saveErr != nil {
			return withModeRefreshAuditOutcome(errors.Join(err, saveErr), failureClass)
		}
		return withModeRefreshAuditOutcome(err, failureClass)
	}
	outcome := result.Outcome
	if outcome == "" {
		outcome = "refresh_succeeded"
	}
	emitGeminiFallbackAuditFromPayload(ctx, txStore, rec, result.Payload, true)
	return txStore.SaveRefreshSuccess(ctx, rec, result.Payload, result.AccessExpiresAt, outcome)
}

func emitGeminiFallbackAuditFromPayload(ctx context.Context, store accountCredentialRefreshTxStore, rec credentialstore.CredentialRecord, payload []byte, success bool) {
	fromClient, toClient, attempted := adapters.GeminiCrossClientFallbackMetadata(payload)
	if !attempted {
		return
	}
	emitGeminiFallbackAuditEvent(ctx, store, rec, fromClient, toClient, success)
}

func emitGeminiFallbackAudit(ctx context.Context, store accountCredentialRefreshTxStore, rec credentialstore.CredentialRecord, err error, success bool) {
	var fallbackErr *adapters.GeminiFallbackError
	if !errors.As(err, &fallbackErr) {
		return
	}
	emitGeminiFallbackAuditEvent(ctx, store, rec, fallbackErr.FromClient, fallbackErr.ToClient, success)
}

func emitGeminiFallbackAuditEvent(ctx context.Context, store accountCredentialRefreshTxStore, rec credentialstore.CredentialRecord, fromClient, toClient string, success bool) {
	if store == nil || fromClient == "" || toClient == "" {
		return
	}
	_ = store.InsertAuditEvent(ctx, credentialstore.AuditEvent{
		TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID, CredentialID: rec.ID,
		EventType: "gemini_cross_client_fallback", Vendor: rec.Vendor, AuthMode: rec.AuthMode,
		CredentialVersion: rec.CredentialVersion,
		Payload: map[string]any{
			"from_client": fromClient,
			"to_client":   toClient,
			"success":     success,
		},
	})
}

type AccountCredentialRefreshQueries struct {
	db db.DBTX
}

func NewAccountCredentialRefreshQueries(database db.DBTX) *AccountCredentialRefreshQueries {
	return &AccountCredentialRefreshQueries{db: database}
}

func (q *AccountCredentialRefreshQueries) ListAccountsForRefresh(ctx context.Context, arg dbbilling.ListAccountsForRefreshParams) ([]dbbilling.ListAccountsForRefreshRow, error) {
	if q == nil || q.db == nil {
		return nil, errors.New("credentialworker: account credential refresh db missing")
	}
	const sql = `
SELECT ac.provider_account_id, ac.tenant_id, pa.provider_id, ac.vendor, ac.access_expires_at
FROM account_credentials ac
JOIN tenants t
  ON t.id = ac.tenant_id
 AND t.status = 'active'
 AND t.deleted_at IS NULL
JOIN provider_accounts pa
  ON pa.id = ac.provider_account_id
 AND pa.tenant_id = ac.tenant_id
WHERE ac.deleted_at IS NULL
  AND pa.deleted_at IS NULL
  AND pa.enabled
  AND pa.health_state <> 'revoked'
  AND (
      pa.health_state = 'healthy'
      OR (
          pa.health_state IN ('throttled', 'cooldown')
          AND pa.health_state_until IS NOT NULL
          AND pa.health_state_until <= NOW()
      )
  )
  AND ac.state IN ('active', 'refreshing_with_grace', 'temp_unschedulable')
  AND ac.refresh_before_at IS NOT NULL
  AND ac.refresh_before_at <= $1
  AND (ac.next_attempt_at IS NULL OR ac.next_attempt_at <= NOW())
ORDER BY ac.refresh_before_at ASC, ac.updated_at ASC
LIMIT $2`
	rows, err := q.db.Query(ctx, sql, arg.RefreshBefore, arg.LimitCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dbbilling.ListAccountsForRefreshRow
	for rows.Next() {
		var row dbbilling.ListAccountsForRefreshRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.ProviderID, &row.VendorName, &row.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type staticModeAdapter struct{}

func (staticModeAdapter) RefreshCredential(context.Context, ModeRefreshInput) (ModeRefreshResult, error) {
	return ModeRefreshResult{}, ErrNoRefreshRequired
}

type legacyOAuthModeAdapter struct {
	providerName string
	adapter      RefreshAdapter
}

func (a legacyOAuthModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	if a.adapter == nil {
		return ModeRefreshResult{}, ErrProviderAdapterMissing
	}
	payload, expiresAt, err := a.adapter.RefreshForProvider(ctx, in.ProviderAccountID, a.providerName, in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: expiresAt, Outcome: "refresh_succeeded"}, nil
}

// codexOAuthModeAdapter 根据账号获取时落下的客户端身份来源选择续期身份。
// 新的公开 CLI 账号固定使用内置公开身份；自定义账号必须使用运维配置。
// 旧账号没有来源标记时优先保持已配置的旧行为，否则回落公开身份。
type codexOAuthModeAdapter struct {
	builtin  ModeRefreshAdapter
	operator ModeRefreshAdapter
}

func (a codexOAuthModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	fields, err := payloadMap(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	source := stringField(fields, "client_id_source")
	if source == "" {
		source = stringField(fields, "client_identity_source")
	}
	var selected ModeRefreshAdapter
	switch {
	case source == credentialacq.ClientSourceOperatorConfig:
		selected = a.operator
	case source == credentialacq.ClientSourcePublicCLI || strings.HasPrefix(source, "approved_builtin_profile_"):
		selected = a.builtin
	case source == "":
		selected = a.operator
		if selected == nil {
			selected = a.builtin
		}
	default:
		return ModeRefreshResult{}, fmt.Errorf("codex refresh: unknown client identity source %q", source)
	}
	if selected == nil {
		return ModeRefreshResult{}, adapters.ErrCodexOAuthConfigRequired
	}
	sanitized, err := sanitizeOperatorOAuthPayload(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	in.Payload = sanitized
	return selected.RefreshCredential(ctx, in)
}

type openAIChatGPTBuiltinOAuthModeAdapter struct {
	adapter adapters.ChatGPTRefresh
}

func newOpenAIChatGPTBuiltinOAuthModeAdapter() openAIChatGPTBuiltinOAuthModeAdapter {
	return openAIChatGPTBuiltinOAuthModeAdapter{
		adapter: adapters.ChatGPTRefresh{
			Endpoint:   adapters.ChatGPTOAuthTokenEndpoint,
			ClientID:   adapters.ChatGPTOAuthClientID,
			HTTPClient: auth.NewSSRFProtectedOAuthClient(http.DefaultClient),
		},
	}
}

func (a openAIChatGPTBuiltinOAuthModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	payload, expiresAt, err := a.adapter.RefreshForProvider(ctx, in.ProviderAccountID, "openai", in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: expiresAt, Outcome: "refresh_succeeded"}, nil
}

type builtinClientOAuthModeAdapter struct {
	providerName     string
	configVendor     string
	clientSecretName string
	adapter          adapters.GeminiRefresh
	loadConfig       func() (*appconfig.Config, error)
}

func newGeminiBuiltinClientOAuthModeAdapter(sourceClientFamily string) builtinClientOAuthModeAdapter {
	return builtinClientOAuthModeAdapter{
		providerName:     "gemini",
		configVendor:     appconfig.VendorOAuthGemini,
		clientSecretName: geminiOAuthClientSecretEnv,
		adapter: adapters.GeminiRefresh{
			Endpoint:   credentialacq.DefaultGeminiTokenEndpoint,
			ClientID:   credentialacq.GeminiPublicCLIClientID,
			HTTPClient: auth.NewSSRFProtectedOAuthClient(http.DefaultClient),
			// GEM-5 R-GEM-FALLBACK-001：Google desktop CLI family 当前共用同一公开
			// ClientID，ClientID 切换式 cross-client fallback 必须显式关闭，等待
			// scope / token shape 等 application-level fallback 方案落地。
			AllowCrossClientFallback: false,
			SourceClientFamily:       sourceClientFamily,
			TierCacheTTL:             24 * time.Hour,
			RequireClientSecret:      true,
		},
	}
}

func (a builtinClientOAuthModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	clientSecret, err := a.loadClientSecret()
	if err != nil {
		return ModeRefreshResult{}, err
	}
	adapter := a.adapter
	adapter.ClientSecret = clientSecret
	payload, expiresAt, err := adapter.RefreshForProvider(ctx, in.ProviderAccountID, a.providerName, in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: expiresAt, Outcome: "refresh_succeeded"}, nil
}

func (a builtinClientOAuthModeAdapter) loadClientSecret() (string, error) {
	loadConfig := a.loadConfig
	if loadConfig == nil {
		loadConfig = appconfig.Load
	}
	runtimeConfig, err := loadConfig()
	if err != nil {
		return "", err
	}
	clientSecret := strings.TrimSpace(runtimeConfig.VendorOAuth[a.configVendor].ClientSecret)
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(credentialacq.GeminiPublicCLIConfig().ClientSecret)
	}
	if clientSecret == "" {
		return "", fmt.Errorf("%s builtin oauth refresh: %w: missing %s: %w", a.providerName, credentialacq.ErrFeatureDisabled, a.clientSecretName, ErrOperatorOAuthConfigMissing)
	}
	return clientSecret, nil
}

type copilotOAuthModeAdapter struct {
	adapter providercopilot.CopilotRefreshAdapter
}

func (a copilotOAuthModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	payload, expiresAt, err := a.adapter.RefreshForProvider(ctx, in.ProviderAccountID, credentialstore.VendorCopilot, in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: expiresAt, Outcome: "refresh_succeeded"}, nil
}

type operatorOAuthConfig struct {
	TokenEndpoint string
	ClientID      string
	ClientSecret  string
	HTTPClient    *http.Client
}

type operatorOAuthModeAdapter struct {
	providerName string
	configVendor string
	tokenURLName string
	clientIDName string
	client       *http.Client
	loadConfig   func() (*appconfig.Config, error)
	newAdapter   func(operatorOAuthConfig) RefreshAdapter
}

func (a operatorOAuthModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	cfg, err := a.loadOperatorConfig()
	if err != nil {
		return ModeRefreshResult{}, err
	}
	sanitizedPayload, err := sanitizeOperatorOAuthPayload(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	if a.newAdapter == nil {
		return ModeRefreshResult{}, ErrProviderAdapterMissing
	}
	adapter := a.newAdapter(cfg)
	if adapter == nil {
		return ModeRefreshResult{}, ErrProviderAdapterMissing
	}
	payload, expiresAt, err := adapter.RefreshForProvider(ctx, in.ProviderAccountID, a.providerName, sanitizedPayload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	payload, err = syncSessionTokenFromAccessToken(payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: expiresAt, Outcome: "refresh_succeeded"}, nil
}

func syncSessionTokenFromAccessToken(raw []byte) ([]byte, error) {
	fields, err := payloadMap(raw)
	if err != nil {
		return nil, err
	}
	accessToken := stringField(fields, "access_token")
	if accessToken == "" {
		return nil, errors.New("operator oauth refresh response missing access_token")
	}
	fields["session_token"] = accessToken
	return json.Marshal(fields)
}

func (a operatorOAuthModeAdapter) loadOperatorConfig() (operatorOAuthConfig, error) {
	loadConfig := a.loadConfig
	if loadConfig == nil {
		loadConfig = appconfig.Load
	}
	runtimeConfig, err := loadConfig()
	if err != nil {
		return operatorOAuthConfig{}, err
	}
	oauth := runtimeConfig.VendorOAuth[a.configVendor]
	cfg := operatorOAuthConfig{
		TokenEndpoint: strings.TrimSpace(oauth.TokenURL),
		ClientID:      strings.TrimSpace(oauth.ClientID),
		ClientSecret:  strings.TrimSpace(oauth.ClientSecret),
		HTTPClient:    a.client,
	}
	var missing []string
	if cfg.TokenEndpoint == "" {
		missing = append(missing, a.tokenURLName)
	}
	if cfg.ClientID == "" {
		missing = append(missing, a.clientIDName)
	}
	if len(missing) > 0 {
		return operatorOAuthConfig{}, fmt.Errorf("%s oauth refresh: %w: missing %s", a.providerName, ErrOperatorOAuthConfigMissing, strings.Join(missing, ","))
	}
	return cfg, nil
}

func sanitizeOperatorOAuthPayload(raw []byte) ([]byte, error) {
	fields, err := payloadMap(raw)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{
		"oauth_token_endpoint",
		"oauth_token_url",
		"token_endpoint",
		"token_url",
		"client_id",
		"client_secret",
		"scope",
	} {
		delete(fields, key)
	}
	return json.Marshal(fields)
}

type windsurfManualModeAdapter struct {
	adapter adapters.WindsurfManualTokenRefresh
}

func (a windsurfManualModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	_, _, err := a.adapter.RefreshForProvider(ctx, in.ProviderAccountID, credentialstore.VendorWindsurf, in.Payload)
	if errors.Is(err, adapters.ErrWindsurfManualTokenRefreshRequired) {
		return ModeRefreshResult{}, ErrNoRefreshRequired
	}
	if err == nil {
		return ModeRefreshResult{}, ErrNoRefreshRequired
	}
	return ModeRefreshResult{}, err
}

// builtinRefreshTokenModeAdapter 刷新这样一类上游 OAuth 凭据:其 provider 在一个
// 固定的、编译期确定的 token endpoint 上暴露标准 OAuth2 refresh_token grant,并带有
// 内置的公开 client_id(xAI/Grok、Kimi/Moonshot)。token endpoint 绝不由 payload 控制,
// 所有出站都使用受 SSRF 保护的 OAuth 客户端。
type builtinRefreshTokenModeAdapter struct {
	providerName string
	tokenURL     string
	clientID     string
	client       *http.Client
}

func (a builtinRefreshTokenModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	fields, err := payloadMap(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	refreshToken := stringField(fields, "refresh_token")
	if refreshToken == "" {
		return ModeRefreshResult{}, ErrNoRefreshRequired
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", a.clientID)
	form.Set("refresh_token", refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ModeRefreshResult{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return executeTokenRequest(a.httpClient(), req, fields)
}

func (a builtinRefreshTokenModeAdapter) httpClient() *http.Client {
	if a.client != nil {
		return a.client
	}
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

type mockTokenExchangeAdapter struct {
	providerName string
	client       *http.Client
}

func (a mockTokenExchangeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	fields, err := payloadMap(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	endpoint := stringField(fields, "mock_token_endpoint")
	if endpoint == "" {
		return ModeRefreshResult{}, ErrNoRefreshRequired
	}
	body, _ := json.Marshal(map[string]any{"vendor": in.Vendor, "auth_mode": in.AuthMode, "provider": a.providerName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ModeRefreshResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	return executeTokenRequest(a.httpClient(), req, fields)
}

func (a mockTokenExchangeAdapter) httpClient() *http.Client {
	if a.client != nil {
		return a.client
	}
	// mock_token_endpoint 取自凭据 payload(任意值),未注入 client 时的 fallback 必须走
	// SSRF 保护客户端,拨号期拒绝环回/内网/link-local 目标,防止被构造的凭据把 worker 导向内网
	// 地址并窃取返回的 access_token。与 operator adapter 同范式。
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

type metadataTokenAdapter struct {
	client *http.Client
}

func (a metadataTokenAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	fields, err := payloadMap(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	endpoint := stringField(fields, "metadata_token_endpoint")
	if endpoint == "" {
		endpoint = stringField(fields, "mock_token_endpoint")
	}
	if endpoint == "" {
		return ModeRefreshResult{}, ErrNoRefreshRequired
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	client := a.client
	if client == nil {
		// metadata_token_endpoint 同为 payload 提供的任意值 —— HUAKAI 把它当普通可配置端点,
		// 而非真实的 in-instance GCE metadata 调用,故 fallback 走 SSRF 保护客户端、拒绝 link-local
		// 169.254 在此是正确的(不会破坏真实 GCE 元数据获取,因为本就不是那条路径)。
		client = auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	}
	return executeTokenRequest(client, req, fields)
}

func executeTokenRequest(client *http.Client, req *http.Request, fields map[string]any) (ModeRefreshResult, error) {
	resp, err := client.Do(req)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return ModeRefreshResult{}, fmt.Errorf("token exchange returned status %d", resp.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil {
		return ModeRefreshResult{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return ModeRefreshResult{}, errors.New("token exchange response missing access_token")
	}
	ttl := token.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	expiresAt := time.Now().UTC().Add(time.Duration(ttl) * time.Second)
	fields["access_token"] = token.AccessToken
	if strings.TrimSpace(token.RefreshToken) != "" {
		fields["refresh_token"] = token.RefreshToken
	}
	fields["expires_at"] = expiresAt.Format(time.RFC3339)
	if token.TokenType != "" {
		fields["token_type"] = token.TokenType
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: expiresAt, Outcome: "refresh_succeeded"}, nil
}

func payloadMap(raw []byte) (map[string]any, error) {
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("credential payload is not an object")
	}
	return fields, nil
}

func stringField(fields map[string]any, key string) string {
	switch v := fields[key].(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
