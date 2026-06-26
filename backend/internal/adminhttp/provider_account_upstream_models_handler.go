package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const (
	defaultUpstreamModelsPlatformTenantID = int64(1)
	upstreamModelsRequestTimeout          = 15 * time.Second
	upstreamModelsMaxBodyBytes            = 1 << 20 // 1 MiB
)

// upstreamModelsTransportWrapper 控制在拨号时哪些已解析的 IP 会被拦截。
// 默认值(nil)使用真实的 provider.WrapPassthroughEndpointTransport 守卫。
// 测试会注入一个全放行的判定函数,以便能访问到 httptest 服务器。
//
// 它表现为一个 DialContext 包装器工厂,而非一个 IP 判定函数,
// 这样在生产环境中我们就能复用现有的、已经过测试的传输层包装器。
type upstreamModelsTransportWrapper func(rt http.RoundTripper) (http.RoundTripper, error)

// UpstreamModelsDeps 持有 upstream-models 处理器的全部依赖。
type UpstreamModelsDeps struct {
	Auth      upstreamModelsAuth
	Accounts  upstreamModelsAccountStore
	Creds     upstreamModelsCredentialStore
	// TransportWrapper 在测试中覆盖经过 SSRF 守卫的传输层。
	// 生产代码必须保持其为 nil,以便使用真实的守卫。
	TransportWrapper upstreamModelsTransportWrapper
}

type upstreamModelsAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type upstreamModelsAccountStore interface {
	GetAdminProviderAccount(context.Context, admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error)
}

type upstreamModelsCredentialStore interface {
	LoadForProviderAccountTest(context.Context, int64, int64) (credentialstore.CredentialRecord, error)
}

// upstreamModelsListResponse 是 JSON 响应体。
type upstreamModelsListResponse struct {
	Models []string `json:"models"`
	Count  int      `json:"count"`
}

// MountProviderAccountUpstreamModelsRoutes 注册 GET /{id}/upstream-models。
func MountProviderAccountUpstreamModelsRoutes(r chi.Router, d UpstreamModelsDeps) {
	r.Get("/{id}/upstream-models", newProviderAccountUpstreamModelsHandler(d))
}

func newProviderAccountUpstreamModelsHandler(d UpstreamModelsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, tenantID, ok := resolveUpstreamModelsTenant(w, r, d)
		if !ok {
			return
		}

		id, ok := parseUpstreamModelsAccountID(w, r)
		if !ok {
			return
		}

		// 拉取 provider account 行(权威的租户范围校验)。
		_, err := d.Accounts.GetAdminProviderAccount(r.Context(), admindb.GetAdminProviderAccountParams{
			ID: id, TenantID: tenantID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusNotFound, "provider_account_not_found", "provider account not found")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "provider_account_get_failed", "provider account lookup is unavailable")
			return
		}

		// 加载并解密凭证。
		rec, err := d.Creds.LoadForProviderAccountTest(r.Context(), tenantID, id)
		if err != nil {
			if errors.Is(err, credentialstore.ErrCredentialNotFound) {
				writeError(w, http.StatusNotFound, "credential_not_found", "no active credential found for this provider account")
				return
			}
			writeError(w, http.StatusServiceUnavailable, "credential_load_failed", "credential load unavailable")
			return
		}
		defer privacy.Zeroize(rec.PlaintextPayload)

		// 根据账号的认证模式,把明文字节映射为 provider.Credential。
		// upstream_static 账号携带 base_url + auth_header_value。
		cred, err := mapProviderCredential(rec)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "credential_format_invalid", "stored credential payload cannot be decoded")
			return
		}

		if cred.Type != provider.CredentialTypeUpstreamPassthrough {
			writeError(w, http.StatusUnprocessableEntity, "unsupported_credential_type",
				"upstream model listing is only supported for upstream_passthrough credentials")
			return
		}

		baseURL := strings.TrimSpace(cred.Extra["base_url"])
		if baseURL == "" {
			writeError(w, http.StatusUnprocessableEntity, "base_url_missing",
				"provider account credential does not carry a base_url; cannot discover upstream models")
			return
		}

		modelsURL, err := buildModelsURL(baseURL)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "upstream_url_invalid",
				"provider account base_url is malformed or unsafe")
			return
		}

		// 构建经过 SSRF 守卫的 HTTP 客户端。
		client, err := buildUpstreamModelsClient(d.TransportWrapper)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "upstream_blocked",
				"upstream transport guard could not be initialised")
			return
		}

		models, err := fetchUpstreamModels(r.Context(), client, modelsURL, cred.Value)
		if err != nil {
			if errors.Is(err, errUpstreamBlocked) {
				writeError(w, http.StatusUnprocessableEntity, "upstream_blocked",
					"upstream address is blocked by SSRF policy")
				return
			}
			writeError(w, http.StatusBadGateway, "upstream_error",
				"upstream models endpoint returned an error or an unparseable response")
			return
		}

		resp := upstreamModelsListResponse{Models: models, Count: len(models)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// errUpstreamBlocked 在 SSRF 守卫拒绝拨号时返回。
var errUpstreamBlocked = errors.New("upstream_blocked")

// resolveUpstreamModelsTenant 与 resolveProviderAccountTestTenant 对应一致。
func resolveUpstreamModelsTenant(w http.ResponseWriter, r *http.Request, d UpstreamModelsDeps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Accounts == nil || d.Creds == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "upstream models dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator:
		if ident.ScopeTenantID <= 0 {
			writeError(w, http.StatusForbidden, "admin_forbidden", "tenant_operator scope_tenant_id required")
			return admin.AdminIdentity{}, 0, false
		}
		return ident, ident.ScopeTenantID, true
	case admin.RolePlatformAdmin:
		if ident.ScopeTenantID > 0 {
			return ident, ident.ScopeTenantID, true
		}
		return ident, defaultUpstreamModelsPlatformTenantID, true
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
}

func parseUpstreamModelsAccountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_provider_account_id", "id must be a positive int64")
		return 0, false
	}
	return id, true
}

// mapProviderCredential 把 CredentialRecord 的 PlaintextPayload 转换成
// provider.Credential,使用账号的 AuthMode 作为 account_type 选择器。
// 这与 postgres_vault.go / credentialworker 映射凭证的方式保持一致。
func mapProviderCredential(rec credentialstore.CredentialRecord) (provider.Credential, error) {
	// credentialstore 中的 AuthMode 对应 postgres_vault 中的 account_type。
	// 对 upstream_static 账号而言,AuthMode == "upstream_static"。
	switch rec.AuthMode {
	case "upstream_static":
		return mapUpstreamStaticCredential(rec.PlaintextPayload)
	default:
		return provider.Credential{}, fmt.Errorf("unsupported auth mode: %s", rec.AuthMode)
	}
}

type rawUpstreamStaticForModels struct {
	BaseURL         string `json:"base_url"`
	AuthHeaderValue string `json:"auth_header_value"`
}

func mapUpstreamStaticCredential(payload []byte) (provider.Credential, error) {
	var r rawUpstreamStaticForModels
	if err := json.Unmarshal(payload, &r); err != nil {
		return provider.Credential{}, fmt.Errorf("upstream_static unmarshal: %w", err)
	}
	if r.AuthHeaderValue == "" {
		return provider.Credential{}, fmt.Errorf("upstream_static auth_header_value is empty")
	}
	extra := map[string]string{}
	if r.BaseURL != "" {
		extra["base_url"] = r.BaseURL
	}
	return provider.Credential{
		Type:  provider.CredentialTypeUpstreamPassthrough,
		Value: r.AuthHeaderValue,
		Extra: extra,
	}, nil
}

// buildModelsURL 在 base URL 后追加 /v1/models,并考虑 base 是否已经带有路径。
// 采用与 adapter.go 相同的 base-URL 逻辑:
// 如果 base 仅为 scheme+host(或以 / 结尾),则直接追加 /v1/models;
// 否则信任运维设置的自定义路径,并追加 /models。
func buildModelsURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" || u.Scheme != "https" {
		return "", fmt.Errorf("invalid base_url: %s", base)
	}
	path := u.Path
	switch {
	case path == "" || path == "/":
		u.Path = "/v1/models"
	case strings.HasSuffix(path, "/v1"):
		u.Path = path + "/models"
	default:
		// 自定义 base 路径:由运维管理完整路径;追加 /models。
		u.Path = strings.TrimRight(path, "/") + "/models"
	}
	return u.String(), nil
}

// buildUpstreamModelsClient 构造一个带有 SSRF 守卫传输层的 *http.Client。
// 在生产环境中,wrapper 为 nil,使用 provider.WrapPassthroughEndpointTransport。
// 测试会注入一个全放行的 wrapper。
func buildUpstreamModelsClient(wrapper upstreamModelsTransportWrapper) (*http.Client, error) {
	if wrapper == nil {
		wrapper = provider.WrapPassthroughEndpointTransport
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	rt, err := wrapper(base)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: rt,
		Timeout:   upstreamModelsRequestTimeout,
	}, nil
}

// openAIModelsResponse 是 OpenAI 兼容的 /v1/models 返回的结构。
type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetchUpstreamModels 以 GET 请求访问 models 端点,解析响应,
// 并返回一个已去重的 model ID 列表。
// 它绝不会记录 Authorization header 的值或原始响应体(CMB-5)。
func fetchUpstreamModels(ctx context.Context, client *http.Client, modelsURL, authHeader string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		if isBlockedErr(err) {
			return nil, errUpstreamBlocked
		}
		return nil, fmt.Errorf("upstream request failed")
	}
	defer resp.Body.Close()

	// 限制读取大小,防止恶意上游导致内存耗尽。
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelsMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}

	return parseModelsResponse(body)
}

// isBlockedErr 报告 err 是否来自 SSRF passthrough 守卫。
func isBlockedErr(err error) bool {
	return errors.Is(err, provider.ErrUnsafePassthroughEndpoint)
}

// parseModelsResponse 解析 OpenAI 兼容的 {data:[{id}]} 响应。
func parseModelsResponse(body []byte) ([]string, error) {
	var r openAIModelsResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	seen := make(map[string]struct{}, len(r.Data))
	models := make([]string, 0, len(r.Data))
	for _, entry := range r.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	return models, nil
}
