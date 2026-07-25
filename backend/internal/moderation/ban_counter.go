package moderation

import "context"

type BanStore interface {
	RecordModerationViolationEvent(context.Context, ModerationEvent) error
	CountBlocksInWindow(context.Context, int64, int64, int32) (int64, error)
	DisableAPIKey(context.Context, int64, int64) error
}

type BanResult struct {
	// Disabled 表示本次确实停用了 Key。
	Disabled bool
	// ThresholdReached 表示窗口内违规已达阈值。它与 Disabled 分开：开关关闭时
	// 达阈值但不停用，运营需要凭这个事实找出待处置的 Key。
	ThresholdReached bool
	Count            int64
}

type DBBanCounter struct {
	store BanStore
}

func NewBanCounter(store BanStore) *DBBanCounter {
	return &DBBanCounter{store: store}
}

func (c *DBBanCounter) RecordAndCheck(ctx context.Context, event ModerationEvent, cfg ModerationConfig) (BanResult, error) {
	if c == nil || c.store == nil || cfg.BanThreshold <= 0 {
		return BanResult{}, nil
	}
	if event.Decision != DecisionBlockKeyword && event.Decision != DecisionBlockHash && event.Decision != DecisionBlockExternal {
		return BanResult{}, nil
	}
	if err := c.store.RecordModerationViolationEvent(ctx, event); err != nil {
		return BanResult{}, err
	}
	count, err := c.store.CountBlocksInWindow(ctx, event.TenantID, event.APIKeyID, cfg.BanWindowSeconds)
	if err != nil {
		return BanResult{}, err
	}
	result := BanResult{Count: count}
	if count < int64(cfg.BanThreshold) {
		return result, nil
	}
	result.ThresholdReached = true
	// 达阈值只是事实，处置是策略。开关关闭时到此为止：违规事件、计数与达标事实
	// 均已落库，运营据此人工判断；此时 Key 保持可用，因为触发拦截的那次请求本身
	// 已经被拒绝，继续留着 Key 不会让违规内容流向上游。
	if !cfg.AutoDisableKeyOnBan {
		return result, nil
	}
	if err := c.store.DisableAPIKey(ctx, event.TenantID, event.APIKeyID); err != nil {
		return BanResult{}, err
	}
	result.Disabled = true
	return result, nil
}
