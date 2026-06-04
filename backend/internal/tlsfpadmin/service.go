package tlsfpadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Querier is the subset of *admindb.Queries this service needs. Declared here so
// handler/service tests can inject a mock. *admindb.Queries satisfies it.
type Querier interface {
	CreateTLSFingerprintProfile(context.Context, admindb.CreateTLSFingerprintProfileParams) (admindb.CreateTLSFingerprintProfileRow, error)
	GetTLSFingerprintProfile(context.Context, admindb.GetTLSFingerprintProfileParams) (admindb.GetTLSFingerprintProfileRow, error)
	UpdateTLSFingerprintProfile(context.Context, admindb.UpdateTLSFingerprintProfileParams) (admindb.UpdateTLSFingerprintProfileRow, error)
	SetTLSFingerprintProfileStatus(context.Context, admindb.SetTLSFingerprintProfileStatusParams) error
	SoftDeleteTLSFingerprintProfile(context.Context, admindb.SoftDeleteTLSFingerprintProfileParams) error
	ListTLSFingerprintProfilesByTenant(context.Context, int64) ([]admindb.ListTLSFingerprintProfilesByTenantRow, error)
}

// Service validates inputs, maps DB errors to sentinels, and (critically)
// detects not-found for the `:exec` SetStatus/SoftDelete queries via a
// pre-flight Get — the raw queries return nil on zero rows, which would
// otherwise silently 200 a delete/status-change on a missing or wrong-tenant id.
type Service struct{ q Querier }

// New builds a Service. q is typically *admindb.Queries.
func New(q Querier) *Service { return &Service{q: q} }

func (s *Service) List(ctx context.Context, tenantID int64) ([]Profile, error) {
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.q.ListTLSFingerprintProfilesByTenant(ctx, tenantID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Profile, 0, len(rows)) // empty slice, never nil (JSON [] not null)
	for _, r := range rows {
		out = append(out, fromListRow(r))
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id int64) (Profile, error) {
	if tenantID <= 0 || id <= 0 {
		return Profile{}, ErrInvalidInput
	}
	row, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: tenantID, ID: id})
	if err != nil {
		return Profile{}, mapErr(err)
	}
	return fromGet(row), nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Profile, error) {
	if in.TenantID <= 0 || strings.TrimSpace(in.Name) == "" {
		return Profile{}, ErrInvalidInput
	}
	row, err := s.q.CreateTLSFingerprintProfile(ctx, admindb.CreateTLSFingerprintProfileParams{
		TenantID: in.TenantID, Name: strings.TrimSpace(in.Name), Description: in.Description,
		GreaseEnabled: in.GreaseEnabled, CipherSuites: in.CipherSuites, SupportedCurves: in.SupportedCurves,
		EcPointFormats: in.EcPointFormats, SignatureAlgorithms: in.SignatureAlgorithms, AlpnProtocols: in.AlpnProtocols,
		TLSSupportedVersions: in.TLSSupportedVersions, KeyShareGroups: in.KeyShareGroups, PskModes: in.PskModes,
		ExtensionsOrder: in.ExtensionsOrder, ExpectedJA3Hash: in.ExpectedJA3Hash,
	})
	if err != nil {
		return Profile{}, mapErr(err)
	}
	return fromCreate(row), nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (Profile, error) {
	if in.TenantID <= 0 || in.ID <= 0 || strings.TrimSpace(in.Name) == "" {
		return Profile{}, ErrInvalidInput
	}
	row, err := s.q.UpdateTLSFingerprintProfile(ctx, admindb.UpdateTLSFingerprintProfileParams{
		Name: strings.TrimSpace(in.Name), Description: in.Description, GreaseEnabled: in.GreaseEnabled,
		CipherSuites: in.CipherSuites, SupportedCurves: in.SupportedCurves, EcPointFormats: in.EcPointFormats,
		SignatureAlgorithms: in.SignatureAlgorithms, AlpnProtocols: in.AlpnProtocols, TLSSupportedVersions: in.TLSSupportedVersions,
		KeyShareGroups: in.KeyShareGroups, PskModes: in.PskModes, ExtensionsOrder: in.ExtensionsOrder,
		ExpectedJA3Hash: in.ExpectedJA3Hash, TenantID: in.TenantID, ID: in.ID,
	})
	if err != nil {
		return Profile{}, mapErr(err) // UpdateTLSFingerprintProfile is :one -> ErrNoRows on 0 rows -> ErrNotFound
	}
	return fromUpdate(row), nil
}

// SetStatus changes status. Pre-flight Get maps not-found (SetStatus is :exec and
// returns nil on zero rows); the re-Get returns the post-change row so the
// response reflects last_validated_at. TOCTOU between the checks is acceptable
// for a single-admin CRUD surface (concurrent soft-delete would surface as a
// 404 on re-Get, which is correct).
func (s *Service) SetStatus(ctx context.Context, in SetStatusInput) (Profile, error) {
	if in.TenantID <= 0 || in.ID <= 0 {
		return Profile{}, ErrInvalidInput
	}
	if !adminSettableStatuses[in.Status] {
		return Profile{}, ErrInvalidStatus
	}
	if _, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: in.TenantID, ID: in.ID}); err != nil {
		return Profile{}, mapErr(err)
	}
	if err := s.q.SetTLSFingerprintProfileStatus(ctx, admindb.SetTLSFingerprintProfileStatusParams{Status: in.Status, TenantID: in.TenantID, ID: in.ID}); err != nil {
		return Profile{}, mapErr(err)
	}
	row, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: in.TenantID, ID: in.ID})
	if err != nil {
		return Profile{}, mapErr(err)
	}
	return fromGet(row), nil
}

// Delete soft-deletes. Pre-flight Get maps not-found (SoftDelete is :exec and
// returns nil on zero rows — without this, a delete on a missing or wrong-tenant
// id would silently 200, an information-leakage oracle).
func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	if _, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: tenantID, ID: id}); err != nil {
		return mapErr(err)
	}
	if err := s.q.SoftDeleteTLSFingerprintProfile(ctx, admindb.SoftDeleteTLSFingerprintProfileParams{TenantID: tenantID, ID: id}); err != nil {
		return mapErr(err)
	}
	return nil
}

// mapErr maps raw DB errors to sentinels. ErrBackend wraps the raw error for
// internal logging; the HTTP layer must NOT echo it .
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicateName
	}
	return fmt.Errorf("%w: %v", ErrBackend, err)
}

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func i32(a []int32) []int32 {
	if a == nil {
		return []int32{}
	}
	return a
}

func strs(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

func fromGet(r admindb.GetTLSFingerprintProfileRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromCreate(r admindb.CreateTLSFingerprintProfileRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromUpdate(r admindb.UpdateTLSFingerprintProfileRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromListRow(r admindb.ListTLSFingerprintProfilesByTenantRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}
