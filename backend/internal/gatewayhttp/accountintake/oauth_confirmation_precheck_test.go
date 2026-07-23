package accountintake

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/intake"
)

// TestPrecheckOAuthConfirmations 判别性守卫:create/update 项缺必需确认时,预检必须
// blocked=true 并返回 confirmation_required(带缺失项);确认补齐或非 create/update 动作
// 时 blocked=false。此预检在破坏性 Claim 之前拦截,保证缺确认不消费 staged token。
func TestPrecheckOAuthConfirmations(t *testing.T) {
	planWith := func(action intake.Action, required ...string) intake.Plan {
		return intake.Plan{Items: []intake.Item{{Index: 0, Action: action, RequiredConfirmations: required}}}
	}

	t.Run("create 缺确认→blocked", func(t *testing.T) {
		res, blocked := precheckOAuthConfirmations(planWith(intake.ActionCreate, "confirm_weak_identity"), nil)
		if !blocked {
			t.Fatal("缺确认应 blocked=true")
		}
		if len(res.Items) != 1 || res.Items[0].Status != StatusConflict || res.Items[0].Code != "confirmation_required" {
			t.Fatalf("冲突结果不符:%+v", res.Items)
		}
		found := false
		for _, w := range res.Items[0].Warnings {
			if w == "confirm_weak_identity" {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺失确认项未回报:%v", res.Items[0].Warnings)
		}
	})

	t.Run("create 确认已补齐→放行", func(t *testing.T) {
		_, blocked := precheckOAuthConfirmations(planWith(intake.ActionCreate, "confirm_weak_identity"), []string{"confirm_weak_identity"})
		if blocked {
			t.Fatal("确认补齐应 blocked=false")
		}
	})

	t.Run("create 无必需确认→放行", func(t *testing.T) {
		_, blocked := precheckOAuthConfirmations(planWith(intake.ActionCreate), nil)
		if blocked {
			t.Fatal("无必需确认应 blocked=false")
		}
	})

	t.Run("skip 动作不校验确认→放行", func(t *testing.T) {
		_, blocked := precheckOAuthConfirmations(planWith(intake.ActionSkip, "confirm_weak_identity"), nil)
		if blocked {
			t.Fatal("非 create/update 动作不应因确认被拦")
		}
	})
}
