package modelsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrNoFetchers          = errors.New("modelsync: no vendor fetchers configured")
	ErrNoStore             = errors.New("modelsync: registry store required")
	ErrAtomicStoreRequired = errors.New("modelsync: multi-vendor sync requires atomic store")
)

// Fetcher 拉取一个 vendor 的当前模型目录。
type Fetcher interface {
	FetchCatalog(context.Context) (Catalog, error)
}

// Store 把完整 vendor 快照应用到 HUAKAI registry。
type Store interface {
	ApplyVendorCatalog(context.Context, Catalog, ApplyOptions) (ApplyResult, error)
}

// BatchStore 在一个 registry 事务内应用多 vendor 快照，防止失败 sync 部分落库。
type BatchStore interface {
	ApplyVendorCatalogs(context.Context, []Catalog, ApplyOptions) ([]ApplyResult, error)
}

type ServiceConfig struct {
	Fetchers []Fetcher
	Store    Store
	Now      func() time.Time
}

// Service 编排模型目录同步。它先完成所有 vendor 拉取，再开始写 registry，
// 避免部分 fetch 失败时污染运行时目录。
type Service struct {
	fetchers []Fetcher
	store    Store
	now      func() time.Time
}

func NewService(cfg ServiceConfig) *Service {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		fetchers: append([]Fetcher(nil), cfg.Fetchers...),
		store:    cfg.Store,
		now:      now,
	}
}

func (s *Service) Sync(ctx context.Context, reason string) (SyncResult, error) {
	return s.SyncWithActor(ctx, reason, "model_sync_scheduler")
}

func (s *Service) SyncWithActor(ctx context.Context, reason, actor string) (SyncResult, error) {
	if s == nil || len(s.fetchers) == 0 {
		return SyncResult{}, ErrNoFetchers
	}
	if s.store == nil {
		return SyncResult{}, ErrNoStore
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := s.now().UTC()

	catalogs := make([]Catalog, 0, len(s.fetchers))
	for _, fetcher := range s.fetchers {
		if fetcher == nil {
			continue
		}
		catalog, err := fetcher.FetchCatalog(ctx)
		if err != nil {
			return SyncResult{}, err
		}
		if err := validateCatalog(catalog); err != nil {
			return SyncResult{}, err
		}
		catalogs = append(catalogs, catalog)
	}
	if len(catalogs) == 0 {
		return SyncResult{}, ErrNoFetchers
	}

	opts := ApplyOptions{Reason: strings.TrimSpace(reason), Actor: strings.TrimSpace(actor)}
	out := SyncResult{StartedAt: started, Results: make([]ApplyResult, 0, len(catalogs))}
	if batch, ok := s.store.(BatchStore); ok {
		results, err := batch.ApplyVendorCatalogs(ctx, catalogs, opts)
		if err != nil {
			return SyncResult{}, err
		}
		for i, applied := range results {
			if applied.Vendor == "" && i < len(catalogs) {
				applied.Vendor = catalogs[i].Vendor
			}
			out.addResult(applied)
		}
		out.CompletedAt = s.now().UTC()
		return out, nil
	}
	if len(catalogs) > 1 {
		return SyncResult{}, ErrAtomicStoreRequired
	}
	for _, catalog := range catalogs {
		applied, err := s.store.ApplyVendorCatalog(ctx, catalog, opts)
		if err != nil {
			return SyncResult{}, err
		}
		if applied.Vendor == "" {
			applied.Vendor = catalog.Vendor
		}
		out.addResult(applied)
	}
	out.CompletedAt = s.now().UTC()
	return out, nil
}

func (r *SyncResult) addResult(applied ApplyResult) {
	r.Results = append(r.Results, applied)
	r.TotalAdded += applied.Added + applied.Reactivated
	r.TotalUpdated += applied.Updated
	r.TotalDisabled += applied.Disabled
	r.TotalDiscovered += applied.Discovered
	r.TotalDiscoveryUpdated += applied.DiscoveryUpdated
	r.TotalDiscoveryAbsent += applied.DiscoveryAbsent
}

func validateCatalog(c Catalog) error {
	if c.Vendor == "" {
		return fmt.Errorf("modelsync: vendor required")
	}
	seen := make(map[string]struct{}, len(c.Models))
	for i, model := range c.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return fmt.Errorf("modelsync: %s model[%d] id required", c.Vendor, i)
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("modelsync: %s duplicate model id %q", c.Vendor, id)
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(model.ProtocolFamily) == "" {
			return fmt.Errorf("modelsync: %s model[%d] protocol family required", c.Vendor, i)
		}
	}
	return nil
}
