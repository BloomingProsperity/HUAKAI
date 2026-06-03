package proxyadmin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/jackc/pgx/v5/pgtype"
)

type Querier interface {
	CreateProxy(context.Context, admindb.CreateProxyParams) (admindb.CreateProxyRow, error)
	UpdateProxy(context.Context, admindb.UpdateProxyParams) (admindb.UpdateProxyRow, error)
}

type Service struct {
	q    Querier
	keys credentialstore.KeyProvider
}

func New(q Querier, keys credentialstore.KeyProvider) *Service {
	return &Service{q: q, keys: keys}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Proxy, error) {
	if err := validateCreate(in); err != nil {
		return Proxy{}, err
	}
	secret, err := s.encryptAuthSecret(ctx, in.TenantID, in.AuthSecret)
	if err != nil {
		return Proxy{}, err
	}
	row, err := s.q.CreateProxy(ctx, admindb.CreateProxyParams{
		TenantID:     in.TenantID,
		Name:         strings.TrimSpace(in.Name),
		Protocol:     strings.TrimSpace(in.Protocol),
		Host:         strings.TrimSpace(in.Host),
		Port:         in.Port,
		AuthUsername: cleanPtr(in.AuthUsername),
		AuthSecret:   secret,
		Status:       statusOrActive(in.Status),
	})
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromCreate(row), nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (Proxy, error) {
	if err := validateUpdate(in); err != nil {
		return Proxy{}, err
	}
	secret, err := s.encryptAuthSecret(ctx, in.TenantID, in.AuthSecret)
	if err != nil {
		return Proxy{}, err
	}
	row, err := s.q.UpdateProxy(ctx, admindb.UpdateProxyParams{
		TenantID:     in.TenantID,
		ID:           in.ID,
		Name:         strings.TrimSpace(in.Name),
		Protocol:     strings.TrimSpace(in.Protocol),
		Host:         strings.TrimSpace(in.Host),
		Port:         in.Port,
		AuthUsername: cleanPtr(in.AuthUsername),
		AuthSecret:   secret,
	})
	if err != nil {
		return Proxy{}, mapErr(err)
	}
	return fromUpdate(row), nil
}

func (s *Service) encryptAuthSecret(ctx context.Context, tenantID int64, raw *string) (*string, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	stored, err := proxysecret.Encode(ctx, s.keys, tenantID, *raw)
	if err != nil {
		return nil, fmt.Errorf("%w: encrypt proxy auth_secret: %v", ErrBackend, err)
	}
	return &stored, nil
}

func validateCreate(in CreateInput) error {
	return validateCommon(in.TenantID, 1, in.Name, in.Protocol, in.Host, in.Port, in.Status)
}

func validateUpdate(in UpdateInput) error {
	return validateCommon(in.TenantID, in.ID, in.Name, in.Protocol, in.Host, in.Port, "active")
}

func validateCommon(tenantID, id int64, name, protocol, host string, port int32, status string) error {
	if tenantID <= 0 || id <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(protocol) == "" || strings.TrimSpace(host) == "" || port <= 0 {
		return ErrInvalidInput
	}
	if !validStatus(statusOrActive(status)) {
		return ErrInvalidStatus
	}
	return nil
}

func validStatus(status string) bool {
	switch status {
	case "active", "disabled", "dead":
		return true
	default:
		return false
	}
}

func statusOrActive(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "active"
	}
	return status
}

func cleanPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrBackend, err)
}

func fromCreate(r admindb.CreateProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, Status: r.Status, CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromUpdate(r admindb.UpdateProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, Status: r.Status, CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
