// Package servermonitor 采集并持久化网关实例的运行资源状态。
package servermonitor

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type IdentitySource string

const (
	IdentitySourceConfigured          IdentitySource = "configured"
	IdentitySourceRuntimeIdentityHash IdentitySource = "runtime_identity_hash"
)

type SourceKind string

const SourceKindBuiltin SourceKind = "builtin"

type ViewScope string

const (
	ViewScopeHost        ViewScope = "host"
	ViewScopeContainer   ViewScope = "container"
	ViewScopeProcessOnly ViewScope = "process_only"
)

type MetricGroup string

const (
	MetricGroupCPU        MetricGroup = "cpu"
	MetricGroupMemory     MetricGroup = "memory"
	MetricGroupSwap       MetricGroup = "swap"
	MetricGroupLoad       MetricGroup = "load"
	MetricGroupFilesystem MetricGroup = "filesystem"
	MetricGroupDiskIO     MetricGroup = "disk_io"
	MetricGroupNetwork    MetricGroup = "network"
	MetricGroupUptime     MetricGroup = "uptime"
	MetricGroupProcess    MetricGroup = "process"
)

var MetricGroups = []MetricGroup{
	MetricGroupCPU,
	MetricGroupMemory,
	MetricGroupSwap,
	MetricGroupLoad,
	MetricGroupFilesystem,
	MetricGroupDiskIO,
	MetricGroupNetwork,
	MetricGroupUptime,
	MetricGroupProcess,
}

type MetricState string

const (
	MetricStateFresh       MetricState = "fresh"
	MetricStateWarming     MetricState = "warming"
	MetricStateUnavailable MetricState = "unavailable"
	MetricStateError       MetricState = "error"
)

type CollectionStatus string

const (
	CollectionStatusSuccess CollectionStatus = "success"
	CollectionStatusPartial CollectionStatus = "partial"
	CollectionStatusFailed  CollectionStatus = "failed"
)

type CPUStats struct {
	UsagePercent float64 `json:"usage_percent"`
	Capacity     float64 `json:"capacity"`
}

type MemoryStats struct {
	TotalBytes     *uint64  `json:"total_bytes"`
	UsedBytes      uint64   `json:"used_bytes"`
	AvailableBytes *uint64  `json:"available_bytes"`
	UsagePercent   *float64 `json:"usage_percent"`
}

type SwapStats struct {
	TotalBytes   uint64  `json:"total_bytes"`
	UsedBytes    uint64  `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type LoadStats struct {
	OneMinute      float64 `json:"one_minute"`
	FiveMinutes    float64 `json:"five_minutes"`
	FifteenMinutes float64 `json:"fifteen_minutes"`
}

type FilesystemStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsagePercent   float64 `json:"usage_percent"`
}

type DiskIOStats struct {
	ReadBytesPerSecond  float64 `json:"read_bytes_per_second"`
	WriteBytesPerSecond float64 `json:"write_bytes_per_second"`
	ReadOpsPerSecond    float64 `json:"read_ops_per_second"`
	WriteOpsPerSecond   float64 `json:"write_ops_per_second"`
}

type NetworkStats struct {
	ReceiveBytesPerSecond  float64 `json:"receive_bytes_per_second"`
	TransmitBytesPerSecond float64 `json:"transmit_bytes_per_second"`
}

type UptimeStats struct {
	Seconds int64 `json:"seconds"`
}

type ProcessStats struct {
	UptimeSeconds  int64   `json:"uptime_seconds"`
	RSSBytes       *uint64 `json:"rss_bytes"`
	HeapAllocBytes uint64  `json:"heap_alloc_bytes"`
	HeapSysBytes   uint64  `json:"heap_sys_bytes"`
	Goroutines     int     `json:"goroutines"`
	GCCount        uint32  `json:"gc_count"`
}

type Metrics struct {
	CPU        *CPUStats        `json:"cpu"`
	Memory     *MemoryStats     `json:"memory"`
	Swap       *SwapStats       `json:"swap"`
	Load       *LoadStats       `json:"load"`
	Filesystem *FilesystemStats `json:"filesystem"`
	DiskIO     *DiskIOStats     `json:"disk_io"`
	Network    *NetworkStats    `json:"network"`
	Uptime     *UptimeStats     `json:"uptime"`
	Process    *ProcessStats    `json:"process"`
}

type Identity struct {
	NodeID      string         `json:"node_id"`
	DisplayName string         `json:"display_name"`
	Source      IdentitySource `json:"identity_source"`
	Stable      bool           `json:"identity_stable"`
}

type Snapshot struct {
	Identity           Identity                    `json:"identity"`
	SourceKind         SourceKind                  `json:"source_kind"`
	ViewScope          ViewScope                   `json:"view_scope"`
	SessionID          uuid.UUID                   `json:"session_id"`
	SessionStartedAt   time.Time                   `json:"session_started_at"`
	Sequence           int64                       `json:"sequence"`
	CollectedAt        time.Time                   `json:"collected_at"`
	CollectionStatus   CollectionStatus            `json:"collection_status"`
	ActiveErrorClasses []string                    `json:"active_error_classes"`
	OSName             string                      `json:"os_name"`
	OSArch             string                      `json:"os_arch"`
	Metrics            Metrics                     `json:"metrics"`
	MetricStates       map[MetricGroup]MetricState `json:"metric_states"`
}

type Node struct {
	Identity           Identity                    `json:"identity"`
	SourceKind         SourceKind                  `json:"source_kind"`
	ViewScope          ViewScope                   `json:"view_scope"`
	SessionID          uuid.UUID                   `json:"session_id"`
	SessionStartedAt   time.Time                   `json:"session_started_at"`
	LastSequence       int64                       `json:"last_sequence"`
	LastActivityAt     time.Time                   `json:"last_activity_at"`
	LastSuccessAt      *time.Time                  `json:"last_success_at"`
	LastErrorAt        *time.Time                  `json:"last_error_at"`
	LastRecoveredAt    *time.Time                  `json:"last_recovered_at"`
	Online             bool                        `json:"online"`
	CollectionStatus   CollectionStatus            `json:"collection_status"`
	ActiveErrorClasses []string                    `json:"active_error_classes"`
	OSName             string                      `json:"os_name"`
	OSArch             string                      `json:"os_arch"`
	Metrics            Metrics                     `json:"metrics"`
	MetricStates       map[MetricGroup]MetricState `json:"metric_states"`
}

type HistoryPoint struct {
	BucketAt           time.Time                   `json:"bucket_at"`
	CollectedAt        time.Time                   `json:"collected_at"`
	SessionID          uuid.UUID                   `json:"session_id"`
	Sequence           int64                       `json:"sequence"`
	ViewScope          ViewScope                   `json:"view_scope"`
	CollectionStatus   CollectionStatus            `json:"collection_status"`
	ActiveErrorClasses []string                    `json:"active_error_classes"`
	Metrics            Metrics                     `json:"metrics"`
	MetricStates       map[MetricGroup]MetricState `json:"metric_states"`
}

type Summary struct {
	Total    int64 `json:"total"`
	Online   int64 `json:"online"`
	Offline  int64 `json:"offline"`
	Degraded int64 `json:"degraded"`
}

type CleanupResult struct {
	SamplesDeleted int64
	NodesDeleted   int64
}

var (
	ErrNodeNotFound      = errors.New("server monitor node not found")
	ErrStaleSnapshot     = errors.New("server monitor stale snapshot")
	ErrSnapshotClockSkew = errors.New("server monitor snapshot clock skew")
	nodeIDPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	errorClassPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
)

func (s *Snapshot) NormalizeAndValidate() error {
	if s == nil {
		return errors.New("snapshot is required")
	}
	if !nodeIDPattern.MatchString(s.Identity.NodeID) {
		return errors.New("node id must be a 3-64 character lowercase opaque slug")
	}
	s.Identity.DisplayName = strings.TrimSpace(s.Identity.DisplayName)
	if len(s.Identity.DisplayName) == 0 || len(s.Identity.DisplayName) > 128 {
		return errors.New("display name must contain 1-128 characters")
	}
	if s.Identity.Source != IdentitySourceConfigured && s.Identity.Source != IdentitySourceRuntimeIdentityHash {
		return errors.New("identity source is invalid")
	}
	if (s.Identity.Source == IdentitySourceConfigured) != s.Identity.Stable {
		return errors.New("identity source and stability are inconsistent")
	}
	if s.SourceKind != SourceKindBuiltin {
		return errors.New("source kind must be builtin")
	}
	if s.ViewScope != ViewScopeHost && s.ViewScope != ViewScopeContainer && s.ViewScope != ViewScopeProcessOnly {
		return errors.New("view scope is invalid")
	}
	if s.SessionID == uuid.Nil || s.SessionStartedAt.IsZero() || s.CollectedAt.IsZero() || s.Sequence <= 0 {
		return errors.New("session, collection time and positive sequence are required")
	}
	if s.CollectedAt.Before(s.SessionStartedAt) {
		return errors.New("collection time precedes session start")
	}
	s.OSName = strings.TrimSpace(s.OSName)
	s.OSArch = strings.TrimSpace(s.OSArch)
	if s.OSName == "" || len(s.OSName) > 32 || s.OSArch == "" || len(s.OSArch) > 32 {
		return errors.New("operating system metadata is invalid")
	}
	hasMetricError := false
	if len(s.MetricStates) != len(MetricGroups) {
		return fmt.Errorf("metric states must contain all %d groups", len(MetricGroups))
	}
	for _, group := range MetricGroups {
		state, ok := s.MetricStates[group]
		if !ok || !validMetricState(state) {
			return fmt.Errorf("metric state for %s is invalid", group)
		}
		if state == MetricStateFresh && !metricPresent(s.Metrics, group) {
			return fmt.Errorf("fresh metric %s has no value", group)
		}
		if state != MetricStateFresh && metricPresent(s.Metrics, group) {
			return fmt.Errorf("non-fresh metric %s must be null", group)
		}
		hasMetricError = hasMetricError || state == MetricStateError
	}
	s.ActiveErrorClasses = normalizeErrorClasses(s.ActiveErrorClasses)
	for _, class := range s.ActiveErrorClasses {
		if !errorClassPattern.MatchString(class) {
			return fmt.Errorf("error class %q is invalid", class)
		}
	}
	if hasMetricError != (len(s.ActiveErrorClasses) > 0) {
		return errors.New("metric errors and active error classes are inconsistent")
	}
	expected := DeriveCollectionStatus(s.MetricStates)
	if s.CollectionStatus == "" {
		s.CollectionStatus = expected
	}
	if s.CollectionStatus != expected {
		return fmt.Errorf("collection status %s does not match metric states %s", s.CollectionStatus, expected)
	}
	s.SessionStartedAt = s.SessionStartedAt.UTC()
	s.CollectedAt = s.CollectedAt.UTC()
	return nil
}

func DeriveCollectionStatus(states map[MetricGroup]MetricState) CollectionStatus {
	fresh, transient := 0, false
	for _, group := range MetricGroups {
		switch states[group] {
		case MetricStateFresh:
			fresh++
		case MetricStateError, MetricStateWarming:
			transient = true
		}
	}
	if fresh == 0 {
		return CollectionStatusFailed
	}
	if transient {
		return CollectionStatusPartial
	}
	return CollectionStatusSuccess
}

func ValidateNodeID(nodeID string) bool {
	return nodeIDPattern.MatchString(nodeID)
}

func validMetricState(state MetricState) bool {
	return state == MetricStateFresh || state == MetricStateWarming || state == MetricStateUnavailable || state == MetricStateError
}

func metricPresent(metrics Metrics, group MetricGroup) bool {
	switch group {
	case MetricGroupCPU:
		return metrics.CPU != nil
	case MetricGroupMemory:
		return metrics.Memory != nil
	case MetricGroupSwap:
		return metrics.Swap != nil
	case MetricGroupLoad:
		return metrics.Load != nil
	case MetricGroupFilesystem:
		return metrics.Filesystem != nil
	case MetricGroupDiskIO:
		return metrics.DiskIO != nil
	case MetricGroupNetwork:
		return metrics.Network != nil
	case MetricGroupUptime:
		return metrics.Uptime != nil
	case MetricGroupProcess:
		return metrics.Process != nil
	default:
		return false
	}
}

func normalizeErrorClasses(classes []string) []string {
	out := make([]string, 0, len(classes))
	seen := make(map[string]struct{}, len(classes))
	for _, class := range classes {
		class = strings.ToLower(strings.TrimSpace(class))
		if class == "" {
			continue
		}
		if _, exists := seen[class]; exists {
			continue
		}
		seen[class] = struct{}{}
		out = append(out, class)
	}
	slices.Sort(out)
	return out
}
