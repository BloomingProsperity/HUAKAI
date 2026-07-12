// HUAKAI · iKun

// DriftObserver 消费链测试: Medium(仅 IP 变)/Low(仅 UA 变)此前被算出即丢弃 ——
// 本测试钉死两路 (Validate/Refresh) 都把非 None 漂移交给观察者, 且放行/拒绝行为不变。

package usersession

import (
	"context"
	"errors"
	"testing"
	"time"
)

type driftObserverStub struct {
	events []SessionDriftEvent
}

func (o *driftObserverStub) ObserveSessionDrift(_ context.Context, ev SessionDriftEvent) {
	o.events = append(o.events, ev)
}

// TestValidateObservesMediumAndLowDrift 守 Validate 漂移消费:
//   - 同 IP 同 UA → 零事件 (无谓噪声也是缺陷);
//   - 仅 UA 变 → Low 事件, 放行 (行为不变);
//   - 仅 IP 变 → Medium 事件, 放行;
//   - IP+UA 全变 → High 事件, 仍撤家族拒绝 (撤销行为不变)。
//
// mutation: Validate 里去掉 observeDrift 调用 → Low/Medium/High 断言全红。
func TestValidateObservesMediumAndLowDrift(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 5, 12, 30, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	obs := &driftObserverStub{}
	svc.DriftObserver = obs

	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 50, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// 无漂移: 零事件。
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.9.9", "Chrome/2"); err != nil {
		t.Fatalf("same-class validate: %v", err)
	}
	if len(obs.events) != 0 {
		t.Fatalf("无漂移却发了 %d 条事件 (噪声)", len(obs.events))
	}

	// 仅 UA 变 (chrome→firefox): Low, 放行。
	if _, err := svc.Validate(ctx, issued.SessionToken, "10.1.2.3", "Firefox/9"); err != nil {
		t.Fatalf("low-drift validate 应放行: %v", err)
	}
	if len(obs.events) != 1 || obs.events[0].Level != DriftLow || obs.events[0].Reason != "ua_changed" || obs.events[0].Source != "validate" {
		t.Fatalf("Low 漂移未被消费: %+v (token 盗用弱信号检测全盲)", obs.events)
	}
	if obs.events[0].UserID != 50 || obs.events[0].UAClass != "firefox" || obs.events[0].BaselineUA != "chrome" {
		t.Fatalf("Low 事件字段错: %+v", obs.events[0])
	}

	// 仅 IP 变 (10.1/16→172.16/16): Medium, 放行。
	if _, err := svc.Validate(ctx, issued.SessionToken, "172.16.2.3", "Chrome/1"); err != nil {
		t.Fatalf("medium-drift validate 应放行: %v", err)
	}
	if len(obs.events) != 2 || obs.events[1].Level != DriftMedium || obs.events[1].Reason != "ip_changed" {
		t.Fatalf("Medium 漂移未被消费: %+v", obs.events)
	}

	// IP+UA 全变: High 事件 + 撤家族拒绝 (撤销行为不因观察者存在而变)。
	if _, err := svc.Validate(ctx, issued.SessionToken, "172.16.2.3", "Firefox/9"); !errors.Is(err, ErrAnomalyRejected) {
		t.Fatalf("high-drift validate err=%v, want ErrAnomalyRejected", err)
	}
	if len(obs.events) != 3 || obs.events[2].Level != DriftHigh {
		t.Fatalf("High 漂移未进同一信号流: %+v", obs.events)
	}
	families, _ := svc.List(ctx, 1, 50)
	if len(families) != 1 || families[0].Status != FamilyStatusRevoked {
		t.Fatalf("High 漂移家族未撤: %+v", families)
	}
}

// TestRefreshObservesDrift 守 Refresh 漂移消费: Medium 事件 + 续期照常成功。
// mutation: Refresh 里去掉 observeDrift 调用 → 事件断言红。
func TestRefreshObservesDrift(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 5, 13, 0, 0, 0, time.UTC)
	svc := NewService(NewMemoryStore())
	svc.Now = func() time.Time { return now }
	svc.SigningKey = testSigningKey()
	obs := &driftObserverStub{}
	svc.DriftObserver = obs

	issued, err := svc.Create(ctx, CreateInput{TenantID: 1, UserID: 51, IP: "10.1.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now = now.Add(time.Minute)
	rotated, err := svc.Refresh(ctx, RefreshInput{TenantID: 1, UserID: 51, RefreshToken: issued.RefreshToken, IP: "172.16.2.3", UserAgent: "Chrome/1"})
	if err != nil {
		t.Fatalf("medium-drift refresh 应放行: %v", err)
	}
	if rotated.Generation != 2 {
		t.Fatalf("续期未推进: %+v", rotated.Generation)
	}
	if len(obs.events) != 1 || obs.events[0].Level != DriftMedium || obs.events[0].Source != "refresh" {
		t.Fatalf("Refresh 路漂移未被消费: %+v", obs.events)
	}
}
