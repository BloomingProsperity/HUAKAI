package admintenant

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

var (
	ErrTenantIDRequired = errors.New("admintenant: tenant_id required")
	ErrInvalidTenantID  = errors.New("admintenant: invalid tenant_id")
)

func FromQuery(values url.Values, ident admin.AdminIdentity) (int64, error) {
	raw := strings.TrimSpace(values.Get("tenant_id"))
	if raw == "" {
		return FromValue(ident, 0)
	}
	tenantID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || tenantID <= 0 {
		return 0, ErrInvalidTenantID
	}
	return FromValue(ident, tenantID)
}

func FromValue(ident admin.AdminIdentity, tenantID int64) (int64, error) {
	if tenantID == 0 && ident.Role == admin.RoleTenantOperator {
		tenantID = ident.ScopeTenantID
	}
	return fromResolvedValue(ident, tenantID, ErrTenantIDRequired)
}

func FromRequiredValue(ident admin.AdminIdentity, tenantID int64) (int64, error) {
	return fromResolvedValue(ident, tenantID, ErrInvalidTenantID)
}

func fromResolvedValue(ident admin.AdminIdentity, tenantID int64, nonPositiveErr error) (int64, error) {
	if tenantID <= 0 {
		return 0, nonPositiveErr
	}
	if err := ident.CanIssueForTenant(tenantID); err != nil {
		return 0, err
	}
	return tenantID, nil
}
