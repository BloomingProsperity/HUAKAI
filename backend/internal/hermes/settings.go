package hermes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func (s *Service) EnableForUser(ctx context.Context, tenantID, userID int64, apiSource string, profileID *int64, model string) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	_, err := enableForUserWithStore(ctx, s.store, tenantID, userID, apiSource, profileID, model)
	return err
}

func (s *Service) EnableForUserWithAudit(ctx context.Context, tenantID, userID int64, apiSource string, profileID *int64, model string, audit AuditFields) (Settings, error) {
	if s == nil || s.store == nil {
		return Settings{}, ErrMisconfigured
	}
	var settings Settings
	err := s.withTx(ctx, func(store Store) error {
		row, err := enableForUserWithStore(ctx, store, tenantID, userID, apiSource, profileID, model)
		if err != nil {
			return err
		}
		settings = settingsFromRow(row)
		audit.TenantID = tenantID
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func enableForUserWithStore(ctx context.Context, store Store, tenantID, userID int64, apiSource string, profileID *int64, model string) (dbhermes.HermesSetting, error) {
	if store == nil {
		return dbhermes.HermesSetting{}, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, userID); err != nil {
		return dbhermes.HermesSetting{}, err
	}
	if apiSource == "" {
		apiSource = APISourceExternal
	}
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 255 {
		return dbhermes.HermesSetting{}, fmt.Errorf("%w: model must contain 1 to 255 bytes", ErrInvalidInput)
	}
	if err := validateSettingsSourceWithStore(ctx, store, tenantID, userID, apiSource, profileID); err != nil {
		return dbhermes.HermesSetting{}, err
	}
	row, err := store.UpsertSettings(ctx, dbhermes.UpsertSettingsParams{
		TenantID: tenantID, UserID: userID, Enabled: true,
		APISource: apiSource, ProfileID: profileID, ModelKey: model,
	})
	if err != nil {
		return dbhermes.HermesSetting{}, fmt.Errorf("enable hermes: %w", err)
	}
	return row, nil
}

func (s *Service) DisableForUser(ctx context.Context, tenantID, userID int64) error {
	if s == nil || s.store == nil {
		return ErrMisconfigured
	}
	_, err := disableForUserWithStore(ctx, s.store, tenantID, userID)
	return err
}

func (s *Service) DisableForUserWithAudit(ctx context.Context, tenantID, userID int64, audit AuditFields) (Settings, error) {
	if s == nil || s.store == nil {
		return Settings{}, ErrMisconfigured
	}
	var settings Settings
	err := s.withTx(ctx, func(store Store) error {
		row, err := disableForUserWithStore(ctx, store, tenantID, userID)
		if err != nil {
			return err
		}
		settings = settingsFromRow(row)
		audit.TenantID = tenantID
		audit.Result = AuditResultSuccess
		return recordAuditWithStore(ctx, store, audit)
	})
	if err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func disableForUserWithStore(ctx context.Context, store Store, tenantID, userID int64) (dbhermes.HermesSetting, error) {
	if store == nil {
		return dbhermes.HermesSetting{}, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, userID); err != nil {
		return dbhermes.HermesSetting{}, err
	}
	if row, err := store.DisableHermes(ctx, dbhermes.DisableHermesParams{TenantID: tenantID, UserID: userID}); err == nil {
		return row, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return dbhermes.HermesSetting{}, fmt.Errorf("disable hermes: %w", err)
	}
	row, err := store.UpsertSettings(ctx, dbhermes.UpsertSettingsParams{
		TenantID: tenantID, UserID: userID, Enabled: false, APISource: APISourceExternal, ModelKey: "",
	})
	if err != nil {
		return dbhermes.HermesSetting{}, fmt.Errorf("disable hermes default row: %w", err)
	}
	return row, nil
}

func (s *Service) GetSettings(ctx context.Context, tenantID, userID int64) (Settings, error) {
	if s == nil || s.store == nil {
		return Settings{}, ErrMisconfigured
	}
	if err := validateTenantUser(tenantID, userID); err != nil {
		return Settings{}, err
	}
	row, err := s.store.GetSettings(ctx, dbhermes.GetSettingsParams{TenantID: tenantID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{TenantID: tenantID, UserID: userID, APISource: APISourceExternal}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("get hermes settings: %w", err)
	}
	return settingsFromRow(row), nil
}

func validateSettingsSourceWithStore(ctx context.Context, store Store, tenantID, userID int64, apiSource string, profileID *int64) error {
	switch apiSource {
	case APISourceExternal:
		if profileID == nil || *profileID <= 0 {
			return fmt.Errorf("%w: external source requires profile_id", ErrInvalidInput)
		}
		profile, err := getProfileWithStore(ctx, store, *profileID, tenantID)
		if err != nil {
			return err
		}
		if profile.Kind != APISourceExternal {
			return fmt.Errorf("%w: profile kind does not match api_source", ErrInvalidInput)
		}
		if profile.OwnerUserID != userID {
			return ErrProfileNotOwned
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown api_source", ErrInvalidInput)
	}
}

func validateTenantUser(tenantID, userID int64) error {
	if tenantID <= 0 || userID <= 0 {
		return fmt.Errorf("%w: tenant_id and user_id must be positive", ErrInvalidInput)
	}
	return nil
}

func settingsFromRow(row dbhermes.HermesSetting) Settings {
	return Settings{
		TenantID: row.TenantID, UserID: row.UserID, Enabled: row.Enabled,
		APISource: row.APISource, ProfileID: row.ProfileID, Model: row.ModelKey,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}
