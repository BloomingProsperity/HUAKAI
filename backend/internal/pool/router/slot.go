package router

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrSlotManagerUnavailable    = errors.New("pool slot manager unavailable")
	ErrNoSlotAvailable           = errors.New("pool account concurrency slot unavailable")
	ErrBindingConcurrencyLimited = errors.New("pool binding concurrency limit exceeded")
)

type SlotManager interface {
	Acquire(ctx context.Context, account *AccountSnapshot, req SelectionRequest) (*AcquireResult, error)
}

type ReleaseFunc func(context.Context) error

type AcquireResult struct {
	AcquisitionToken uuid.UUID
	Release          ReleaseFunc
}

func (r *AcquireResult) release(ctx context.Context) error {
	if r == nil || r.Release == nil {
		return nil
	}
	return r.Release(ctx)
}

// releaseSlotDetached 补偿 release 脱离请求 ctx (镜像 pasr HIGH-2): 补偿恰发生在
// 请求已取消/超时的场景, 用原 ctx 会连带丢 release, 槽位只能等孤儿扫兜底。
func releaseSlotDetached(ctx context.Context, acquired *AcquireResult) {
	cleanupCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), pasrPostMutationReleaseTimeout)
	defer cancel()
	_ = acquired.release(cleanupCtx)
}

type nilSlotManager struct{}

func (nilSlotManager) Acquire(context.Context, *AccountSnapshot, SelectionRequest) (*AcquireResult, error) {
	return nil, ErrSlotManagerUnavailable
}

func NewIdempotentRelease(token uuid.UUID, fn ReleaseFunc) ReleaseFunc {
	var once sync.Once
	var err error
	return func(ctx context.Context) error {
		if token == uuid.Nil {
			return nil
		}
		once.Do(func() {
			if fn != nil {
				err = fn(ctx)
			}
		})
		return err
	}
}
