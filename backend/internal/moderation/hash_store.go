package moderation

import "context"

type HashSource interface {
	Contains(context.Context, int64, string) (HashMatch, error)
}

type hashCacheKey struct {
	tenantID int64
	hashHex  string
}

type CachedHashStore struct {
	source HashSource
	cache  *ttlLRU[hashCacheKey, HashMatch]
}

func NewHashStore(source HashSource, opts CacheOptions) *CachedHashStore {
	return &CachedHashStore{
		source: source,
		cache:  newTTLLRU[hashCacheKey, HashMatch](opts),
	}
}

func (s *CachedHashStore) Contains(ctx context.Context, tenantID int64, hashHex string) (HashMatch, error) {
	if s == nil || s.source == nil || hashHex == "" {
		return HashMatch{}, nil
	}
	key := hashCacheKey{tenantID: tenantID, hashHex: hashHex}
	if match, ok := s.cache.Get(key); ok {
		return match, nil
	}
	match, err := s.source.Contains(ctx, tenantID, hashHex)
	if err != nil {
		return HashMatch{}, err
	}
	s.cache.Set(key, match)
	return match, nil
}
