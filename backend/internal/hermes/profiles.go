package hermes

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	APISourceExternal     = "external_openai_compatible"
	hermesProfileVendor   = "hermes_model"
	hermesProfileAuthMode = "api_key"
)

type ResolvedProfileCredential struct {
	ProfileID int64
	TenantID  int64
	BaseURL   string
	APIKey    []byte
}

type profileCredentialRotator interface {
	RotateProfileCredential(context.Context, dbhermes.RotateProfileCredentialParams) (dbhermes.HermesApiProfile, error)
}

func (s *Service) CreateProfile(ctx context.Context, tenantID, ownerUserID int64, name, baseURL, apiKey string) (Profile, error) {
	if s == nil || s.store == nil {
		return Profile{}, ErrMisconfigured
	}
	spec := ProfileSpec{
		TenantID: tenantID, OwnerUserID: ownerUserID, Name: name,
		BaseURL: baseURL, APIKey: apiKey,
	}
	row, err := s.createProfileWithStore(ctx, s.store, spec)
	if err != nil {
		return Profile{}, err
	}
	return profileFromRow(row), nil
}

func (s *Service) CreateProfileWithAudit(ctx context.Context, tenantID, ownerUserID int64, name, baseURL, apiKey string, audit AuditFields) (Profile, error) {
	if s == nil || s.store == nil {
		return Profile{}, ErrMisconfigured
	}
	spec := ProfileSpec{
		TenantID: tenantID, OwnerUserID: ownerUserID, Name: name,
		BaseURL: baseURL, APIKey: apiKey,
	}
	var profile Profile
	err := s.withTx(ctx, func(store Store) error {
		row, err := s.createProfileWithStore(ctx, store, spec)
		if err != nil {
			return err
		}
		profile = profileFromRow(row)
		audit.TenantID = tenantID
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s *Service) createProfileWithStore(ctx context.Context, store Store, spec ProfileSpec) (dbhermes.HermesApiProfile, error) {
	if store == nil {
		return dbhermes.HermesApiProfile{}, ErrMisconfigured
	}
	if err := validateProfileSpec(spec); err != nil {
		return dbhermes.HermesApiProfile{}, err
	}
	normalizedBaseURL, _ := NormalizeExternalBaseURL(spec.BaseURL)
	bindingID, err := randomPositiveInt64()
	if err != nil {
		return dbhermes.HermesApiProfile{}, err
	}
	envelope, fingerprint, hint, err := s.encryptProfileAPIKey(ctx, spec.TenantID, bindingID, 1, spec.APIKey)
	if err != nil {
		return dbhermes.HermesApiProfile{}, err
	}
	row, err := store.CreateProfile(ctx, dbhermes.CreateProfileParams{
		TenantID: spec.TenantID, OwnerUserID: spec.OwnerUserID, Name: strings.TrimSpace(spec.Name),
		ProfileKind: APISourceExternal, BaseUrl: normalizedBaseURL,
		EncryptedApiKey: envelope.Ciphertext, EncryptionScheme: envelope.EncryptionScheme,
		KeyID: envelope.KeyID, Nonce: envelope.Nonce, AadHash: envelope.AADHash,
		ApiKeyFingerprint: fingerprint, ApiKeyHint: hint, CredentialVersion: 1,
		SecretBindingID: bindingID,
	})
	if err != nil {
		return dbhermes.HermesApiProfile{}, fmt.Errorf("create hermes profile: %w", err)
	}
	return row, nil
}

func (s *Service) RotateProfileWithAudit(ctx context.Context, profileID, tenantID, ownerUserID int64, name, baseURL, apiKey string, audit AuditFields) (Profile, error) {
	if s == nil || s.store == nil {
		return Profile{}, ErrMisconfigured
	}
	spec := ProfileSpec{TenantID: tenantID, OwnerUserID: ownerUserID, Name: name, BaseURL: baseURL, APIKey: apiKey}
	if err := validateProfileSpec(spec); err != nil {
		return Profile{}, err
	}
	normalizedBaseURL, _ := NormalizeExternalBaseURL(spec.BaseURL)
	var profile Profile
	err := s.withTx(ctx, func(store Store) error {
		row, err := store.GetProfile(ctx, dbhermes.GetProfileParams{ID: profileID, TenantID: tenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get hermes profile for rotation: %w", err)
		}
		if row.OwnerUserID != ownerUserID {
			return ErrNotFound
		}
		nextVersion := row.CredentialVersion + 1
		envelope, fingerprint, hint, err := s.encryptProfileAPIKey(ctx, tenantID, row.SecretBindingID, nextVersion, apiKey)
		if err != nil {
			return err
		}
		rotator, ok := store.(profileCredentialRotator)
		if !ok {
			return ErrMisconfigured
		}
		updated, err := rotator.RotateProfileCredential(ctx, dbhermes.RotateProfileCredentialParams{
			ID: profileID, TenantID: tenantID, Name: strings.TrimSpace(name), BaseUrl: normalizedBaseURL,
			EncryptedApiKey: envelope.Ciphertext, EncryptionScheme: envelope.EncryptionScheme,
			KeyID: envelope.KeyID, Nonce: envelope.Nonce, AadHash: envelope.AADHash,
			ApiKeyFingerprint: fingerprint, ApiKeyHint: hint,
			NewCredentialVersion: nextVersion, ExpectedCredentialVersion: row.CredentialVersion,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return fmt.Errorf("rotate hermes profile credential: %w", err)
		}
		profile = profileFromRow(updated)
		audit.TenantID = tenantID
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s *Service) ResolveProfileCredential(ctx context.Context, profileID, tenantID int64) (ResolvedProfileCredential, error) {
	if s == nil || s.store == nil || s.profileCredentialKeys == nil {
		return ResolvedProfileCredential{}, ErrMisconfigured
	}
	row, err := s.store.GetProfile(ctx, dbhermes.GetProfileParams{ID: profileID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedProfileCredential{}, ErrNotFound
	}
	if err != nil {
		return ResolvedProfileCredential{}, fmt.Errorf("resolve hermes profile: %w", err)
	}
	plaintext, err := credentialstore.NewCipher(s.profileCredentialKeys).Decrypt(ctx, credentialstore.Envelope{
		Ciphertext: row.EncryptedApiKey, Nonce: row.Nonce, KeyID: row.KeyID,
		EncryptionScheme: row.EncryptionScheme, AADHash: row.AadHash,
	}, profileAAD(row.TenantID, row.SecretBindingID, row.CredentialVersion))
	if err != nil {
		return ResolvedProfileCredential{}, fmt.Errorf("resolve hermes profile credential: %w", err)
	}
	return ResolvedProfileCredential{ProfileID: row.ID, TenantID: row.TenantID, BaseURL: row.BaseUrl, APIKey: plaintext}, nil
}

func (s *Service) ListProfilesByTenant(ctx context.Context, tenantID int64) ([]Profile, error) {
	if s == nil || s.store == nil {
		return nil, ErrMisconfigured
	}
	if tenantID <= 0 {
		return nil, fmt.Errorf("%w: tenant_id must be positive", ErrInvalidInput)
	}
	rows, err := s.store.ListProfilesByTenant(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list hermes tenant profiles: %w", err)
	}
	return profilesFromRows(rows), nil
}

func (s *Service) ListProfilesByOwner(ctx context.Context, tenantID, ownerUserID int64) ([]Profile, error) {
	if s == nil || s.store == nil {
		return nil, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, ownerUserID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListProfilesByOwner(ctx, dbhermes.ListProfilesByOwnerParams{
		TenantID: tenantID, OwnerUserID: ownerUserID,
	})
	if err != nil {
		return nil, fmt.Errorf("list hermes owner profiles: %w", err)
	}
	return profilesFromRows(rows), nil
}

func (s *Service) GetProfile(ctx context.Context, profileID, tenantID int64) (Profile, error) {
	if s == nil || s.store == nil {
		return Profile{}, ErrMisconfigured
	}
	return getProfileWithStore(ctx, s.store, profileID, tenantID)
}

func getProfileWithStore(ctx context.Context, store Store, profileID, tenantID int64) (Profile, error) {
	if store == nil {
		return Profile{}, ErrMisconfigured
	}
	if tenantID <= 0 || profileID <= 0 {
		return Profile{}, fmt.Errorf("%w: profile_id and tenant_id must be positive", ErrInvalidInput)
	}
	row, err := store.GetProfile(ctx, dbhermes.GetProfileParams{ID: profileID, TenantID: tenantID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("get hermes profile: %w", err)
	}
	return profileFromRow(row), nil
}

func (s *Service) DeleteProfile(ctx context.Context, profileID, tenantID int64) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	return deleteProfileWithStore(ctx, s.store, profileID, tenantID)
}

func (s *Service) DeleteProfileWithAudit(ctx context.Context, profileID, tenantID, ownerUserID int64, audit AuditFields) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, ownerUserID); err != nil {
		return err
	}
	return s.withTx(ctx, func(store Store) error {
		profile, err := getProfileWithStore(ctx, store, profileID, tenantID)
		if err != nil {
			return err
		}
		if profile.OwnerUserID != ownerUserID {
			return ErrNotFound
		}
		if err := deleteProfileWithStore(ctx, store, profileID, tenantID); err != nil {
			return err
		}
		audit.TenantID = tenantID
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
}

func deleteProfileWithStore(ctx context.Context, store Store, profileID, tenantID int64) error {
	if store == nil {
		return ErrMisconfigured
	}
	if tenantID <= 0 || profileID <= 0 {
		return fmt.Errorf("%w: profile_id and tenant_id must be positive", ErrInvalidInput)
	}
	inUse, err := store.ProfileInUse(ctx, dbhermes.ProfileInUseParams{TenantID: tenantID, ProfileID: profileID})
	if err != nil {
		return fmt.Errorf("check hermes profile usage: %w", err)
	}
	if inUse {
		return ErrProfileInUse
	}
	rows, err := store.DeleteProfile(ctx, dbhermes.DeleteProfileParams{ID: profileID, TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("delete hermes profile: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func validateProfileSpec(spec ProfileSpec) error {
	if err := validateTenantUser(spec.TenantID, spec.OwnerUserID); err != nil {
		return err
	}
	if strings.TrimSpace(spec.Name) == "" || len(strings.TrimSpace(spec.Name)) > 255 {
		return fmt.Errorf("%w: profile name must contain 1 to 255 bytes", ErrInvalidInput)
	}
	normalized, err := NormalizeExternalBaseURL(spec.BaseURL)
	if err != nil {
		return err
	}
	spec.BaseURL = normalized
	if strings.TrimSpace(spec.APIKey) == "" || len(spec.APIKey) > 8192 {
		return fmt.Errorf("%w: api_key must contain 1 to 8192 bytes", ErrInvalidInput)
	}
	return nil
}

func profilesFromRows(rows []dbhermes.HermesApiProfile) []Profile {
	out := make([]Profile, 0, len(rows))
	for _, row := range rows {
		out = append(out, profileFromRow(row))
	}
	return out
}

func profileFromRow(row dbhermes.HermesApiProfile) Profile {
	return Profile{
		ID: row.ID, TenantID: row.TenantID, OwnerUserID: row.OwnerUserID,
		Name: row.Name, Kind: row.ProfileKind,
		BaseURL: row.BaseUrl, APIKeyMasked: row.ApiKeyHint,
		CredentialVersion: row.CredentialVersion, CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}
}

func (s *Service) encryptProfileAPIKey(ctx context.Context, tenantID, bindingID int64, version int32, apiKey string) (credentialstore.Envelope, string, string, error) {
	if s == nil || s.profileCredentialKeys == nil {
		return credentialstore.Envelope{}, "", "", ErrMisconfigured
	}
	secret := []byte(strings.TrimSpace(apiKey))
	defer privacy.Zeroize(secret)
	envelope, err := credentialstore.NewCipher(s.profileCredentialKeys).Encrypt(ctx, secret, profileAAD(tenantID, bindingID, version))
	if err != nil {
		return credentialstore.Envelope{}, "", "", err
	}
	key, err := s.profileCredentialKeys.CurrentKey(ctx)
	if err != nil {
		return credentialstore.Envelope{}, "", "", err
	}
	defer privacy.Zeroize(key.Material)
	fingerprint := credentialstore.HMACFingerprint(key, fmt.Sprintf("hermes-profile:%d:%d", tenantID, bindingID), secret)
	return envelope, fingerprint, maskAPIKey(string(secret)), nil
}

func profileAAD(tenantID, bindingID int64, version int32) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: tenantID, ProviderAccountID: bindingID,
		Vendor: hermesProfileVendor, AuthMode: hermesProfileAuthMode, Version: version,
	}
}

func randomPositiveInt64() (int64, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 63)
	value, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return 0, fmt.Errorf("generate hermes profile binding: %w", err)
	}
	if value.Sign() == 0 {
		return randomPositiveInt64()
	}
	return value.Int64(), nil
}

func maskAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if len(apiKey) <= 4 {
		return "****"
	}
	return "****" + apiKey[len(apiKey)-4:]
}

func NormalizeExternalBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", fmt.Errorf("%w: base_url must contain 1 to 2048 bytes", ErrInvalidInput)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return "", fmt.Errorf("%w: base_url must be an absolute https URL", ErrInvalidInput)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", fmt.Errorf("%w: base_url must not contain credentials, query, fragment, or encoded path", ErrInvalidInput)
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if strings.HasSuffix(host, ".") || strings.ContainsAny(u.Path, "\\\r\n\t") {
		return "", fmt.Errorf("%w: base_url host or path is unsafe", ErrInvalidInput)
	}
	if ip := net.ParseIP(host); ip != nil && !auth.IsPublicOAuthIP(ip) {
		return "", fmt.Errorf("%w: base_url must resolve to a public service", ErrInvalidInput)
	}
	cleanPath := path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if cleanPath == "/" {
		cleanPath = ""
	}
	cleanPath = strings.TrimSuffix(cleanPath, "/chat/completions")
	u.Scheme = "https"
	u.Path = strings.TrimRight(cleanPath, "/")
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}
