package workerpulse

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// MemoryStore 是进程内心跳面,供判别测试使用;生产接线走 PostgreSQL。
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Pulse
	err  error
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{rows: make(map[string]Pulse)}
}

func (s *MemoryStore) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *MemoryStore) Record(_ context.Context, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	jobKey := strings.TrimSpace(rec.JobKey)
	replicaID := strings.TrimSpace(rec.ReplicaID)
	if jobKey == "" || replicaID == "" {
		return errors.New("worker pulse job and replica are required")
	}
	seen := rec.SeenAt.UTC()
	if seen.IsZero() {
		seen = time.Now().UTC()
	}
	key := jobKey + "\x00" + replicaID
	prev := s.rows[key]
	success := rec.SuccessAt.UTC()
	if success.IsZero() {
		success = prev.SuccessAt
	}
	s.rows[key] = Pulse{
		JobKey:    jobKey,
		ReplicaID: replicaID,
		LastSeen:  seen,
		SuccessAt: success,
		LastError: rec.LastError,
		Executor:  rec.Executor,
	}
	return nil
}

func (s *MemoryStore) List(_ context.Context, jobKey string) ([]Pulse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	jobKey = strings.TrimSpace(jobKey)
	if jobKey == "" {
		return nil, errors.New("worker pulse job is required")
	}
	out := make([]Pulse, 0)
	for _, item := range s.rows {
		if item.JobKey == jobKey {
			out = append(out, item)
		}
	}
	return out, nil
}
