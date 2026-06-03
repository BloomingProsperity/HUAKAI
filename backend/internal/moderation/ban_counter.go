package moderation

import "context"

type BanStore interface {
	CountBlocksInWindow(context.Context, int64, int64, int32) (int64, error)
	DisableAPIKey(context.Context, int64, int64) error
}

type BanResult struct {
	Disabled bool
	Count    int64
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
	if event.Decision != DecisionBlockKeyword && event.Decision != DecisionBlockHash {
		return BanResult{}, nil
	}
	count, err := c.store.CountBlocksInWindow(ctx, event.TenantID, event.APIKeyID, cfg.BanWindowSeconds)
	if err != nil {
		return BanResult{}, err
	}
	result := BanResult{Count: count}
	if count < int64(cfg.BanThreshold) {
		return result, nil
	}
	if err := c.store.DisableAPIKey(ctx, event.TenantID, event.APIKeyID); err != nil {
		return BanResult{}, err
	}
	result.Disabled = true
	return result, nil
}
