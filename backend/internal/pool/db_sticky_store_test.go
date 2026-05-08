// db_sticky_store_test.go — DBStickyStore 单测（不连真 DB, stub repo）。
package pool

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	"github.com/jackc/pgx/v5"
)

// stubStickyRepo 实现 stickyBindingReader 用于测试。
type stubStickyRepo struct {
	get func(ctx context.Context, arg db.GetStickyBindingParams) (int64, error)
}

func (s *stubStickyRepo) GetStickyBinding(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
	if s.get != nil {
		return s.get(ctx, arg)
	}
	return 0, pgx.ErrNoRows
}

func TestDBStickyStore_HappyHit(t *testing.T) {
	repo := &stubStickyRepo{
		get: func(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
			if arg.TenantID != 1 || arg.SessionHash != "h" || arg.Model != "claude-3" {
				t.Errorf("意外参数: %+v", arg)
			}
			return 42, nil
		},
	}
	store := NewDBStickyStore(repo)
	id, found, err := store.Lookup(context.Background(), SelectionRequest{
		TenantID: 1, SessionHash: "h", RequestedModel: "claude-3",
	})
	if err != nil || !found || id != 42 {
		t.Errorf("expected (42, true, nil), got (%d, %v, %v)", id, found, err)
	}
}

func TestDBStickyStore_MissReturnsNoError(t *testing.T) {
	repo := &stubStickyRepo{
		get: func(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
			return 0, pgx.ErrNoRows
		},
	}
	store := NewDBStickyStore(repo)
	id, found, err := store.Lookup(context.Background(), SelectionRequest{
		TenantID: 1, SessionHash: "h", RequestedModel: "m",
	})
	if err != nil || found || id != 0 {
		t.Errorf("expected (0, false, nil), got (%d, %v, %v)", id, found, err)
	}
}

func TestDBStickyStore_EmptySessionHashSkips(t *testing.T) {
	called := false
	repo := &stubStickyRepo{
		get: func(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
			called = true
			return 99, nil
		},
	}
	store := NewDBStickyStore(repo)
	_, found, _ := store.Lookup(context.Background(), SelectionRequest{
		TenantID: 1, SessionHash: "", RequestedModel: "m",
	})
	if found {
		t.Error("空 SessionHash 不应 found")
	}
	if called {
		t.Error("空 SessionHash 不应 query DB")
	}
}

func TestDBStickyStore_MissingTenantOrModelSkips(t *testing.T) {
	called := false
	repo := &stubStickyRepo{
		get: func(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
			called = true
			return 1, nil
		},
	}
	store := NewDBStickyStore(repo)
	cases := []SelectionRequest{
		{TenantID: 0, SessionHash: "h", RequestedModel: "m"},
		{TenantID: 1, SessionHash: "h", RequestedModel: ""},
	}
	for _, req := range cases {
		_, found, _ := store.Lookup(context.Background(), req)
		if found {
			t.Errorf("缺必填字段不应 found: %+v", req)
		}
	}
	if called {
		t.Error("缺必填字段不应 query DB")
	}
}

func TestDBStickyStore_PassesThroughOtherErrors(t *testing.T) {
	wantErr := errors.New("db transient")
	repo := &stubStickyRepo{
		get: func(ctx context.Context, arg db.GetStickyBindingParams) (int64, error) {
			return 0, wantErr
		},
	}
	store := NewDBStickyStore(repo)
	_, _, err := store.Lookup(context.Background(), SelectionRequest{
		TenantID: 1, SessionHash: "h", RequestedModel: "m",
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("非 ErrNoRows 错误应透传, got %v", err)
	}
}

func TestDBStickyStore_NilRepoSafe(t *testing.T) {
	var store *DBStickyStore
	_, found, err := store.Lookup(context.Background(), SelectionRequest{TenantID: 1, SessionHash: "h", RequestedModel: "m"})
	if found || err != nil {
		t.Errorf("nil store 应 (0, false, nil)")
	}
	store2 := &DBStickyStore{repo: nil}
	_, found2, err2 := store2.Lookup(context.Background(), SelectionRequest{TenantID: 1, SessionHash: "h", RequestedModel: "m"})
	if found2 || err2 != nil {
		t.Errorf("nil repo 应 (0, false, nil)")
	}
}
