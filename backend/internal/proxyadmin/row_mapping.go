package proxyadmin

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func fromCreate(r admindb.CreateProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromUpdate(r admindb.UpdateProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromGet(r admindb.GetProxyRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromList(r admindb.ListProxiesByTenantRow) Proxy {
	return Proxy{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Protocol: r.Protocol, Host: r.Host, Port: r.Port,
		AuthUsername: r.AuthUsername, GroupID: r.GroupID, Status: r.Status, LastCheckAt: tsPtr(r.LastCheckAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
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
