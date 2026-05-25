package hermes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func (s *Service) CreateProfile(ctx context.Context, tenantID, ownerUserID int64, name, kind string, apiKeyID, poolGroupID *int64) (Profile, error) {
	if s == nil || s.store == nil {
		return Profile{}, ErrMisconfigured
	}
	spec := ProfileSpec{
		TenantID: tenantID, OwnerUserID: ownerUserID, Name: name,
		Kind: kind, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
	}
	row, err := createProfileWithStore(ctx, s.store, spec)
	if err != nil {
		return Profile{}, err
	}
	return profileFromRow(row), nil
}

func (s *Service) CreateProfileWithAudit(ctx context.Context, tenantID, ownerUserID int64, name, kind string, apiKeyID, poolGroupID *int64, audit AuditFields) (Profile, error) {
	if s == nil || s.store == nil {
		return Profile{}, ErrMisconfigured
	}
	spec := ProfileSpec{
		TenantID: tenantID, OwnerUserID: ownerUserID, Name: name,
		Kind: kind, APIKeyID: apiKeyID, PoolGroupID: poolGroupID,
	}
	var profile Profile
	err := s.withTx(ctx, func(store Store) error {
		row, err := createProfileWithStore(ctx, store, spec)
		if err != nil {
			return err
		}
		profile = profileFromRow(row)
		audit.TenantID = tenantID
		audit.ActorUserID = ownerUserID
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func createProfileWithStore(ctx context.Context, store Store, spec ProfileSpec) (dbhermes.HermesApiProfile, error) {
	if store == nil {
		return dbhermes.HermesApiProfile{}, ErrMisconfigured
	}
	if err := validateProfileSpecWithStore(ctx, store, spec); err != nil {
		return dbhermes.HermesApiProfile{}, err
	}
	row, err := store.CreateProfile(ctx, dbhermes.CreateProfileParams{
		TenantID: spec.TenantID, OwnerUserID: spec.OwnerUserID, Name: strings.TrimSpace(spec.Name),
		ProfileKind: spec.Kind, APIKeyID: spec.APIKeyID, PoolGroupID: spec.PoolGroupID,
	})
	if err != nil {
		return dbhermes.HermesApiProfile{}, fmt.Errorf("create hermes profile: %w", err)
	}
	return row, nil
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
		audit.ActorUserID = ownerUserID
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

func (s *Service) validateProfileSpec(ctx context.Context, spec ProfileSpec) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	return validateProfileSpecWithStore(ctx, s.store, spec)
}

func validateProfileSpecWithStore(ctx context.Context, store Store, spec ProfileSpec) error {
	if err := validateTenantUser(spec.TenantID, spec.OwnerUserID); err != nil {
		return err
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("%w: profile name is required", ErrInvalidInput)
	}
	switch spec.Kind {
	case APISourceManaged:
		if spec.PoolGroupID != nil {
			return fmt.Errorf("%w: managed profile must not set pool_group_id", ErrInvalidInput)
		}
	case APISourceDedicatedGroup:
		if spec.PoolGroupID == nil || *spec.PoolGroupID <= 0 {
			return fmt.Errorf("%w: dedicated profile requires pool_group_id", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("%w: unknown profile kind", ErrInvalidInput)
	}
	if spec.APIKeyID != nil {
		owner, err := store.GetAPIKeyOwner(ctx, dbhermes.GetAPIKeyOwnerParams{
			APIKeyID: *spec.APIKeyID, TenantID: spec.TenantID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("validate hermes api key owner: %w", err)
		}
		if owner != spec.OwnerUserID {
			return fmt.Errorf("%w: api_key owner mismatch", ErrForbidden)
		}
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
		Name: row.Name, Kind: row.ProfileKind, APIKeyID: row.APIKeyID,
		PoolGroupID: row.PoolGroupID, CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
	}
}
