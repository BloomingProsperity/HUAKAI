package credentialworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker/adapters"
	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

var ErrNoRefreshRequired = errors.New("credentialworker: no refresh required")

type ModeRefreshInput struct {
	CredentialID      int64
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	AuthMode          string
	Payload           []byte
	Now               time.Time
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
	r := NewModeAdapterRegistry()
	register := func(vendor, authMode string, adapter ModeRefreshAdapter) {
		_ = r.Register(vendor, authMode, adapter)
	}
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey, staticModeAdapter{})
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, legacyOAuthModeAdapter{providerName: "anthropic", adapter: adapters.AnthropicRefresh{}})
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode, legacyOAuthModeAdapter{providerName: "anthropic", adapter: adapters.AnthropicRefresh{}})
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeBedrock, staticModeAdapter{})
	register(credentialstore.VendorAnthropic, credentialstore.AuthModeVertexAnthropic, metadataTokenAdapter{})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, staticModeAdapter{})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, legacyOAuthModeAdapter{providerName: "openai", adapter: adapters.OpenAIRefresh{}})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth, legacyOAuthModeAdapter{providerName: "codex", adapter: adapters.CodexRefresh{OpenAI: adapters.OpenAIRefresh{}}})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeAzure, mockTokenExchangeAdapter{providerName: "azure"})
	register(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken, legacyOAuthModeAdapter{providerName: "openai", adapter: adapters.OpenAIRefresh{}})
	register(credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey, staticModeAdapter{})
	register(credentialstore.VendorGemini, credentialstore.AuthModeVertexSA, metadataTokenAdapter{})
	register(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, legacyOAuthModeAdapter{providerName: "gemini", adapter: adapters.GeminiRefresh{AllowCrossClientFallback: true, SourceClientFamily: "code_assist", TierCacheTTL: 24 * time.Hour}})
	register(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne, legacyOAuthModeAdapter{providerName: "gemini", adapter: adapters.GeminiRefresh{AllowCrossClientFallback: true, SourceClientFamily: "google_one", TierCacheTTL: 24 * time.Hour}})
	register(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity, legacyOAuthModeAdapter{providerName: "antigravity", adapter: adapters.AntigravityRefresh{Gemini: adapters.GeminiRefresh{AllowCrossClientFallback: true, SourceClientFamily: "antigravity", TierCacheTTL: 24 * time.Hour}}})
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
	store    accountCredentialRefreshStore
	registry *ModeAdapterRegistry
	now      func() time.Time
}

func NewAccountCredentialRefresher(store *credentialstore.Store, registry *ModeAdapterRegistry) *AccountCredentialRefresher {
	if registry == nil {
		registry = DefaultModeAdapterRegistry()
	}
	return &AccountCredentialRefresher{store: postgresAccountCredentialRefreshStore{store: store}, registry: registry, now: time.Now}
}

func (r *AccountCredentialRefresher) Refresh(ctx context.Context, accountID int64) error {
	return r.refresh(ctx, 0, accountID)
}

func (r *AccountCredentialRefresher) RefreshForProvider(ctx context.Context, providerID, accountID int64) error {
	return r.refresh(ctx, providerID, accountID)
}

func (r *AccountCredentialRefresher) refresh(ctx context.Context, _ int64, accountID int64) error {
	if r == nil || r.store == nil {
		return errors.New("credentialworker: account credential store missing")
	}
	probe, err := r.store.LoadForRefresh(ctx, accountID)
	if err != nil {
		return err
	}
	defer privacy.Zeroize(probe.PlaintextPayload)
	return r.store.WithRefreshTransaction(ctx, func(txStore accountCredentialRefreshTxStore, tx db.DBTX) error {
		return credentialacq.WithRefreshLock(ctx, tx, probe.ID, func(db.DBTX) error {
			rec, err := txStore.LoadForRefresh(ctx, accountID)
			if err != nil {
				return err
			}
			if rec.ID != probe.ID {
				return credentialacq.WithRefreshLock(ctx, tx, rec.ID, func(db.DBTX) error {
					lockedRec, err := txStore.LoadForRefresh(ctx, accountID)
					if err != nil {
						return err
					}
					return r.refreshLockedRecord(ctx, txStore, accountID, lockedRec)
				})
			}
			return r.refreshLockedRecord(ctx, txStore, accountID, rec)
		})
	})
}

func (r *AccountCredentialRefresher) refreshLockedRecord(ctx context.Context, txStore accountCredentialRefreshTxStore, accountID int64, rec credentialstore.CredentialRecord) error {
	defer privacy.Zeroize(rec.PlaintextPayload)
	adapter, ok := r.registry.Lookup(rec.Vendor, rec.AuthMode)
	if !ok {
		err := fmt.Errorf("%w: vendor=%s auth_mode=%s account_id=%d", ErrProviderAdapterMissing, rec.Vendor, rec.AuthMode, accountID)
		_ = txStore.SaveRefreshFailure(ctx, rec, "adapter_missing", r.now().Add(time.Minute))
		return err
	}
	result, err := adapter.RefreshCredential(ctx, ModeRefreshInput{
		CredentialID: rec.ID, TenantID: rec.TenantID, ProviderAccountID: rec.ProviderAccountID,
		Vendor: rec.Vendor, AuthMode: rec.AuthMode, Payload: rec.PlaintextPayload, Now: r.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, ErrNoRefreshRequired) {
			return nil
		}
		emitGeminiFallbackAudit(ctx, txStore, rec, err, false)
		_ = txStore.SaveRefreshFailure(ctx, rec, classifyModeRefreshError(err), r.now().Add(time.Minute))
		return err
	}
	outcome := result.Outcome
	if outcome == "" {
		outcome = "refresh_succeeded"
	}
	emitGeminiFallbackAuditFromPayload(ctx, txStore, rec, result.Payload, true)
	return txStore.SaveRefreshSuccess(ctx, rec, result.Payload, result.AccessExpiresAt, outcome)
}

type accountCredentialRefreshTxStore interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	SaveRefreshSuccess(context.Context, credentialstore.CredentialRecord, []byte, time.Time, string) error
	SaveRefreshFailure(context.Context, credentialstore.CredentialRecord, string, time.Time) error
	InsertAuditEvent(context.Context, credentialstore.AuditEvent) error
}

type accountCredentialRefreshStore interface {
	LoadForRefresh(context.Context, int64) (credentialstore.CredentialRecord, error)
	WithRefreshTransaction(context.Context, func(accountCredentialRefreshTxStore, db.DBTX) error) error
}

type postgresAccountCredentialRefreshStore struct {
	store *credentialstore.Store
}

func (s postgresAccountCredentialRefreshStore) WithRefreshTransaction(ctx context.Context, fn func(accountCredentialRefreshTxStore, db.DBTX) error) error {
	if s.store == nil {
		return errors.New("credentialworker: account credential store missing")
	}
	return s.store.WithTransaction(ctx, func(txStore *credentialstore.Store, tx db.DBTX) error {
		if fn == nil {
			return nil
		}
		return fn(txStore, tx)
	})
}

func (s postgresAccountCredentialRefreshStore) LoadForRefresh(ctx context.Context, accountID int64) (credentialstore.CredentialRecord, error) {
	if s.store == nil {
		return credentialstore.CredentialRecord{}, errors.New("credentialworker: account credential store missing")
	}
	return s.store.LoadForRefresh(ctx, accountID)
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

func (q *AccountCredentialRefreshQueries) ListAccountsForRefresh(ctx context.Context, arg db.ListAccountsForRefreshParams) ([]db.ListAccountsForRefreshRow, error) {
	if q == nil || q.db == nil {
		return nil, errors.New("credentialworker: account credential refresh db missing")
	}
	const sql = `
SELECT ac.provider_account_id, ac.tenant_id, pa.provider_id, ac.access_expires_at
FROM account_credentials ac
JOIN provider_accounts pa ON pa.id = ac.provider_account_id
WHERE ac.deleted_at IS NULL
  AND pa.deleted_at IS NULL
  AND pa.enabled
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
	var out []db.ListAccountsForRefreshRow
	for rows.Next() {
		var row db.ListAccountsForRefreshRow
		if err := rows.Scan(&row.ID, &row.TenantID, &row.ProviderID, &row.ExpiresAt); err != nil {
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
	return http.DefaultClient
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
		client = http.DefaultClient
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
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		TokenType   string `json:"token_type"`
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

func classifyModeRefreshError(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "invalid_grant"):
		return "invalid_grant"
	case strings.Contains(msg, "decrypt"):
		return "decrypt_failed"
	case strings.Contains(msg, "payload") || strings.Contains(msg, "json"):
		return "payload_invalid"
	default:
		return "temporary"
	}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
