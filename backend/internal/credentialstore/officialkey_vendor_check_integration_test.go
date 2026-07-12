//go:build integration_pg

package credentialstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestOfficialKeyVendorsInVendorModeChecks 守护迁移 0169:vendor-mode CHECK 必须放行
// 官 key 厂商组合与治愈的存量 OAuth 组合,同时继续拒绝 Owner 明确不接的托管云厂商。
//
// 判别性升级(对抗审查抓回的盲区):早期版本只 grep vendor 字符串,对「vendor 在白名单里
// 但缺某个 auth_mode」零判别力(gemini/oauth 正是这样漏掉的)。本版做**组合级真插入 oracle**:
// 对 DefaultHandlerRegistry 的每个 (vendor, auth_mode) 组合执行真 INSERT(喂故意不存在的
// FK id),CHECK 先于 FK 触发器求值——若组合被白名单拒,报 23514 check_violation;
// 若组合放行,报 23503 foreign_key_violation。断言「绝不 23514」即锁死
// 「代码注册的每个组合真库都写得进」的不变量,且随未来新增 handlerSpec 自动扩展。
func TestOfficialKeyVendorsInVendorModeChecks(t *testing.T) {
	ctx, pool := openCredentialAuditTxPool(t)

	insertErrCode := func(vendor, mode string) string {
		t.Helper()
		// 每次独立事务并回滚,不留脏数据;FK id 用 -1 保证 23503 兜底。
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `
			INSERT INTO account_credentials
				(tenant_id, provider_account_id, vendor, auth_mode, encrypted_payload, key_id, nonce, aad_hash)
			VALUES (-1, -1, $1, $2, '\x00'::bytea, 'test-key', '\x00'::bytea, 'test-aad')`,
			vendor, mode)
		if err == nil {
			t.Fatalf("%s/%s: INSERT 不应成功(FK id=-1 必须炸 FK)", vendor, mode)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("%s/%s: 非 pg 错误:%v", vendor, mode, err)
		}
		return pgErr.Code
	}

	// ① 组合级正向:代码注册的全部组合(官 key 12 家 + 存量 OAuth 全部)真库放行。
	for _, key := range DefaultHandlerRegistry().Names() {
		vendor, mode, _ := strings.Cut(key, "/")
		if code := insertErrCode(vendor, mode); code == "23514" {
			t.Fatalf("%s 被 vendor-mode CHECK 拒绝(迁移 0169 缺该组合?)— 代码注册的组合真库必须写得进", key)
		}
	}

	// ② 负向边界锁:托管云厂商(Owner 不接)与胡编 mode 必须被 CHECK 拒(23514)。
	for _, tc := range [][2]string{
		{"openrouter", "api_key"}, {"mistral", "api_key"}, {"groqcloud", "api_key"},
		{"together", "api_key"}, {"perplexity", "api_key"}, {"fireworks", "api_key"},
		{"gemini", "definitely_not_a_real_mode"}, {"grok", "kimi_oauth"},
	} {
		if code := insertErrCode(tc[0], tc[1]); code != "23514" {
			t.Fatalf("%s/%s 应被 CHECK 拒(23514),得 %s — 白名单被意外放宽", tc[0], tc[1], code)
		}
	}

	// ③ 镜像不变量:采集流表(credential_acquisition_flow_sessions)的 CHECK 体与
	// account_credentials 逐字相同(0016/0019/0143/0169 一贯镜像)。①②已在 account_credentials
	// 上做了组合级验证,此断言把同等保证传递到采集流表——两表漂移即红。
	var acctDef, acqDef string
	if err := pool.QueryRow(ctx,
		"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'account_credentials_vendor_mode_check'",
	).Scan(&acctDef); err != nil {
		t.Fatalf("load acct constraint def: %v", err)
	}
	if err := pool.QueryRow(ctx,
		"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'credential_acq_vendor_mode_check'",
	).Scan(&acqDef); err != nil {
		t.Fatalf("load acq constraint def: %v", err)
	}
	if acctDef != acqDef {
		t.Fatalf("两表 vendor-mode CHECK 漂移(应逐字镜像):\nacct=%s\nacq=%s", acctDef, acqDef)
	}
}
