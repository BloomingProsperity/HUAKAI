package accountbundle

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	contractVersion    = "account-bundle-v1"
	exportConfirmation = "EXPORT_ENCRYPTED_ACCOUNT_BUNDLE"
)

type Service struct {
	pool        *pgxpool.Pool
	credentials *credentialstore.Store
	keys        credentialstore.KeyProvider
	intake      *accountintake.Service
	now         func() time.Time
}

func NewService(pool *pgxpool.Pool, credentials *credentialstore.Store, keys credentialstore.KeyProvider, intakeService *accountintake.Service) *Service {
	return &Service{pool: pool, credentials: credentials, keys: keys, intake: intakeService}
}

type accountSnapshot struct {
	ID                 int64
	TenantID           int64
	ProviderID         int64
	ChannelID          int64
	Config             PublicConfig
	ProxyID            *int64
	UpdatedAt          time.Time
	Credential         *credentialstore.CredentialMetadata
	CredentialConflict string
	Proxy              *admindb.GetProxyRow
}

func (s *Service) ready() error {
	if s == nil || s.pool == nil || s.credentials == nil || s.keys == nil || s.intake == nil {
		return ErrNotConfigured
	}
	return nil
}

func (s *Service) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func normalizeAccountIDs(values []int64) ([]int64, error) {
	if len(values) == 0 || len(values) > MaxAccounts {
		return nil, ErrInvalidInput
	}
	out := append([]int64(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	for index, id := range out {
		if id <= 0 || index > 0 && id == out[index-1] {
			return nil, ErrInvalidInput
		}
	}
	return out, nil
}

func validateOperator(tenantID int64, actorID, actorRole string) error {
	if tenantID <= 0 || strings.TrimSpace(actorID) == "" ||
		(actorRole != admin.RolePlatformAdmin && actorRole != admin.RoleTenantOperator) {
		return ErrInvalidInput
	}
	return nil
}

func stableHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	defer privacy.Zeroize(raw)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func compareHash(left, right string) bool {
	if len(left) != sha256.Size*2 || len(right) != sha256.Size*2 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func accountRef(tenantID, accountID int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("account-bundle:%d:%d", tenantID, accountID)))
	return "account-" + hex.EncodeToString(sum[:8])
}

func destinationKey(providerID, channelID int64) string {
	return fmt.Sprintf("provider:%d/channel:%d", providerID, channelID)
}

func proxyRef(row admindb.GetProxyRow) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		fmt.Sprint(row.ID),
		strings.ToLower(strings.TrimSpace(row.Protocol)), strings.ToLower(strings.TrimSpace(row.Host)),
		fmt.Sprint(row.Port), valueOrEmpty(row.AuthUsername),
	}, "\x00")))
	return "proxy-" + hex.EncodeToString(sum[:8])
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func pgTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func portableCredentialState(state string) bool {
	switch state {
	case credentialstore.StateActive,
		credentialstore.StateRefreshingWithGrace,
		credentialstore.StateTempUnschedulable,
		credentialstore.StateNeedsRotation,
		credentialstore.StateOperatorAttention:
		return true
	default:
		return false
	}
}

func (s *Service) readSnapshots(ctx context.Context, tenantID int64, accountIDs []int64) ([]accountSnapshot, error) {
	ids, err := normalizeAccountIDs(accountIDs)
	if err != nil {
		return nil, err
	}
	q := admindb.New(s.pool)
	out := make([]accountSnapshot, 0, len(ids))
	for _, id := range ids {
		item, err := s.readAccountSnapshot(ctx, q, tenantID, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				out = append(out, accountSnapshot{ID: id, TenantID: tenantID, CredentialConflict: "account_not_found"})
				continue
			}
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) readAccountSnapshot(ctx context.Context, q *admindb.Queries, tenantID, accountID int64) (accountSnapshot, error) {
	const query = `
SELECT id, tenant_id, provider_id, channel_id, name, account_type, enabled, expires_at,
       cap_concurrency, cap_queue_sticky, cap_queue_fallback, priority, static_weight,
       upstream_cost_ratio, probe_model, tags, extra, model_allow_list, capability_flags,
       rpm_limit, tpm_limit, window_cost_limit_cents, max_sessions, disable_cooling,
       refresh_lead_seconds, tls_fingerprint_rotate, custom_error_codes_enabled,
       custom_error_codes, pool_mode, temp_unschedulable_enabled, temp_unschedulable_rules,
       proxy_id, updated_at
FROM provider_accounts
WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL`
	var row accountSnapshot
	var expires pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, query, tenantID, accountID).Scan(
		&row.ID, &row.TenantID, &row.ProviderID, &row.ChannelID,
		&row.Config.Name, &row.Config.AccountType, &row.Config.Enabled, &expires,
		&row.Config.CapConcurrency, &row.Config.CapQueueSticky, &row.Config.CapQueueFallback,
		&row.Config.Priority, &row.Config.StaticWeight, &row.Config.UpstreamCostRatio,
		&row.Config.ProbeModel, &row.Config.Tags, &row.Config.Extra,
		&row.Config.ModelAllowList, &row.Config.CapabilityFlags,
		&row.Config.RPMLimit, &row.Config.TPMLimit, &row.Config.WindowCostLimitCents,
		&row.Config.MaxSessions, &row.Config.DisableCooling, &row.Config.RefreshLeadSeconds,
		&row.Config.TLSFingerprintRotate, &row.Config.CustomErrorCodesEnabled,
		&row.Config.CustomErrorCodes, &row.Config.PoolMode, &row.Config.TempUnschedulableEnabled,
		&row.Config.TempUnschedulableRules, &row.ProxyID, &row.UpdatedAt,
	)
	if err != nil {
		return accountSnapshot{}, err
	}
	row.Config.ExpiresAt = pgTime(expires)
	credentials, err := s.credentials.ListByAccount(ctx, tenantID, accountID)
	if err != nil {
		return accountSnapshot{}, err
	}
	for index := range credentials {
		credential := credentials[index]
		if !portableCredentialState(credential.State) {
			continue
		}
		if row.Credential != nil {
			row.Credential = nil
			row.CredentialConflict = "multiple_portable_credentials"
			break
		}
		copy := credential
		row.Credential = &copy
	}
	if row.Credential == nil && row.CredentialConflict == "" {
		row.CredentialConflict = "portable_credential_missing"
	}
	if row.ProxyID != nil {
		proxy, err := q.GetProxy(ctx, admindb.GetProxyParams{TenantID: tenantID, ID: *row.ProxyID})
		if err != nil {
			row.CredentialConflict = "proxy_not_found"
		} else {
			row.Proxy = &proxy
		}
	}
	return row, nil
}

func zeroPortableContent(content *payloadContent) {
	if content == nil {
		return
	}
	for i := range content.Accounts {
		privacy.Zeroize(content.Accounts[i].Credential.Payload)
		content.Accounts[i].Credential.Payload = nil
	}
	for i := range content.Proxies {
		content.Proxies[i].AuthSecret = ""
	}
}
