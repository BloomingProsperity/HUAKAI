package moderation

import "context"

type BanStore interface {
	RecordModerationViolation(context.Context, ModerationEvent, ModerationConfig) (BanResult, error)
}

type BanResult struct {
	EventID          int64
	Disabled         bool
	Count            int64
	ThresholdReached bool
	Idempotent       bool
}

type DBBanCounter struct {
	store BanStore
}

func NewBanCounter(store BanStore) *DBBanCounter {
	return &DBBanCounter{store: store}
}

func (c *DBBanCounter) RecordAndCheck(ctx context.Context, event ModerationEvent, cfg ModerationConfig) (BanResult, error) {
	if c == nil || c.store == nil {
		return BanResult{}, nil
	}
	if event.Decision != DecisionBlockKeyword && event.Decision != DecisionBlockHash && event.Decision != DecisionBlockExternal {
		return BanResult{}, nil
	}
	return c.store.RecordModerationViolation(ctx, event, cfg)
}

func isCountedViolation(decision Decision) bool {
	switch decision {
	case DecisionBlockKeyword, DecisionBlockHash, DecisionBlockExternal:
		return true
	default:
		return false
	}
}
