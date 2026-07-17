package accountbundle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type Exporter struct {
	pool        *pgxpool.Pool
	credentials *credentialstore.Store
	now         func() time.Time
}

func NewExporter(pool *pgxpool.Pool, credentials *credentialstore.Store) *Exporter {
	return &Exporter{pool: pool, credentials: credentials, now: time.Now}
}

func (e *Exporter) Export(ctx context.Context, tenantID int64, mode, passphrase string, ttl time.Duration) (ExportResult, error) {
	if e == nil || e.pool == nil || e.credentials == nil || tenantID <= 0 || (mode != ModeStructure && mode != ModeRecovery) {
		return ExportResult{}, ErrInvalidBundle
	}
	now := e.now().UTC()
	manifest := Manifest{Version: ManifestVersion, BundleID: uuid.NewString(), Mode: mode, CreatedAt: now}
	defer ZeroizeManifest(&manifest)
	credentialCount := 0
	identityByCredential := map[int64]credentialstore.CredentialIdentityMetadata{}
	if mode == ModeRecovery {
		if ttl <= 0 || ttl > MaxRecoveryTTL {
			ttl = MaxRecoveryTTL
		}
		manifest.ExpiresAt = now.Add(ttl)
		identities, err := e.credentials.ListIdentityInventory(ctx, tenantID, "")
		if err != nil {
			return ExportResult{}, err
		}
		for _, identity := range identities {
			identityByCredential[identity.CredentialID] = identity
		}
	}
	rows, err := e.pool.Query(ctx, `
		SELECT pa.id,p.code,pa.name,pa.account_type,pa.enabled,pa.cap_concurrency,pa.priority,
			pa.static_weight,pa.probe_model,pa.tags,pa.extra,pa.model_allow_list,pa.capability_flags
		FROM provider_accounts pa
		JOIN providers p ON p.id=pa.provider_id AND p.tenant_id=pa.tenant_id AND p.deleted_at IS NULL
		WHERE pa.tenant_id=$1 AND pa.deleted_at IS NULL ORDER BY pa.id`, tenantID)
	if err != nil {
		return ExportResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var account Account
		var extra []byte
		if err := rows.Scan(&account.SourceAccountID, &account.Template.SourceProvider, &account.Template.Name,
			&account.Template.AccountType, &account.Template.Enabled, &account.Template.CapConcurrency,
			&account.Template.Priority, &account.Template.StaticWeight, &account.Template.ProbeModel,
			&account.Template.Tags, &extra, &account.Template.ModelAllowList, &account.Template.CapabilityFlags); err != nil {
			return ExportResult{}, err
		}
		if mode == ModeRecovery {
			account.Template.Extra = append(json.RawMessage(nil), extra...)
			record, resolveErr := e.credentials.ResolveActive(ctx, tenantID, account.SourceAccountID)
			switch {
			case resolveErr == nil:
				account.Vendor = record.Vendor
				account.AuthMode = record.AuthMode
				account.Credential = append(json.RawMessage(nil), record.PlaintextPayload...)
				privacy.Zeroize(record.PlaintextPayload)
				if identity, ok := identityByCredential[record.ID]; ok {
					account.ExternalAccountID = identity.ExternalAccountID
					account.ExternalSubjectID = identity.ExternalSubjectID
					account.ExternalAccountEmail = identity.ExternalAccountEmail
					account.IdentitySource = identity.ExternalIdentitySource
				} else if record.ExternalAccountID != nil {
					account.ExternalAccountID = strings.TrimSpace(*record.ExternalAccountID)
				}
				credentialCount++
			case errors.Is(resolveErr, credentialstore.ErrCredentialNotFound), errors.Is(resolveErr, credentialstore.ErrCredentialNotActive):
				return ExportResult{}, ErrRecoveryIncomplete
			default:
				return ExportResult{}, resolveErr
			}
		}
		account.Template.Name = strings.TrimSpace(account.Template.Name)
		manifest.Accounts = append(manifest.Accounts, account)
	}
	if err := rows.Err(); err != nil || len(manifest.Accounts) == 0 || len(manifest.Accounts) > 500 {
		return ExportResult{}, ErrInvalidBundle
	}
	var raw json.RawMessage
	if mode == ModeRecovery {
		raw, err = EncodeRecovery(manifest, passphrase, now)
	} else {
		raw, err = EncodeStructure(manifest, now)
	}
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Mode: mode, BundleID: manifest.BundleID, AccountCount: len(manifest.Accounts), CredentialCount: credentialCount, Bundle: raw}, nil
}

func RecoveryItems(manifest Manifest) ([]accountsource.Item, error) {
	if manifest.Mode != ModeRecovery {
		return nil, ErrInvalidBundle
	}
	items := make([]accountsource.Item, 0, len(manifest.Accounts))
	for _, account := range manifest.Accounts {
		if account.Vendor == "" || account.AuthMode == "" || len(account.Credential) == 0 {
			accountsource.ZeroizeItems(items)
			return nil, ErrRecoveryIncomplete
		}
		items = append(items, accountsource.Item{Template: account.Template, Candidate: credentialCandidate(account)})
	}
	if len(items) == 0 {
		return nil, ErrInvalidBundle
	}
	return items, nil
}

func ZeroizeManifest(manifest *Manifest) {
	if manifest == nil {
		return
	}
	for index := range manifest.Accounts {
		privacy.Zeroize(manifest.Accounts[index].Credential)
		manifest.Accounts[index].Credential = nil
	}
}

func credentialCandidate(account Account) credentialacq.CredentialCandidate {
	candidate := credentialacq.CredentialCandidate{
		Vendor: account.Vendor, AuthMode: account.AuthMode,
		Payload:         append([]byte(nil), account.Credential...),
		RedactedContext: map[string]any{"shape": "account_recovery_bundle"},
	}
	credentialacq.AttachIdentity(&candidate, accountident.Identity{
		AccountID: account.ExternalAccountID, SubjectID: account.ExternalSubjectID,
		Email: account.ExternalAccountEmail, Source: account.IdentitySource,
	})
	return candidate
}
