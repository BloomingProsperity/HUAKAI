package workerpulse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Record 是一次作业心跳。
type Record struct {
	JobKey    string
	ReplicaID string
	SeenAt    time.Time
	SuccessAt time.Time
	LastError string
	Executor  bool
}

// Pulse 是库内一行心跳。
type Pulse struct {
	JobKey    string
	ReplicaID string
	LastSeen  time.Time
	SuccessAt time.Time
	LastError string
	Executor  bool
}

// Recorder 写入本副本心跳。
type Recorder interface {
	Record(context.Context, Record) error
}

// Reader 读取某一作业的全部副本心跳。
type Reader interface {
	List(context.Context, string) ([]Pulse, error)
}

// Store 是 PostgreSQL 心跳面。
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Record(ctx context.Context, rec Record) error {
	if s == nil || s.pool == nil {
		return errors.New("worker pulse store unavailable")
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
	var success any
	if !rec.SuccessAt.IsZero() {
		success = rec.SuccessAt.UTC()
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO worker_job_pulses (job_key, replica_id, last_seen_at, last_success_at, last_error, executor)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (job_key, replica_id) DO UPDATE SET
    last_seen_at = EXCLUDED.last_seen_at,
    last_success_at = COALESCE(EXCLUDED.last_success_at, worker_job_pulses.last_success_at),
    last_error = EXCLUDED.last_error,
    executor = EXCLUDED.executor
`, jobKey, replicaID, seen, success, rec.LastError, rec.Executor)
	if err != nil {
		return fmt.Errorf("record worker pulse: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, jobKey string) ([]Pulse, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("worker pulse store unavailable")
	}
	jobKey = strings.TrimSpace(jobKey)
	if jobKey == "" {
		return nil, errors.New("worker pulse job is required")
	}
	rows, err := s.pool.Query(ctx, `
SELECT job_key, replica_id, last_seen_at, last_success_at, last_error, executor
FROM worker_job_pulses
WHERE job_key = $1
ORDER BY replica_id
`, jobKey)
	if err != nil {
		return nil, fmt.Errorf("list worker pulses: %w", err)
	}
	defer rows.Close()
	return scanPulses(rows)
}

func scanPulses(rows pgx.Rows) ([]Pulse, error) {
	out := make([]Pulse, 0)
	for rows.Next() {
		var item Pulse
		var success *time.Time
		if err := rows.Scan(&item.JobKey, &item.ReplicaID, &item.LastSeen, &success, &item.LastError, &item.Executor); err != nil {
			return nil, fmt.Errorf("scan worker pulse: %w", err)
		}
		if success != nil {
			item.SuccessAt = success.UTC()
		}
		item.LastSeen = item.LastSeen.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
