package modelsync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceSyncAddsNewUpstreamModelToRegistry(t *testing.T) {
	ctx := context.Background()
	store := newMemoryRegistry()
	svc := NewService(ServiceConfig{
		Fetchers: []Fetcher{
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{
					Vendor: VendorOpenAI,
					Models: []Model{{
						ID:             "gpt-new-sync",
						DisplayName:    "GPT New Sync",
						OwnedBy:        "openai",
						ProtocolFamily: "openai_chat",
						ContextWindow:  128000,
					}},
				}, nil
			}),
		},
		Store: store,
		Now:   fixedSyncNow,
	})

	result, err := svc.Sync(ctx, "unit-test")
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if result.TotalAdded != 1 {
		t.Fatalf("TotalAdded=%d want 1 result=%+v", result.TotalAdded, result)
	}
	if !store.available["openai/gpt-new-sync"] {
		t.Fatalf("new upstream model missing from registry after sync: %+v", store.available)
	}
	if got := store.models["openai/gpt-new-sync"].ContextWindow; got != 128000 {
		t.Fatalf("context window=%d want refreshed upstream value", got)
	}
}

func TestServiceSyncMarksRemovedUpstreamModelUnavailableWithoutDeleting(t *testing.T) {
	ctx := context.Background()
	store := newMemoryRegistry()
	store.models["anthropic/claude-removed"] = Model{
		ID:             "claude-removed",
		OwnedBy:        "anthropic",
		ProtocolFamily: "anthropic_messages",
	}
	store.available["anthropic/claude-removed"] = true

	svc := NewService(ServiceConfig{
		Fetchers: []Fetcher{
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{Vendor: VendorAnthropic, Models: nil}, nil
			}),
		},
		Store: store,
		Now:   fixedSyncNow,
	})

	result, err := svc.Sync(ctx, "unit-test")
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if result.TotalDisabled != 1 {
		t.Fatalf("TotalDisabled=%d want 1 result=%+v", result.TotalDisabled, result)
	}
	if store.deleted["anthropic/claude-removed"] {
		t.Fatalf("removed upstream model was hard-deleted")
	}
	if store.available["anthropic/claude-removed"] {
		t.Fatalf("removed upstream model remains available after sync")
	}
	if _, ok := store.models["anthropic/claude-removed"]; !ok {
		t.Fatalf("removed upstream model row missing; sync must mark unavailable, not delete")
	}
}

func TestServiceSyncFailureDoesNotApplyPartialCatalog(t *testing.T) {
	ctx := context.Background()
	store := newMemoryRegistry()
	store.models["openai/gpt-existing"] = Model{ID: "gpt-existing", OwnedBy: "openai", ProtocolFamily: "openai_chat"}
	store.available["openai/gpt-existing"] = true

	fetchErr := errors.New("vendor unavailable")
	svc := NewService(ServiceConfig{
		Fetchers: []Fetcher{
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{
					Vendor: VendorOpenAI,
					Models: []Model{{ID: "gpt-new-before-failure", OwnedBy: "openai", ProtocolFamily: "openai_chat"}},
				}, nil
			}),
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{}, fetchErr
			}),
		},
		Store: store,
		Now:   fixedSyncNow,
	})

	_, err := svc.Sync(ctx, "unit-test")
	if !errors.Is(err, fetchErr) {
		t.Fatalf("err=%v want vendor fetch error", err)
	}
	if store.applyCalls != 0 {
		t.Fatalf("partial sync called ApplyVendorCatalog %d time(s); failure must not pollute registry", store.applyCalls)
	}
	if !store.available["openai/gpt-existing"] {
		t.Fatalf("existing registry state was polluted by failed sync")
	}
	if store.available["openai/gpt-new-before-failure"] {
		t.Fatalf("partial fetched model became available despite later vendor failure")
	}
}

func TestServiceSyncRejectsMultiVendorWithoutAtomicStore(t *testing.T) {
	ctx := context.Background()
	store := newMemoryRegistry()
	svc := NewService(ServiceConfig{
		Fetchers: []Fetcher{
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{Vendor: VendorOpenAI, Models: []Model{{
					ID:             "gpt-atomic-a",
					OwnedBy:        "openai",
					ProtocolFamily: "openai_chat",
				}}}, nil
			}),
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{Vendor: VendorGemini, Models: []Model{{
					ID:             "gemini-atomic-b",
					OwnedBy:        "google",
					ProtocolFamily: "gemini",
				}}}, nil
			}),
		},
		Store: store,
		Now:   fixedSyncNow,
	})

	_, err := svc.Sync(ctx, "unit-test")
	if !errors.Is(err, ErrAtomicStoreRequired) {
		t.Fatalf("err=%v want ErrAtomicStoreRequired", err)
	}
	if store.applyCalls != 0 {
		t.Fatalf("non-atomic multi-vendor sync applied %d catalog(s)", store.applyCalls)
	}
}

func TestServiceSyncPassesReasonAndActorToBatchStore(t *testing.T) {
	ctx := context.Background()
	store := &batchMemoryRegistry{memoryRegistry: *newMemoryRegistry()}
	svc := NewService(ServiceConfig{
		Fetchers: []Fetcher{
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{Vendor: VendorOpenAI, Models: []Model{{
					ID:             "gpt-batch-a",
					OwnedBy:        "openai",
					ProtocolFamily: "openai_chat",
				}}}, nil
			}),
			fetcherFunc(func(context.Context) (Catalog, error) {
				return Catalog{Vendor: VendorGemini, Models: []Model{{
					ID:             "gemini-batch-b",
					OwnedBy:        "google",
					ProtocolFamily: "gemini",
				}}}, nil
			}),
		},
		Store: store,
		Now:   fixedSyncNow,
	})

	result, err := svc.SyncWithActor(ctx, "manual reason", "admin_token:42")
	if err != nil {
		t.Fatalf("SyncWithActor returned error: %v", err)
	}
	if store.lastOptions.Reason != "manual reason" || store.lastOptions.Actor != "admin_token:42" {
		t.Fatalf("options=%+v want reason and actor propagated", store.lastOptions)
	}
	if result.TotalAdded != 2 {
		t.Fatalf("TotalAdded=%d want 2 result=%+v", result.TotalAdded, result)
	}
}

func TestServiceSyncIdenticalCatalogDoesNotReportRuntimeRefresh(t *testing.T) {
	ctx := context.Background()
	store := newMemoryRegistry()
	fetcher := fetcherFunc(func(context.Context) (Catalog, error) {
		return Catalog{
			Vendor: VendorGemini,
			Models: []Model{{
				ID:             "gemini-noop",
				DisplayName:    "Gemini Noop",
				OwnedBy:        "google",
				ProtocolFamily: "gemini",
				ContextWindow:  1048576,
				Capabilities:   []string{"generateContent"},
			}},
		}, nil
	})
	svc := NewService(ServiceConfig{
		Fetchers: []Fetcher{fetcher},
		Store:    store,
		Now:      fixedSyncNow,
	})

	first, err := svc.Sync(ctx, "unit-test")
	if err != nil {
		t.Fatalf("first Sync returned error: %v", err)
	}
	if first.TotalAdded != 1 {
		t.Fatalf("first TotalAdded=%d want 1 result=%+v", first.TotalAdded, first)
	}

	second, err := svc.Sync(ctx, "unit-test")
	if err != nil {
		t.Fatalf("second Sync returned error: %v", err)
	}
	if second.TotalAdded != 0 || second.TotalUpdated != 0 || second.TotalDisabled != 0 {
		t.Fatalf("second sync result=%+v want no runtime refresh for identical catalog", second)
	}
	if got := second.Results[0].Unchanged; got != 1 {
		t.Fatalf("second unchanged=%d want 1", got)
	}
}

type fetcherFunc func(context.Context) (Catalog, error)

func (f fetcherFunc) FetchCatalog(ctx context.Context) (Catalog, error) {
	return f(ctx)
}

type memoryRegistry struct {
	models     map[string]Model
	available  map[string]bool
	deleted    map[string]bool
	applyCalls int
}

func newMemoryRegistry() *memoryRegistry {
	return &memoryRegistry{
		models:    map[string]Model{},
		available: map[string]bool{},
		deleted:   map[string]bool{},
	}
}

func (s *memoryRegistry) ApplyVendorCatalog(_ context.Context, catalog Catalog, _ ApplyOptions) (ApplyResult, error) {
	s.applyCalls++
	incoming := map[string]Model{}
	var out ApplyResult
	out.Vendor = catalog.Vendor
	for _, model := range catalog.Models {
		key := string(catalog.Vendor) + "/" + model.ID
		incoming[key] = model
		existing, existed := s.models[key]
		if !existed {
			out.Added++
			s.models[key] = model
		} else if !s.available[key] {
			out.Reactivated++
			s.models[key] = model
		} else if !reflect.DeepEqual(existing, model) {
			out.Updated++
			s.models[key] = model
		} else {
			out.Unchanged++
		}
		s.available[key] = true
	}

	for key := range s.models {
		if !hasVendorPrefix(key, catalog.Vendor) {
			continue
		}
		if _, ok := incoming[key]; ok {
			continue
		}
		if s.available[key] {
			out.Disabled++
		}
		s.available[key] = false
	}
	return out, nil
}

type batchMemoryRegistry struct {
	memoryRegistry
	lastOptions ApplyOptions
}

func (s *batchMemoryRegistry) ApplyVendorCatalogs(ctx context.Context, catalogs []Catalog, opts ApplyOptions) ([]ApplyResult, error) {
	s.lastOptions = opts
	results := make([]ApplyResult, 0, len(catalogs))
	for _, catalog := range catalogs {
		result, err := s.memoryRegistry.ApplyVendorCatalog(ctx, catalog, opts)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func hasVendorPrefix(key string, vendor Vendor) bool {
	prefix := string(vendor) + "/"
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}

func fixedSyncNow() time.Time {
	return time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
}
