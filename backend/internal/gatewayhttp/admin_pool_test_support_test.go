package gatewayhttp

import (
	"context"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 这组替身服务于仍在 gatewayhttp 的管理端测试；账号池生产处理器已独立到 adminpoolhttp。
type adminPoolAuthStub struct {
	ident admin.AdminIdentity
	err   error
}

func (s adminPoolAuthStub) Resolve(context.Context, *http.Request) (admin.AdminIdentity, error) {
	return s.ident, s.err
}

type adminPoolStoreStub struct {
	providerFamilies map[int64]string
	getArg           *admindb.GetAdminProviderAccountParams
	get              *admindb.AdminProviderAccountRow
	getErr           error
	audits           []admindb.InsertAdminAuditEventParams
}

func (s *adminPoolStoreStub) GetProviderProtocolForAccountCreate(_ context.Context, arg admindb.GetProviderProtocolForAccountCreateParams) (string, error) {
	if family, ok := s.providerFamilies[arg.ProviderID]; ok {
		return family, nil
	}
	return "openai_chat", nil
}

func (s *adminPoolStoreStub) GetAdminProviderAccount(_ context.Context, arg admindb.GetAdminProviderAccountParams) (admindb.AdminProviderAccountRow, error) {
	s.getArg = &arg
	if s.getErr != nil {
		return admindb.AdminProviderAccountRow{}, s.getErr
	}
	if s.get != nil {
		return *s.get, nil
	}
	return admindb.AdminProviderAccountRow{ID: arg.ID, TenantID: arg.TenantID, ProviderID: 8, AccountType: "api_key"}, nil
}

func (s *adminPoolStoreStub) InsertAdminAuditEvent(_ context.Context, arg admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error) {
	s.audits = append(s.audits, arg)
	return admindb.InsertAdminAuditEventRow{ID: int64(len(s.audits))}, nil
}

type allowAdminPoolCapability struct{}

func (allowAdminPoolCapability) Allowed(context.Context, int64, string) (bool, error) {
	return true, nil
}

type adminPoolCapabilityStub struct {
	allowed bool
	err     error
}

func (s adminPoolCapabilityStub) Allowed(context.Context, int64, string) (bool, error) {
	return s.allowed, s.err
}

func adminPoolAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RolePlatformAdmin}}
}

func providerAccountAdmin() adminPoolAuthStub {
	return adminPoolAuthStub{ident: admin.AdminIdentity{TokenID: 11, Role: admin.RoleTenantOperator, ScopeTenantID: 7}}
}
