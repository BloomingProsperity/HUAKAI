package moderation

import "context"

type KeywordSource interface {
	ListEnabled(context.Context, int64) ([]KeywordRule, error)
}

type CachedKeywordStore struct {
	source KeywordSource
	cache  *ttlLRU[int64, []KeywordRule]
}

func NewKeywordStore(source KeywordSource, opts CacheOptions) *CachedKeywordStore {
	return &CachedKeywordStore{
		source: source,
		cache:  newTTLLRU[int64, []KeywordRule](opts),
	}
}

func (s *CachedKeywordStore) ListEnabled(ctx context.Context, tenantID int64) ([]KeywordRule, error) {
	if s == nil || s.source == nil {
		return nil, nil
	}
	if rules, ok := s.cache.Get(tenantID); ok {
		return copyKeywordRules(rules), nil
	}
	rules, err := s.source.ListEnabled(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rules = copyKeywordRules(rules)
	s.cache.Set(tenantID, rules)
	return copyKeywordRules(rules), nil
}

func copyKeywordRules(in []KeywordRule) []KeywordRule {
	if len(in) == 0 {
		return nil
	}
	out := make([]KeywordRule, len(in))
	copy(out, in)
	return out
}
