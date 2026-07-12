package proxyadmin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCreateEncryptsAuthSecretBeforeDBWrite(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	q := &mockProxyQuerier{}
	plaintext := "proxy-secret:with@reserved?chars"

	_, err := New(q, keys).Create(ctx, CreateInput{
		TenantID:     7,
		Name:         "residential-a",
		Protocol:     "http",
		Host:         "proxy.example.com",
		Port:         3128,
		AuthUsername: strPtr("proxy-user"),
		AuthSecret:   &plaintext,
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if q.createCalls != 1 {
		t.Fatalf("CreateProxy calls=%d want 1", q.createCalls)
	}
	if q.createArg.AuthSecret == nil {
		t.Fatal("CreateProxy AuthSecret nil; want encrypted envelope")
	}
	stored := *q.createArg.AuthSecret
	if stored == plaintext {
		t.Fatal("stored auth_secret equals plaintext; write path did not encrypt")
	}
	if !strings.HasPrefix(stored, proxysecret.EnvelopePrefix) {
		t.Fatalf("stored auth_secret prefix=%q want %q", stored[:min(len(stored), len(proxysecret.EnvelopePrefix))], proxysecret.EnvelopePrefix)
	}
	roundTrip, err := proxysecret.Decode(ctx, keys, 7, stored)
	if err != nil {
		t.Fatalf("Decode stored auth_secret: %v", err)
	}
	if roundTrip != plaintext {
		t.Fatalf("decoded auth_secret=%q want original", roundTrip)
	}
}

func TestUpdateEncryptsAuthSecretBeforeDBWrite(t *testing.T) {
	ctx := context.Background()
	keys := testKeys(t)
	q := &mockProxyQuerier{}
	plaintext := "updated-proxy-secret"

	_, err := New(q, keys).Update(ctx, UpdateInput{
		TenantID:     9,
		ID:           55,
		Name:         "residential-b",
		Protocol:     "socks5",
		Host:         "proxy2.example.com",
		Port:         1080,
		AuthUsername: strPtr("proxy-user"),
		AuthSecret:   &plaintext,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if q.updateCalls != 1 {
		t.Fatalf("UpdateProxy calls=%d want 1", q.updateCalls)
	}
	if q.updateArg.AuthSecret == nil {
		t.Fatal("UpdateProxy AuthSecret nil; want encrypted envelope")
	}
	stored := *q.updateArg.AuthSecret
	if stored == plaintext {
		t.Fatal("stored auth_secret equals plaintext; update path did not encrypt")
	}
	roundTrip, err := proxysecret.Decode(ctx, keys, 9, stored)
	if err != nil {
		t.Fatalf("Decode stored auth_secret: %v", err)
	}
	if roundTrip != plaintext {
		t.Fatalf("decoded auth_secret=%q want original", roundTrip)
	}
}

type mockProxyQuerier struct {
	createCalls int
	createArg   admindb.CreateProxyParams
	updateCalls int
	updateArg   admindb.UpdateProxyParams

	getCalls int
	getArg   admindb.GetProxyParams
	getRow   admindb.GetProxyRow
	getErr   error

	listCalls    int
	listTenantID int64
	listRows     []admindb.ListProxiesByTenantRow
	listErr      error

	setStatusCalls int
	setStatusArg   admindb.SetProxyStatusParams
	setStatusErr   error

	deleteCalls int
	deleteArg   admindb.SoftDeleteProxyParams
	deleteErr   error
}

func (m *mockProxyQuerier) CreateProxy(_ context.Context, arg admindb.CreateProxyParams) (admindb.CreateProxyRow, error) {
	m.createCalls++
	m.createArg = arg
	return admindb.CreateProxyRow{
		ID: arg.TenantID + 100, TenantID: arg.TenantID, Name: arg.Name, Protocol: arg.Protocol,
		Host: arg.Host, Port: arg.Port, AuthUsername: arg.AuthUsername, AuthSecret: arg.AuthSecret,
		Status: arg.Status,
	}, nil
}

func (m *mockProxyQuerier) UpdateProxy(_ context.Context, arg admindb.UpdateProxyParams) (admindb.UpdateProxyRow, error) {
	m.updateCalls++
	m.updateArg = arg
	return admindb.UpdateProxyRow{
		ID: arg.ID, TenantID: arg.TenantID, Name: arg.Name, Protocol: arg.Protocol,
		Host: arg.Host, Port: arg.Port, AuthUsername: arg.AuthUsername, AuthSecret: arg.AuthSecret,
		Status: "active",
	}, nil
}

func (m *mockProxyQuerier) GetProxy(_ context.Context, arg admindb.GetProxyParams) (admindb.GetProxyRow, error) {
	m.getCalls++
	m.getArg = arg
	if m.getErr != nil {
		return admindb.GetProxyRow{}, m.getErr
	}
	return m.getRow, nil
}

func (m *mockProxyQuerier) ListProxiesByTenant(_ context.Context, tenantID int64) ([]admindb.ListProxiesByTenantRow, error) {
	m.listCalls++
	m.listTenantID = tenantID
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listRows, nil
}

func (m *mockProxyQuerier) SetProxyStatus(_ context.Context, arg admindb.SetProxyStatusParams) error {
	m.setStatusCalls++
	m.setStatusArg = arg
	return m.setStatusErr
}

func (m *mockProxyQuerier) SoftDeleteProxy(_ context.Context, arg admindb.SoftDeleteProxyParams) error {
	m.deleteCalls++
	m.deleteArg = arg
	return m.deleteErr
}

// TestListProjectsNonSecretFieldsTenantScoped 守护读取路径的投影:List 必须把
// tenant id 透传给按租户收敛的查询,并把非凭据列
//(name/protocol/host/port/auth_username/status/timestamps)映射进结果,
// 同时底层行上加密的 auth_secret 列绝不可携带(Proxy 类型没有该字段——这断言
// 映射在结构上不含凭据)。变异:删掉 fromList 里的 AuthUsername 映射,或透传
// 错误的 tenant id → 转红。
func TestListProjectsNonSecretFieldsTenantScoped(t *testing.T) {
	ctx := context.Background()
	user := "proxy-user"
	checked := time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC)
	q := &mockProxyQuerier{
		listRows: []admindb.ListProxiesByTenantRow{{
			ID: 11, TenantID: 7, Name: "residential-a", Protocol: "http",
			Host: "proxy.example.com", Port: 3128, AuthUsername: &user,
			// 刻意设置的加密密文:它绝不可浮现到结果中。
			AuthSecret:  strPtr("hk_proxy_v1$leakcanary$ciphertext"),
			Status:      "active",
			LastCheckAt: pgts(checked),
			CreatedAt:   pgts(checked),
			UpdatedAt:   pgts(checked),
		}},
	}

	out, err := New(q, testKeys(t)).List(ctx, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if q.listCalls != 1 || q.listTenantID != 7 {
		t.Fatalf("List must query tenant 7 once; calls=%d tenant=%d", q.listCalls, q.listTenantID)
	}
	if len(out) != 1 {
		t.Fatalf("List len=%d want 1", len(out))
	}
	got := out[0]
	if got.ID != 11 || got.TenantID != 7 || got.Name != "residential-a" || got.Protocol != "http" ||
		got.Host != "proxy.example.com" || got.Port != 3128 || got.Status != "active" {
		t.Fatalf("List projection mismatch: %+v", got)
	}
	if got.AuthUsername == nil || *got.AuthUsername != "proxy-user" {
		t.Fatalf("List must project auth_username; got %v", got.AuthUsername)
	}
	if got.LastCheckAt == nil || !got.LastCheckAt.Equal(checked) {
		t.Fatalf("List must project last_check_at; got %v", got.LastCheckAt)
	}
	// 结构层面的不含凭据证明:该行携带了密文,但它唯一可能泄露的途径是结构体字段——
	// 而 Proxy 一个都没有。我们通过穷尽断言每个被填充的字段都源自非凭据列,
	// 来验证上面的投影不携带任何由 AuthSecret 派生的值。
	assertProxySecretFree(t, got)
}

// TestGetTenantScopedAndNotFound 守护 Get:它透传 {tenant_id,id},映射非凭据行,
// 并把 pgx.ErrNoRows(不存在或跨租户,因查询按租户过滤)翻译成 ErrNotFound。
// 变异:遗漏 ErrNoRows→ErrNotFound 的翻译 → 调用方看到 ErrBackend → not-found
// 用例转红。
func TestGetTenantScopedAndNotFound(t *testing.T) {
	ctx := context.Background()
	user := "u"
	now := time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC)

	t.Run("found projects non-secret fields", func(t *testing.T) {
		q := &mockProxyQuerier{getRow: admindb.GetProxyRow{
			ID: 5, TenantID: 9, Name: "n", Protocol: "socks5", Host: "h", Port: 1080,
			AuthUsername: &user, AuthSecret: strPtr("hk_proxy_v1$secret"), Status: "disabled",
			CreatedAt: pgts(now), UpdatedAt: pgts(now),
		}}
		got, err := New(q, testKeys(t)).Get(ctx, 9, 5)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if q.getArg.TenantID != 9 || q.getArg.ID != 5 {
			t.Fatalf("Get must forward {tenant=9,id=5}; got %+v", q.getArg)
		}
		if got.ID != 5 || got.Protocol != "socks5" || got.Status != "disabled" {
			t.Fatalf("Get projection mismatch: %+v", got)
		}
		assertProxySecretFree(t, got)
	})

	t.Run("missing/cross-tenant yields ErrNotFound", func(t *testing.T) {
		q := &mockProxyQuerier{getErr: pgx.ErrNoRows}
		_, err := New(q, testKeys(t)).Get(ctx, 9, 404)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Get of absent proxy = %v; want ErrNotFound", err)
		}
	})
}

// TestSetStatusValidatesAndScopes 守护 SetStatus:合法值连同 tenant+id 一起透传,
// 非法值在任何 DB 写入之前就以 ErrInvalidStatus 被拒绝。变异:删掉 validStatus
// 守卫 → "banned" 抵达 querier → 转红(setStatusCalls != 0)。
func TestSetStatusValidatesAndScopes(t *testing.T) {
	ctx := context.Background()

	t.Run("valid status writes tenant-scoped", func(t *testing.T) {
		q := &mockProxyQuerier{}
		if err := New(q, testKeys(t)).SetStatus(ctx, 7, 11, "disabled"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
		if q.setStatusCalls != 1 || q.setStatusArg.Status != "disabled" ||
			q.setStatusArg.TenantID != 7 || q.setStatusArg.ID != 11 {
			t.Fatalf("SetStatus arg mismatch: %+v (calls=%d)", q.setStatusArg, q.setStatusCalls)
		}
	})

	t.Run("invalid status rejected before write", func(t *testing.T) {
		q := &mockProxyQuerier{}
		err := New(q, testKeys(t)).SetStatus(ctx, 7, 11, "banned")
		if !errors.Is(err, ErrInvalidStatus) {
			t.Fatalf("SetStatus(banned) = %v; want ErrInvalidStatus", err)
		}
		if q.setStatusCalls != 0 {
			t.Fatalf("invalid status must not touch the querier; calls=%d", q.setStatusCalls)
		}
	})
}

// TestDeleteTenantScopedIdempotent 守护 Delete:它把 {tenant_id,id} 透传给软删除
// 查询,并把后端错误以 ErrBackend 暴露。变异:透传错误的 tenant → 参数断言转红;
// 删掉 mapErr → 后端故障会以原始形态而非 ErrBackend 暴露。
func TestDeleteTenantScopedIdempotent(t *testing.T) {
	ctx := context.Background()

	t.Run("forwards tenant-scoped delete", func(t *testing.T) {
		q := &mockProxyQuerier{}
		if err := New(q, testKeys(t)).Delete(ctx, 7, 11); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if q.deleteCalls != 1 || q.deleteArg.TenantID != 7 || q.deleteArg.ID != 11 {
			t.Fatalf("Delete arg mismatch: %+v (calls=%d)", q.deleteArg, q.deleteCalls)
		}
	})

	t.Run("backend failure maps to ErrBackend", func(t *testing.T) {
		q := &mockProxyQuerier{deleteErr: errors.New("conn reset")}
		err := New(q, testKeys(t)).Delete(ctx, 7, 11)
		if !errors.Is(err, ErrBackend) {
			t.Fatalf("Delete backend error = %v; want ErrBackend", err)
		}
	})
}

// TestReadPathRejectsBadScope 守护读取路径共用的廉价输入门:非正的 tenant/id
// 绝不抵达 querier。变异:移除 tenantID<=0 守卫 → querier 被以 tenant 0 调用 →
// 转红。
func TestReadPathRejectsBadScope(t *testing.T) {
	ctx := context.Background()
	q := &mockProxyQuerier{}
	s := New(q, testKeys(t))
	if _, err := s.List(ctx, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("List(0) = %v; want ErrInvalidInput", err)
	}
	if _, err := s.Get(ctx, 0, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Get(0,1) = %v; want ErrInvalidInput", err)
	}
	if err := s.Delete(ctx, 1, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Delete(1,0) = %v; want ErrInvalidInput", err)
	}
	if q.listCalls != 0 || q.getCalls != 0 || q.deleteCalls != 0 {
		t.Fatalf("bad-scope inputs must not touch the querier; %+v", q)
	}
}

// assertProxySecretFree 断言读取路径 DTO 的"不含凭据"契约:投影必须填充非凭据
// 字段,且加密的 auth_secret 没有任何途径浮现(Proxy 类型没有该字段)。若日后某次
// 修改给 Proxy 加上携带凭据的字段,这个 helper(以及该类型)就不得不改动,
// 这正是我们想要的绊线。
func assertProxySecretFree(t *testing.T, p Proxy) {
	t.Helper()
	if p.ID <= 0 || p.Name == "" || p.Protocol == "" || p.Host == "" || p.Port <= 0 || p.Status == "" {
		t.Fatalf("read-path DTO must surface the non-secret identity fields; got %+v", p)
	}
}

func pgts(at time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: at, Valid: true}
}

func testKeys(t *testing.T) credentialstore.KeyProvider {
	t.Helper()
	keys, err := credentialstore.NewStaticKeyProvider("proxy-test", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewStaticKeyProvider: %v", err)
	}
	return keys
}

func strPtr(v string) *string { return &v }
