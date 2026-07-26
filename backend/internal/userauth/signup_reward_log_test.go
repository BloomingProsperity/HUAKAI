package userauth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestIssueSignupCredits_LogsRewardFailure 证明「注册成功但奖励发放失败」不再被静默吞掉,
// 而是打出带 error_class(signup_reward_failed)+ reward_kind + 租户/用户 ID 的高辨识度告警。
//
// 变异刀:把 issueSignupCredits 失败分支里的 logSignupRewardFailure 调用删掉、退回 `_ =` 静默,
// 该断言立即转红(缓冲区里再无 signup_reward_failed)。
func TestIssueSignupCredits_LogsRewardFailure(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := &Service{
		SignupRewards: SignupRewardConfig{SignupBonusCents: 100},
		SignupBonusFn: func(context.Context, int64, int64) error {
			return errors.New("wallet backend down")
		},
	}
	// 注册主流程绝不因奖励失败而报错/回滚:issueSignupCredits 无返回值,调用不 panic 即可。
	s.issueSignupCredits(context.Background(), 7, 42, false)

	out := buf.String()
	for _, want := range []string{"signup_reward_failed", "signup_bonus", "reward_kind"} {
		if !strings.Contains(out, want) {
			t.Fatalf("奖励失败告警缺字段 %q;实际输出=%s", want, out)
		}
	}
}

// TestIssueSignupCredits_SilentOnSuccess 反向:奖励成功时不应打失败告警(避免噪声污染检索)。
func TestIssueSignupCredits_SilentOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := &Service{
		SignupRewards: SignupRewardConfig{SignupBonusCents: 100},
		SignupBonusFn: func(context.Context, int64, int64) error { return nil },
	}
	s.issueSignupCredits(context.Background(), 7, 42, false)

	if strings.Contains(buf.String(), "signup_reward_failed") {
		t.Fatalf("奖励成功却打了失败告警;输出=%s", buf.String())
	}
}

func TestIssueSignupCredits_EnqueuesOnlyFailedRewards(t *testing.T) {
	var recovered []string
	s := &Service{
		SignupRewards:   SignupRewardConfig{SignupBonusCents: 100, InviteeRewardCents: 50},
		SignupBonusFn:   func(context.Context, int64, int64) error { return errors.New("bonus down") },
		InviteeRewardFn: func(context.Context, int64, int64) error { return nil },
		SignupRewardRecoveryFn: func(_ context.Context, tenantID, userID int64, kind string, amountCents int64) error {
			if tenantID != 7 || userID != 42 {
				t.Fatalf("recovery identity=%d/%d want 7/42", tenantID, userID)
			}
			if amountCents != 100 {
				t.Fatalf("recovery amount=%d want 100", amountCents)
			}
			recovered = append(recovered, kind)
			return nil
		},
	}
	s.issueSignupCredits(context.Background(), 7, 42, true)
	if len(recovered) != 1 || recovered[0] != "signup_bonus" {
		t.Fatalf("recovered=%v want [signup_bonus]", recovered)
	}
}

func TestIssueSignupCredits_RecoverySurvivesRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	s := &Service{
		SignupRewards: SignupRewardConfig{SignupBonusCents: 100},
		SignupBonusFn: func(context.Context, int64, int64) error { return errors.New("bonus down") },
		SignupRewardRecoveryFn: func(recoveryCtx context.Context, tenantID, userID int64, kind string, amountCents int64) error {
			called = true
			if recoveryCtx.Err() != nil {
				t.Fatalf("恢复入队继承了已取消请求: %v", recoveryCtx.Err())
			}
			if tenantID != 7 || userID != 42 || kind != "signup_bonus" || amountCents != 100 {
				t.Fatalf("恢复事件=%d/%d/%s/%d，期望 7/42/signup_bonus/100", tenantID, userID, kind, amountCents)
			}
			return nil
		},
	}

	s.issueSignupCredits(ctx, 7, 42, false)
	if !called {
		t.Fatal("请求取消后未写入奖励恢复事件")
	}
}
