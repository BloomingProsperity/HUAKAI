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

// UpstreamModelsIPPredicate controls which resolved IPs are blocked at dial
// time. The default (nil) uses the real provider.WrapPassthroughEndpointTransport
// guard. Tests inject an allow-all predicate to reach httptest servers.
//
// It is expressed as a DialContext wrapper factory rather than an IP predicate
// so that we reuse the existing, tested transport wrapper in production.
type upstreamModelsTransportWrapper func(rt http.RoundTripper) (http.RoundTripper, error)

// UpstreamModelsDeps holds all dependencies for the upstream-models handler.
type UpstreamModelsDeps struct {
	Auth      upstreamModelsAuth
	Accounts  upstreamModelsAccountStore
	Creds     upstreamModelsCredentialStore
	// TransportWrapper overrides the SSRF-guarded transport in tests.
	// Production code MUST leave this nil so the real guard is used.
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

// upstreamModelsListResponse is the JSON response body.
type upstreamModelsListResponse struct {
	Models []string `json:"models"`
	Count  int      `json:"count"`
}

// MountProviderAccountUpstreamModelsRoutes registers GET /{id}/upstream-models.
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

		// Fetch the provider account row (authoritative tenant scope check).
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

		// Load and decrypt the credential.
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

		// Map plaintext bytes ??provider.Credential using the account's auth mode.
		// upstream_static accounts carry base_url + auth_header_value.
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

		// Build SSRF-guarded HTTP client.
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

// errUpstreamBlocked is returned when the SSRF guard rejects the dial.
var errUpstreamBlocked = errors.New("upstream_blocked")

// resolveUpstreamModelsTenant mirrors resolveProviderAccountTestTenant.
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

// mapProviderCredential converts a CredentialRecord's PlaintextPayload into a
// provider.Credential using the account's AuthMode as account_type selector.
// This mirrors how postgres_vault.go / credentialworker maps credentials.
func mapProviderCredential(rec credentialstore.CredentialRecord) (provider.Credential, error) {
	// AuthMode in credentialstore maps to account_type in postgres_vault.
	// For upstream_static accounts AuthMode == "upstream_static".
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

// buildModelsURL appends /v1/models to the base URL, respecting whether the
// base already carries a path. Uses the same base-URL logic as adapter.go:
// if base is scheme+host only (or ends with /), append /v1/models directly;
// otherwise trust the operator's custom path and append /models.
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
		// Custom base path: operator manages the full path; append /models.
		u.Path = strings.TrimRight(path, "/") + "/models"
	}
	return u.String(), nil
}

// buildUpstreamModelsClient constructs an *http.Client with the SSRF-guarded
// transport. In production, wrapper is nil and provider.WrapPassthroughEndpointTransport
// is used. Tests inject an allow-all wrapper.
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

// openAIModelsResponse is the shape returned by OpenAI-compatible /v1/models.
type openAIModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetchUpstreamModels GETs the models endpoint, parses the response, and
// returns a sorted, de-duplicated list of model IDs.
// It NEVER logs the Authorization header value or the raw body (CMB-5).
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

	// Read with a size cap to prevent memory exhaustion from a malicious upstream.
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelsMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read upstream response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}

	return parseModelsResponse(body)
}

// isBlockedErr reports whether err comes from the SSRF passthrough guard.
func isBlockedErr(err error) bool {
	return errors.Is(err, provider.ErrUnsafePassthroughEndpoint)
}

// parseModelsResponse parses OpenAI-compatible {data:[{id}]} responses.
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
