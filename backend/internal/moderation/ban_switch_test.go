package moderation

import (
	"context"
	"testing"
)

// AT-01：开关关闭时达到阈值，只落库不停用。这是「必须人工确认」的核心保证：
// 违规事实、计数、达标标记都要在，唯独不动 Key。
// 变异：把 ban_counter 里的开关判断删掉时转红。
func TestBanCounter_开关关闭时达阈值只记录不停用(t *testing.T) {
	store := &banStoreStub{count: 3} // 桩返回的是记录本次之后的窗口计数
	counter := NewBanCounter(store)

	res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7,
		APIKeyID: 11,
		Decision: DecisionBlockKeyword,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: false})
	if err != nil {
		t.Fatalf("RecordAndCheck 返回错误: %v", err)
	}

	if res.Disabled {
		t.Fatalf("Disabled=true，开关关闭时不应停用 Key")
	}
	if store.disableCalls != 0 {
		t.Fatalf("disableCalls=%d want 0，开关关闭时不应触达停用语句", store.disableCalls)
	}
	// 达标事实必须仍然可见，否则运营找不出待处置的 Key。
	if !res.ThresholdReached {
		t.Fatalf("ThresholdReached=false want true，达标事实丢失运营无从人工处置")
	}
	if res.Count != 3 {
		t.Fatalf("Count=%d want 3，计数必须照常累计", res.Count)
	}
	// 违规事件必须照常落库：这是「只记录不停用」里「记录」的那一半。
	if store.recordCalls != 1 {
		t.Fatalf("recordCalls=%d want 1，违规事件未落库", store.recordCalls)
	}
}

// AT-02：开关开启时达到阈值，恢复既有自动停用行为。
// 变异：把停用调用摘掉时转红。
func TestBanCounter_开关开启时达阈值仍自动停用(t *testing.T) {
	store := &banStoreStub{count: 3}
	counter := NewBanCounter(store)

	res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID: 7,
		APIKeyID: 11,
		Decision: DecisionBlockKeyword,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: true})
	if err != nil {
		t.Fatalf("RecordAndCheck 返回错误: %v", err)
	}

	if !res.Disabled {
		t.Fatalf("Disabled=false want true")
	}
	if !res.ThresholdReached {
		t.Fatalf("ThresholdReached=false want true")
	}
	if store.disabledTenantID != 7 || store.disabledAPIKeyID != 11 {
		t.Fatalf("停用目标错误: tenant=%d api_key=%d", store.disabledTenantID, store.disabledAPIKeyID)
	}
}

// AT-03：未达阈值时两种开关状态都不停用，且都不误报达标。
func TestBanCounter_未达阈值两种开关都不停用(t *testing.T) {
	for _, autoDisable := range []bool{false, true} {
		t.Run(map[bool]string{false: "开关关闭", true: "开关开启"}[autoDisable], func(t *testing.T) {
			store := &banStoreStub{count: 2} // 低于阈值 3
			counter := NewBanCounter(store)

			res, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
				TenantID: 7,
				APIKeyID: 11,
				Decision: DecisionBlockKeyword,
			}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: autoDisable})
			if err != nil {
				t.Fatalf("RecordAndCheck 返回错误: %v", err)
			}
			if res.Disabled {
				t.Fatalf("Disabled=true，未达阈值不应停用")
			}
			if res.ThresholdReached {
				t.Fatalf("ThresholdReached=true，未达阈值不应报达标")
			}
			if store.disableCalls != 0 {
				t.Fatalf("disableCalls=%d want 0", store.disableCalls)
			}
		})
	}
}

// 开关关闭不得被误当成「关掉整条计数链」：这正是不能复用 ban_threshold=0
// 当开关的原因——阈值为 0 会让计数链在记录违规之前提前返回。
// 变异：把开关判断挪到记录违规之前时转红。
func TestBanCounter_开关关闭仍保留完整违规审计链(t *testing.T) {
	store := &banStoreStub{countFromRecorded: true}
	counter := NewBanCounter(store)
	cfg := ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300, AutoDisableKeyOnBan: false}

	for i := 0; i < 5; i++ {
		if _, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
			TenantID: 7,
			APIKeyID: 11,
			Decision: DecisionBlockKeyword,
		}, cfg); err != nil {
			t.Fatalf("第 %d 次 RecordAndCheck 返回错误: %v", i+1, err)
		}
	}

	if store.recordCalls != 5 {
		t.Fatalf("recordCalls=%d want 5，开关关闭时违规记录不得缺失", store.recordCalls)
	}
	if store.disableCalls != 0 {
		t.Fatalf("disableCalls=%d want 0", store.disableCalls)
	}
}

// 摘录必须随违规事件一起落库，否则运营在管理端看不到内容。
// 变异：把 sql_store 里 InputExcerpt 的传参删掉时，此断言在集成层转红；
// 本用例在单元层锁住 event 到 store 的传递不丢字段。
func TestBanCounter_违规事件携带摘录落库(t *testing.T) {
	store := &banStoreStub{count: 0}
	counter := NewBanCounter(store)

	const excerpt = "帮我写一段关于水质检测的说明"
	if _, err := counter.RecordAndCheck(context.Background(), ModerationEvent{
		TenantID:     7,
		APIKeyID:     11,
		Decision:     DecisionBlockKeyword,
		InputExcerpt: excerpt,
	}, ModerationConfig{BanThreshold: 3, BanWindowSeconds: 300}); err != nil {
		t.Fatalf("RecordAndCheck 返回错误: %v", err)
	}

	if store.lastEvent.InputExcerpt != excerpt {
		t.Fatalf("落库摘录=%q want %q", store.lastEvent.InputExcerpt, excerpt)
	}
}
