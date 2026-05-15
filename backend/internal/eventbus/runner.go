package eventbus

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

type handlerJob struct {
	ctx    context.Context
	event  RequestCompletionEvent
	result chan error
}

type handlerRunner struct {
	bus     *Bus
	handler Handler
	queue   chan handlerJob
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	active  int64
}

func newHandlerRunner(bus *Bus, h Handler) *handlerRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &handlerRunner{
		bus:     bus,
		handler: h,
		queue:   make(chan handlerJob, bus.bufferSize(h.Tier())),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (r *handlerRunner) start() {
	workers := r.bus.workerCount(r.handler.Tier())
	for i := 0; i < workers; i++ {
		r.wg.Add(1)
		go r.loop()
	}
}

func (r *handlerRunner) submit(ctx context.Context, event RequestCompletionEvent, await bool) error {
	if r == nil || r.bus == nil {
		return ErrBusClosed
	}
	r.bus.mu.RLock()
	closed := r.bus.closed
	r.bus.mu.RUnlock()
	if closed {
		return ErrBusClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var result chan error
	if await {
		result = make(chan error, 1)
	}
	job := handlerJob{ctx: ctx, event: event, result: result}
	if await {
		select {
		case r.queue <- job:
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done():
			return ErrBusClosed
		}
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done():
			return ErrBusClosed
		}
	}
	select {
	case r.queue <- job:
		return nil
	default:
		r.dropOldest(event, "queue_full_drop_oldest")
	}
	select {
	case r.queue <- job:
		return nil
	default:
		r.bus.writeDLQ(event, r.handler, fmt.Errorf("%w: %s", ErrQueueFull, r.handler.ID()))
		return ErrQueueFull
	}
}

func (r *handlerRunner) dropOldest(event RequestCompletionEvent, reason string) {
	select {
	case old := <-r.queue:
		r.finish(old, ErrHandlerDropped)
	default:
	}
	if r.bus.onDrop != nil {
		r.bus.onDrop(DropNotice{
			HandlerID: r.handler.ID(),
			Tier:      r.handler.Tier(),
			EventID:   event.ID,
			Reason:    reason,
			DroppedAt: r.bus.now(),
		})
	}
}

func (r *handlerRunner) loop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.ctx.Done():
			return
		case job := <-r.queue:
			atomic.AddInt64(&r.active, 1)
			err := r.runJob(job)
			atomic.AddInt64(&r.active, -1)
			r.finish(job, err)
		}
	}
}

func (r *handlerRunner) runJob(job handlerJob) error {
	base := r.ctx
	if job.result != nil && job.ctx != nil {
		base = job.ctx
	}
	timeout := r.bus.handlerTimeout(r.handler)
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()

	r.bus.setState(job.event, r.handler.ID(), HandlerStateInflight, nil)
	done := make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- fmt.Errorf("%w: %v", ErrHandlerPanic, v)
			}
		}()
		done <- r.handler.Handle(ctx, job.event)
	}()
	select {
	case err := <-done:
		if err != nil {
			r.bus.setState(job.event, r.handler.ID(), HandlerStateFailed, err)
			r.bus.writeDLQ(job.event, r.handler, err)
			return err
		}
		r.bus.setState(job.event, r.handler.ID(), HandlerStateDone, nil)
		return nil
	case <-ctx.Done():
		err := fmt.Errorf("%w: %s", ErrHandlerTimeout, r.handler.ID())
		r.bus.setState(job.event, r.handler.ID(), HandlerStateFailed, err)
		r.bus.writeDLQ(job.event, r.handler, err)
		return err
	}
}

func (r *handlerRunner) finish(job handlerJob, err error) {
	if job.result == nil {
		return
	}
	select {
	case job.result <- err:
	default:
	}
}

func (r *handlerRunner) pending() int {
	if r == nil {
		return 0
	}
	return len(r.queue) + int(atomic.LoadInt64(&r.active))
}

func (r *handlerRunner) stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
}

func (r *handlerRunner) wait() {
	if r == nil {
		return
	}
	r.wg.Wait()
}
