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
