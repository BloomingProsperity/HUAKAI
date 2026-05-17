package dlq

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryOutbox struct {
	mu     sync.Mutex
	events map[string]OutboxEvent
	order  []string
	dead   []DeadEvent
	now    func() time.Time
}

type MemoryOption func(*MemoryOutbox)

func WithMemoryClock(now func() time.Time) MemoryOption {
	return func(m *MemoryOutbox) {
		if now != nil {
			m.now = now
		}
	}
}

func NewMemoryOutbox(opts ...MemoryOption) *MemoryOutbox {
	m := &MemoryOutbox{
		events: make(map[string]OutboxEvent),
		now:    func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

func (m *MemoryOutbox) Enqueue(_ context.Context, e OutboxEvent) (OutboxEvent, error) {
	if m == nil {
		return OutboxEvent{}, ErrOutboxNotConfigured
	}
	e, err := normalizeEvent(e, m.now())
	if err != nil {
		return OutboxEvent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.events[e.ID]; !exists {
		m.order = append(m.order, e.ID)
	}
	m.events[e.ID] = e.clone()
	return e.clone(), nil
}

func (m *MemoryOutbox) Dequeue(_ context.Context, opts DequeueOptions) (OutboxEvent, bool, error) {
	if m == nil {
		return OutboxEvent{}, false, ErrOutboxNotConfigured
	}
	now := opts.Now
	if now.IsZero() {
		now = m.now()
	}
	visibility := opts.VisibilityTimeout
	if visibility <= 0 {
		visibility = 15 * time.Minute
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := make([]OutboxEvent, 0, len(m.events))
	for _, id := range m.order {
		ev := m.events[id]
		if opts.Priority != PriorityAny && opts.Priority != "" && ev.Priority != opts.Priority {
			continue
		}
		if ev.Status != StatusPending && ev.Status != StatusFailedRetry && ev.Status != StatusProcessing {
			continue
		}
		if ev.NextRetryAt.After(now) {
			continue
		}
		candidates = append(candidates, ev)
	}
	if len(candidates) == 0 {
		return OutboxEvent{}, false, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if opts.Priority == PriorityAny || opts.Priority == "" {
			if priorityRank(candidates[i].Priority) != priorityRank(candidates[j].Priority) {
				return priorityRank(candidates[i].Priority) > priorityRank(candidates[j].Priority)
			}
		}
		if !candidates[i].NextRetryAt.Equal(candidates[j].NextRetryAt) {
			return candidates[i].NextRetryAt.Before(candidates[j].NextRetryAt)
		}
		if !candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].ID < candidates[j].ID
	})
	ev := candidates[0]
	ev.Status = StatusProcessing
	ev.NextRetryAt = now.Add(visibility)
	m.events[ev.ID] = ev
	return ev.clone(), true, nil
}

func (m *MemoryOutbox) MarkCompleted(_ context.Context, id string) error {
	return m.update(id, func(ev *OutboxEvent) {
		ev.Status = StatusCompleted
		ev.FailureReason = ""
	})
}

func (m *MemoryOutbox) MarkFailedRetry(_ context.Context, id, reason string, next time.Time) error {
	return m.update(id, func(ev *OutboxEvent) {
		ev.AttemptCount++
		ev.Status = StatusFailedRetry
		ev.FailureReason = RedactString(reason)
		ev.NextRetryAt = next.UTC()
	})
}

func (m *MemoryOutbox) MarkFailedDead(_ context.Context, id, reason string) error {
	if m == nil {
		return ErrOutboxNotConfigured
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ev, ok := m.events[id]
	if !ok {
		return ErrEventNotFound
	}
	reason = RedactString(reason)
	ev.AttemptCount++
	ev.Status = StatusFailedDead
	ev.FailureReason = reason
	m.events[id] = ev
	m.dead = append(m.dead, DeadEvent{
		ID:            newEventID(),
		OutboxEventID: ev.ID,
		TenantID:      ev.TenantID,
		Payload:       append([]byte(nil), ev.Payload...),
		DeadAt:        m.now(),
		DeadReason:    reason,
	})
	return nil
}

func (m *MemoryOutbox) Snapshot() []OutboxEvent {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboxEvent, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.events[id].clone())
	}
	return out
}

func (m *MemoryOutbox) DeadEvents() []DeadEvent {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]DeadEvent, len(m.dead))
	copy(out, m.dead)
	return out
}

func (m *MemoryOutbox) update(id string, fn func(*OutboxEvent)) error {
	if m == nil {
		return ErrOutboxNotConfigured
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ev, ok := m.events[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrEventNotFound, id)
	}
	fn(&ev)
	m.events[id] = ev
	return nil
}
