package admintenant

import (
	"errors"
	"net/url"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

func TestFromQueryResolvesTenantScopeAndErrors(t *testing.T) {
	platform := admin.AdminIdentity{Role: admin.RolePlatformAdmin}
	operator := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}
	cases := []struct {
		name  string
		query string
		ident admin.AdminIdentity
		want  int64
		err   error
	}{
		{name: "platform requires query tenant", ident: platform, err: ErrTenantIDRequired},
		{name: "operator falls back to scope", ident: operator, want: 7},
		{name: "query trims and parses", query: "tenant_id=+9+", ident: platform, want: 9},
		{name: "invalid tenant text", query: "tenant_id=abc", ident: platform, err: ErrInvalidTenantID},
		{name: "invalid tenant zero", query: "tenant_id=0", ident: platform, err: ErrInvalidTenantID},
		{name: "operator cannot cross tenant", query: "tenant_id=8", ident: operator, err: admin.ErrAdminForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatalf("ParseQuery: %v", err)
			}
			got, err := FromQuery(values, tc.ident)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
			if got != tc.want {
				t.Fatalf("tenant=%d want %d", got, tc.want)
			}
		})
	}
}

func TestFromValueRequiresPositiveAuthorizedTenant(t *testing.T) {
	operator := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}
	got, err := FromValue(operator, 0)
	if err != nil || got != 7 {
		t.Fatalf("operator fallback tenant=%d err=%v want 7 nil", got, err)
	}
	if _, err := FromValue(admin.AdminIdentity{Role: admin.RoleTenantOperator}, 0); !errors.Is(err, ErrTenantIDRequired) {
		t.Fatalf("empty operator scope err=%v want ErrTenantIDRequired", err)
	}
	if _, err := FromValue(operator, 8); !errors.Is(err, admin.ErrAdminForbidden) {
		t.Fatalf("cross tenant err=%v want ErrAdminForbidden", err)
	}
}

func TestFromRequiredValueDoesNotFallbackToOperatorScope(t *testing.T) {
	operator := admin.AdminIdentity{Role: admin.RoleTenantOperator, ScopeTenantID: 7}
	if _, err := FromRequiredValue(operator, 0); !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("required value err=%v want ErrInvalidTenantID", err)
	}
	got, err := FromRequiredValue(operator, 7)
	if err != nil || got != 7 {
		t.Fatalf("required value tenant=%d err=%v want 7 nil", got, err)
	}
}
