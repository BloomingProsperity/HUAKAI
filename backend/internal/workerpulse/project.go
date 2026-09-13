package workerpulse

import (
	"context"
	"errors"
	"sort"
	"time"
)

// ReplicaView 是单副本在集群口径中的投影。
type ReplicaView struct {
	ReplicaID     string  `json:"replica_id"`
	Role          string  `json:"role"`
	LastSeenAt    string  `json:"last_seen_at"`
	LastSuccessAt *string `json:"last_success_at,omitempty"`
	LastError     string  `json:"last_error,omitempty"`
}

// ClusterView 是跨副本作业口径。应答本请求的副本身份不在此对象内。
type ClusterView struct {
	Running           bool          `json:"running"`
	ExecutorReplica   string        `json:"executor_replica,omitempty"`
	LastTickAt        *string       `json:"last_tick_at"`
	LastSuccessAt     *string       `json:"last_success_at"`
	LastError         string        `json:"last_error,omitempty"`
	ReplicaCount      int           `json:"replica_count"`
	FreshReplicaCount int           `json:"fresh_replica_count"`
	Replicas          []ReplicaView `json:"replicas"`
}

// ErrUnavailable 表示心跳面缺失,状态入口必须 503。
var ErrUnavailable = errors.New("worker pulse store unavailable")

// Project 把心跳行折成集群口径。now 与 freshness 决定谁仍算在跑。
func Project(now time.Time, rows []Pulse, freshness time.Duration) ClusterView {
	if freshness <= 0 {
		freshness = 3 * time.Minute
	}
	now = now.UTC()
	view := ClusterView{Replicas: make([]ReplicaView, 0, len(rows))}
	var latestTick time.Time
	var latestSuccess time.Time
	var latestExecutorSeen time.Time
	var latestExecutorErr string
	for _, row := range rows {
		stale := row.LastSeen.IsZero() || now.Sub(row.LastSeen) > freshness
		role := RoleFollower
		switch {
		case stale:
			role = RoleStale
		case row.Executor:
			role = RoleExecutor
		}
		item := ReplicaView{
			ReplicaID:  row.ReplicaID,
			Role:       role,
			LastSeenAt: row.LastSeen.UTC().Format(time.RFC3339),
			LastError:  row.LastError,
		}
		if !row.SuccessAt.IsZero() {
			formatted := row.SuccessAt.UTC().Format(time.RFC3339)
			item.LastSuccessAt = &formatted
		}
		view.Replicas = append(view.Replicas, item)
		view.ReplicaCount++
		if !stale {
			view.FreshReplicaCount++
		}
		if row.Executor && row.LastSeen.After(latestTick) {
			latestTick = row.LastSeen
		}
		if row.SuccessAt.After(latestSuccess) {
			latestSuccess = row.SuccessAt
		}
		if row.Executor && !stale && row.LastSeen.After(latestExecutorSeen) {
			latestExecutorSeen = row.LastSeen
			view.ExecutorReplica = row.ReplicaID
			view.Running = true
			latestExecutorErr = row.LastError
		}
	}
	if !latestTick.IsZero() {
		formatted := latestTick.UTC().Format(time.RFC3339)
		view.LastTickAt = &formatted
	}
	if !latestSuccess.IsZero() {
		formatted := latestSuccess.UTC().Format(time.RFC3339)
		view.LastSuccessAt = &formatted
	}
	view.LastError = latestExecutorErr
	sort.Slice(view.Replicas, func(i, j int) bool {
		return view.Replicas[i].ReplicaID < view.Replicas[j].ReplicaID
	})
	return view
}

// Load 读取并投影某一作业。存储不可用时返回错误,调用方必须 fail-closed。
func Load(ctx context.Context, reader Reader, jobKey string, now time.Time) (ClusterView, error) {
	if reader == nil {
		return ClusterView{}, ErrUnavailable
	}
	rows, err := reader.List(ctx, jobKey)
	if err != nil {
		return ClusterView{}, err
	}
	return Project(now, rows, Freshness(jobKey)), nil
}
