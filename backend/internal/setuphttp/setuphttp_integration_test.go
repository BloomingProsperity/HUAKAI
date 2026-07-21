//go:build integration_pg

package setuphttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const setupTestToken = "setup-test-token-0123456789-abcdef"

// 共享测试库上不能包事务(install 自管事务),用"每测一个全新租户"做隔离:
// 新租户天然无 admin → needs_setup=true;测毕删该租户的用户与租户行。
func openSetupPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL not set; skipping integration_pg")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newIsolatedTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx,
		`INSERT INTO tenants (name, status) VALUES ($1, 'active') RETURNING id`,
		fmt.Sprintf("setuphttp-test-%s", t.Name())).Scan(&id)
	if err != nil {
		t.Fatalf("建隔离租户: %v", err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE tenant_id = $1`, id)
		_, _ = pool.Exec(cctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

func newSetupServer(pool *pgxpool.Pool, tenantID int64) *httptest.Server {
	r := chi.NewRouter()
	Mount(r, Deps{Pool: pool, TenantID: tenantID, SetupToken: setupTestToken})
	return httptest.NewServer(r)
}

func getStatus(t *testing.T, srv *httptest.Server) bool {
	t.Helper()
	resp, err := http.Get(srv.URL + "/setup/status")
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status 应 200,得 %d", resp.StatusCode)
	}
	var body struct {
		NeedsSetup bool `json:"needs_setup"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.NeedsSetup
}

func postInstall(t *testing.T, srv *httptest.Server, payload string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/setup/install", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("build POST install: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SetupTokenHeader, setupTestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST install: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// 主流程:空租户 needs_setup=true → install 建 admin(角色/验证态/激活态全对)→
// needs_setup=false → 再 install 一律 403(fail-closed 关死)。
// 变异:锁内重查删掉 / role 写成 user / guard 恒放行 → 对应断言红。
func TestSetupInstallLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := openSetupPool(t, ctx)
	tenant := newIsolatedTenant(t, ctx, pool)
	srv := newSetupServer(pool, tenant)
	defer srv.Close()

	if !getStatus(t, srv) {
		t.Fatal("空租户应 needs_setup=true")
	}

	// 大写输入必须小写归一化存储(变异:去掉 ToLower → 下方按小写查库红)。
	resp := postInstall(t, srv, `{"email":"Boss@Example.com","password":"Str0ngPass!","display_name":"老板"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("install 应 201,得 %d", resp.StatusCode)
	}

	var role, status string
	var verified bool
	err := pool.QueryRow(ctx, `
SELECT role, status, email_verified FROM users
WHERE tenant_id = $1 AND email = 'boss@example.com' AND deleted_at IS NULL`,
		tenant).Scan(&role, &status, &verified)
	if err != nil {
		t.Fatalf("查建出的用户: %v", err)
	}
	if role != "admin" || status != "active" || !verified {
		t.Fatalf("建出的账号应 admin/active/verified,得 %s/%s/%v", role, status, verified)
	}

	if getStatus(t, srv) {
		t.Fatal("装完应 needs_setup=false")
	}
	resp = postInstall(t, srv, `{"email":"second@example.com","password":"Str0ngPass!"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("已装再 install 应 403,得 %d", resp.StatusCode)
	}
	// 错误信封必须是网关统一形态 {error:{code}}(变异:退回扁平串 → 前端只能拿到 http_403 → 红)。
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil || envelope.Error.Code != "already_installed" {
		t.Fatalf("403 信封应含 error.code=already_installed,得 %+v err=%v", envelope, err)
	}
}

// 并发双装:同租户两请求赛跑,恰好一个 201、一个 403,且只建出一个 admin
// (变异:去掉 advisory lock 或锁内重查 → 可能双 201/双 admin → 红)。
func TestSetupInstallConcurrentSingleWinner(t *testing.T) {
	ctx := context.Background()
	pool := openSetupPool(t, ctx)
	tenant := newIsolatedTenant(t, ctx, pool)
	srv := newSetupServer(pool, tenant)
	defer srv.Close()

	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"email":"race%d@example.com","password":"Str0ngPass!"}`, i)
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/setup/install", strings.NewReader(payload))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set(SetupTokenHeader, setupTestToken)
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
					codes[i] = resp.StatusCode
					return
				}
			}
			if err != nil {
				codes[i] = -1
				return
			}
		}(i)
	}
	wg.Wait()

	created := 0
	for _, c := range codes {
		if c == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("并发双装应恰一个 201,得 %v", codes)
	}
	var adminCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM users WHERE tenant_id = $1 AND role = 'admin'`, tenant).Scan(&adminCount); err != nil {
		t.Fatalf("数 admin: %v", err)
	}
	if adminCount != 1 {
		t.Fatalf("应只建出 1 个 admin,得 %d", adminCount)
	}
}

// 入参校验:弱口令/坏邮箱 400 且不产生任何用户;邮箱撞已有用户 409 不静默提升。
func TestSetupInstallValidationAndConflict(t *testing.T) {
	ctx := context.Background()
	pool := openSetupPool(t, ctx)
	tenant := newIsolatedTenant(t, ctx, pool)
	srv := newSetupServer(pool, tenant)
	defer srv.Close()

	for name, payload := range map[string]string{
		"弱口令":    `{"email":"a@b.co","password":"short"}`,
		"坏邮箱":    `{"email":"not-an-email","password":"Str0ngPass!"}`,
		"空邮箱":    `{"email":"","password":"Str0ngPass!"}`,
		"带显示名邮箱": `{"email":"Boss <boss@example.com>","password":"Str0ngPass!"}`,
		"坏JSON":  `{`,
	} {
		if resp := postInstall(t, srv, payload); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s 应 400,得 %d", name, resp.StatusCode)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE tenant_id = $1`, tenant).Scan(&n); err != nil || n != 0 {
		t.Fatalf("校验失败不应建用户,count=%d err=%v", n, err)
	}

	// 预置一个同邮箱普通用户 → install 撞唯一约束应 409,且该用户不被提升。
	if _, err := pool.Exec(ctx, `
INSERT INTO users (tenant_id, email, display_name, status) VALUES ($1, 'taken@example.com', 'x', 'active')`,
		tenant); err != nil {
		t.Fatalf("预置用户: %v", err)
	}
	if resp := postInstall(t, srv, `{"email":"taken@example.com","password":"Str0ngPass!"}`); resp.StatusCode != http.StatusConflict {
		t.Fatalf("邮箱冲突应 409,得 %d", resp.StatusCode)
	}
	var role string
	if err := pool.QueryRow(ctx,
		`SELECT role FROM users WHERE tenant_id = $1 AND email = 'taken@example.com'`, tenant).Scan(&role); err != nil {
		t.Fatalf("查角色: %v", err)
	}
	if role == "admin" {
		t.Fatal("冲突路径不得静默提升既有用户为 admin")
	}
}
