package hermeshttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
)

// TestMutatingAuditActionMapsEveryMutatingTool 钉死:每个 mutating 工具都映射出非空审计 action,且
// alert_rule_enable/disable 映射到迁移 0160 准入的 hermes.tool.alert_rule_*。
// 这是 confirm 执行的命门:漏映射 → confirm 用空 action 构造审计行 → 违反 admin_audit_events.action CHECK →
// 整个 mutation 事务回滚 → 该工具的真正执行 100% 失败(工具完全不可用)。spec 层 Resolve/Mutate 测试覆盖不到
// 这一层,故单列此判别性测试。
// 判别(§14):删掉 mutatingAuditAction 里任一 case → 对应工具的断言转红。
func TestMutatingAuditActionMapsEveryMutatingTool(t *testing.T) {
	cases := map[string]string{
		hermesops.ToolDLQReplay:        "hermes.tool.dlq_replay",
		hermesops.ToolAccountPause:     "hermes.tool.account_pause",
		hermesops.ToolAccountResume:    "hermes.tool.account_resume",
		hermesops.ToolRenewTrigger:     "hermes.tool.renew_trigger",
		hermesops.ToolAlertRuleEnable:  "hermes.tool.alert_rule_enable",
		hermesops.ToolAlertRuleDisable: "hermes.tool.alert_rule_disable",
	}
	for tool, want := range cases {
		if got := mutatingAuditAction(tool); got != want {
			t.Fatalf("mutatingAuditAction(%q)=%q want %q(漏映射→confirm 执行写空 action 违反 CHECK 回滚)", tool, got, want)
		}
	}
	// 未知工具返回空串(fail-closed:不为意料外的工具凭空造审计 action)。
	if got := mutatingAuditAction("alert_rule_enable_typo"); got != "" {
		t.Fatalf("未知工具应返回空串(fail-closed),got %q", got)
	}
}
