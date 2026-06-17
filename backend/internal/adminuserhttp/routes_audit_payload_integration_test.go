// HUAKAI · iKun
//go:build integration_pg

package adminuserhttp

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// 自证:admin_audit_events.payload 是 NOT NULL。漏给 payload(旧 force_disable_2fa / reset_passkey /
// set_user_remark handler 的行为)→ sqlc 显式插该列写成 NULL → 违约 → 审计 insert 必失败 → handler
// 每次返 503(动作已生效却报失败)。补非空 payload 后成功。两路对照证明这是真 bug + 真修(非假绿)。
//
// 变异判别:把 case(2)的 payload 也改成 nil → case(2)转红(说明断言确实依赖非空 payload)。
func TestAdminAuditPayload_NullFailsNonNullSucceeds(t *testing.T) {
	ctx := context.Background()
	pool := openAdminUsersPool(t, ctx)
	f := newAdminUsersFixture(t, ctx, pool)
	userID := f.seedUser("audit-pl", "active", "user", "0.00000000")
	q := admindb.New(pool)

	mk := func(payload []byte) admindb.InsertAdminAuditEventParams {
		tid := f.tenantID
		uid := userID
		return admindb.InsertAdminAuditEventParams{
			TenantID: &tid, ActorID: "tok", ActorRole: admin.RoleTenantOperator,
			Action: "force_disable_2fa", TargetType: "user", TargetID: &uid, Payload: payload,
		}
	}

	// (1) nil payload(旧行为)→ 期望 NOT NULL 违约
	if _, err := q.InsertAdminAuditEvent(ctx, mk(nil)); err == nil {
		t.Fatal("nil payload 应触 payload NOT NULL 违约,却成功了 —— 测试前提(列约束)已变,需复核")
	} else {
		le := strings.ToLower(err.Error())
		if !strings.Contains(le, "payload") && !strings.Contains(le, "null value") && !strings.Contains(le, "not-null") {
			t.Fatalf("期望 payload NOT NULL 违约,实得其它错误: %v", err)
		}
	}

	// (2) 非空 payload(修复后 handler 的行为)→ 期望成功
	if _, err := q.InsertAdminAuditEvent(ctx, mk([]byte(`{"forced":true}`))); err != nil {
		t.Fatalf("非空 payload 应成功,实得: %v", err)
	}
}
