package admin

import "testing"

// AuditActor 必须按来源产出可区分的归属串,绝不因 session-admin 的 TokenID=0
// 而把人的操作误记成 token 0(P1 审计误归隐患的正解)。
func TestAuditActorBySource(t *testing.T) {
	cases := []struct {
		name string
		id   AdminIdentity
		want string
	}{
		// token 源:走 TokenID。变异:若 AuditActor 忽略 Source 恒用 UserID → 得 "admin_user:0" → RED。
		{"token", AdminIdentity{Source: AdminSourceToken, TokenID: 7, UserID: 0}, "admin_token:7"},
		// 空源兼容既有令牌通道:视同 token。变异:若空源被当 session → "admin_user:0" → RED。
		{"empty-source-legacy", AdminIdentity{TokenID: 42}, "admin_token:42"},
		// session 源:走 UserID(TokenID 恒 0)。变异:若 AuditActor 恒用 TokenID → "admin_token:0" → RED。
		{"session", AdminIdentity{Source: AdminSourceSession, UserID: 9, TokenID: 0}, "admin_user:9"},
	}
	for _, tc := range cases {
		if got := tc.id.AuditActor(); got != tc.want {
			t.Fatalf("%s: AuditActor()=%q want %q", tc.name, got, tc.want)
		}
	}
}

// 判别性:同一底层 id 值(7),token 源与 session 源必须产出不同归属串——
// 证明 Source 真的参与判别,而非碰巧同值。
func TestAuditActorSourceIsDiscriminating(t *testing.T) {
	tokenActor := AdminIdentity{Source: AdminSourceToken, TokenID: 7}.AuditActor()
	sessionActor := AdminIdentity{Source: AdminSourceSession, UserID: 7}.AuditActor()
	if tokenActor == sessionActor {
		t.Fatalf("token 源与 session 源同值 id 归属串不应相同,均得 %q(Source 未参与判别)", tokenActor)
	}
}

// 判别性:session 源即便 UserID=0(未填/异常)也必须走 session 前缀,
// 绝不因数值为 0 而回退成 "admin_token:0" 把人的会话误记成 token 0。
// 变异:若 AuditActor 的 session 判定改成 `Source==session && UserID!=0`
// 这类"值非零才用 UserID"的错误短路 → 本例得 "admin_token:0" → RED。
func TestAuditActorSessionZeroUserStillSessionPrefix(t *testing.T) {
	got := AdminIdentity{Source: AdminSourceSession, UserID: 0}.AuditActor()
	if got != "admin_user:0" {
		t.Fatalf("session 源 UserID=0 AuditActor()=%q,want %q(不得回退成 token 形态)", got, "admin_user:0")
	}
}

// 判别性:大小写敏感——Source 常量是精确串 "session";任何非精确匹配
// (如 "Session"/"SESSION")按既有兼容语义退化为 token 源,绝不误判成 session。
// 守 AuditActor 里 `i.Source == AdminSourceSession` 用的是精确等值而非
// 大小写不敏感/前缀匹配。
func TestAuditActorSourceIsCaseSensitiveExact(t *testing.T) {
	for _, bad := range []string{"Session", "SESSION", "session ", " session", "token"} {
		got := AdminIdentity{Source: bad, TokenID: 5, UserID: 9}.AuditActor()
		if got != "admin_token:5" {
			t.Fatalf("Source=%q AuditActor()=%q,want admin_token:5(非精确 session 串须退化为 token 源)", bad, got)
		}
	}
}
