package proxyadmin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type tenantDefaultRowStub struct {
	value any
	err   error
}

type tenantDefaultReaderStub struct {
	row  pgx.Row
	sql  string
	args []any
}

func (s *tenantDefaultReaderStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.sql = sql
	s.args = append([]any(nil), args...)
	return s.row
}

func (r tenantDefaultRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("测试行只支持单列")
	}
	switch out := dest[0].(type) {
	case **int64:
		if r.value == nil {
			*out = nil
			return nil
		}
		v := r.value.(int64)
		*out = &v
	case *int64:
		*out = r.value.(int64)
	default:
		return errors.New("测试行收到未知目标类型")
	}
	return nil
}

type tenantDefaultExecCall struct {
	sql  string
	args []any
}

type tenantDefaultTxStub struct {
	beforeProxyID *int64
	proxyExists   bool
	failAudit     bool
	queries       []string
	queryArgs     [][]any
	execs         []tenantDefaultExecCall
	committed     bool
	rolledBack    bool
}

func (s *tenantDefaultTxStub) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	s.queries = append(s.queries, sql)
	s.queryArgs = append(s.queryArgs, append([]any(nil), args...))
	if strings.Contains(sql, "FROM tenants") {
		if s.beforeProxyID == nil {
			return tenantDefaultRowStub{}
		}
		return tenantDefaultRowStub{value: *s.beforeProxyID}
	}
	if strings.Contains(sql, "FROM proxies") {
		if !s.proxyExists {
			return tenantDefaultRowStub{err: pgx.ErrNoRows}
		}
		return tenantDefaultRowStub{value: int64(1)}
	}
	return tenantDefaultRowStub{err: errors.New("测试收到未知查询")}
}

func (s *tenantDefaultTxStub) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	s.execs = append(s.execs, tenantDefaultExecCall{sql: sql, args: append([]any(nil), args...)})
	if strings.Contains(sql, "admin_audit_events") && s.failAudit {
		return pgconn.CommandTag{}, errors.New("审计写入失败")
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (s *tenantDefaultTxStub) Commit(context.Context) error {
	s.committed = true
	return nil
}

func (s *tenantDefaultTxStub) Rollback(context.Context) error {
	s.rolledBack = true
	return nil
}

type tenantDefaultBeginnerStub struct{ tx tenantDefaultTx }

func (s tenantDefaultBeginnerStub) BeginTx(context.Context, pgx.TxOptions) (tenantDefaultTx, error) {
	return s.tx, nil
}

// TestTenantDefaultGetReadsNullableColumn 钉住真实读口：只按 path 租户读取现有列，
// 并把 SQL NULL 保留为 nil。把 GET 写死或查错租户都会使精确断言转红。
func TestTenantDefaultGetReadsNullableColumn(t *testing.T) {
	t.Run("已设置", func(t *testing.T) {
		reader := &tenantDefaultReaderStub{row: tenantDefaultRowStub{value: int64(41)}}
		got, err := (&PostgresTenantDefaultStore{reader: reader}).Get(context.Background(), 7)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ProxyID == nil || *got.ProxyID != 41 {
			t.Fatalf("proxy_id=%v want 41", got.ProxyID)
		}
		if !strings.Contains(reader.sql, "SELECT default_proxy_id FROM tenants WHERE id = $1") ||
			len(reader.args) != 1 || reader.args[0] != int64(7) {
			t.Fatalf("读取 SQL/参数漂移: sql=%q args=%v", reader.sql, reader.args)
		}
	})

	t.Run("未设置", func(t *testing.T) {
		reader := &tenantDefaultReaderStub{row: tenantDefaultRowStub{}}
		got, err := (&PostgresTenantDefaultStore{reader: reader}).Get(context.Background(), 7)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ProxyID != nil {
			t.Fatalf("proxy_id=%v want nil", got.ProxyID)
		}
	})
}

// TestTenantDefaultSetWritesValidatedColumnAndAudit 钉住写口的三件事：代理查询同时含
// tenant_id/deleted_at 守卫、真实 UPDATE default_proxy_id、同事务 admin 审计。
// 变异：删任一守卫、漏 UPDATE、漏审计或不提交，都会使精确断言转红。
func TestTenantDefaultSetWritesValidatedColumnAndAudit(t *testing.T) {
	before := int64(9)
	tx := &tenantDefaultTxStub{beforeProxyID: &before, proxyExists: true}
	store := &PostgresTenantDefaultStore{beginner: tenantDefaultBeginnerStub{tx: tx}}
	proxyID := int64(41)

	got, err := store.Set(context.Background(), 7, &proxyID, TenantDefaultAudit{
		ActorID: "admin_user:3", ActorRole: "tenant_operator", RequestID: "req-default-1",
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got.ProxyID == nil || *got.ProxyID != proxyID {
		t.Fatalf("返回 proxy_id=%v want %d", got.ProxyID, proxyID)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("成功事务状态 committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
	if len(tx.queries) != 2 {
		t.Fatalf("查询次数=%d want 2(租户锁+代理校验)", len(tx.queries))
	}
	proxyQuery := tx.queries[1]
	for _, required := range []string{"tenant_id = $2", "deleted_at IS NULL", "FOR UPDATE"} {
		if !strings.Contains(proxyQuery, required) {
			t.Fatalf("代理校验 SQL 缺 %q: %s", required, proxyQuery)
		}
	}
	if args := tx.queryArgs[1]; len(args) != 2 || args[0] != proxyID || args[1] != int64(7) {
		t.Fatalf("代理校验参数=%v want [41 7]", args)
	}
	if len(tx.execs) != 2 {
		t.Fatalf("写调用数=%d want 2(列更新+审计)", len(tx.execs))
	}
	update := tx.execs[0]
	if !strings.Contains(update.sql, "UPDATE tenants") || !strings.Contains(update.sql, "SET default_proxy_id = $2") {
		t.Fatalf("首个写不是 default_proxy_id 更新: %s", update.sql)
	}
	if len(update.args) != 2 || update.args[0] != int64(7) || update.args[1] != proxyID {
		t.Fatalf("更新参数=%v want [7 41]", update.args)
	}
	audit := tx.execs[1]
	if !strings.Contains(audit.sql, "admin_audit_events") || len(audit.args) != 8 {
		t.Fatalf("审计写错误: sql=%s args=%v", audit.sql, audit.args)
	}
	if audit.args[0] != int64(7) || audit.args[1] != "admin_user:3" || audit.args[2] != "tenant_operator" ||
		audit.args[3] != tenantDefaultAuditAction || audit.args[4] != tenantDefaultAuditTarget || audit.args[5] != int64(7) ||
		audit.args[6] != "req-default-1" {
		t.Fatalf("审计字段漂移: %v", audit.args[:7])
	}
	var payload map[string]any
	if err := json.Unmarshal(audit.args[7].([]byte), &payload); err != nil {
		t.Fatalf("解析审计 payload: %v", err)
	}
	if payload["setting"] != "default_proxy_id" || payload["before_proxy_id"] != float64(9) || payload["after_proxy_id"] != float64(41) {
		t.Fatalf("审计 payload=%v", payload)
	}
}

// TestTenantDefaultSetRejectsMissingScopedProxy 证明跨租户与软删都在 UPDATE 前被同一个
// tenant-scoped 查询折叠成 ErrNotFound。变异：删除代理校验调用会继续 UPDATE，测试转红。
func TestTenantDefaultSetRejectsMissingScopedProxy(t *testing.T) {
	tx := &tenantDefaultTxStub{proxyExists: false}
	store := &PostgresTenantDefaultStore{beginner: tenantDefaultBeginnerStub{tx: tx}}
	proxyID := int64(88)
	_, err := store.Set(context.Background(), 7, &proxyID, TenantDefaultAudit{ActorID: "admin_token:1", ActorRole: "tenant_operator"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("错误=%v want ErrNotFound", err)
	}
	if len(tx.execs) != 0 || tx.committed || !tx.rolledBack {
		t.Fatalf("拒绝后仍写入/提交: execs=%d committed=%v rolledBack=%v", len(tx.execs), tx.committed, tx.rolledBack)
	}
}

// TestTenantDefaultSetNullClearsWithoutProxyLookup 钉住 null 清除语义：不查代理、给列写
// SQL NULL、审计 after_proxy_id=null。把 nil 变成 0 会被参数和 payload 精确断言抓住。
func TestTenantDefaultSetNullClearsWithoutProxyLookup(t *testing.T) {
	before := int64(9)
	tx := &tenantDefaultTxStub{beforeProxyID: &before}
	store := &PostgresTenantDefaultStore{beginner: tenantDefaultBeginnerStub{tx: tx}}

	got, err := store.Set(context.Background(), 7, nil, TenantDefaultAudit{ActorID: "admin_token:1", ActorRole: "tenant_operator"})
	if err != nil {
		t.Fatalf("Set clear: %v", err)
	}
	if got.ProxyID != nil {
		t.Fatalf("清除返回 proxy_id=%v want nil", got.ProxyID)
	}
	if len(tx.queries) != 1 || strings.Contains(tx.queries[0], "FROM proxies") {
		t.Fatalf("清除不应查询代理: %v", tx.queries)
	}
	if len(tx.execs) != 2 || tx.execs[0].args[1] != nil {
		t.Fatalf("清除 UPDATE 参数=%v want 第二项 nil", tx.execs)
	}
	var payload map[string]any
	if err := json.Unmarshal(tx.execs[1].args[7].([]byte), &payload); err != nil {
		t.Fatal(err)
	}
	if value, exists := payload["after_proxy_id"]; !exists || value != nil {
		t.Fatalf("after_proxy_id 必须显式 null: exists=%v value=%v", exists, value)
	}
}

// TestTenantDefaultAuditFailureRollsBackColumnUpdate 防止配置先成功、审计后失败形成半提交。
func TestTenantDefaultAuditFailureRollsBackColumnUpdate(t *testing.T) {
	tx := &tenantDefaultTxStub{proxyExists: true, failAudit: true}
	store := &PostgresTenantDefaultStore{beginner: tenantDefaultBeginnerStub{tx: tx}}
	proxyID := int64(41)
	_, err := store.Set(context.Background(), 7, &proxyID, TenantDefaultAudit{ActorID: "admin_token:1", ActorRole: "tenant_operator"})
	if !errors.Is(err, ErrBackend) {
		t.Fatalf("错误=%v want ErrBackend", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("审计失败必须回滚: committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}
