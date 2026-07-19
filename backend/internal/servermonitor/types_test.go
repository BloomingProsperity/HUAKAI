package servermonitor

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSnapshotRejectsStaleValueMasqueradingAsError(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 0, 0, 0, time.UTC)
	snapshot := validTestSnapshot(now)
	snapshot.MetricStates[MetricGroupMemory] = MetricStateError
	if err := snapshot.NormalizeAndValidate(); err == nil {
		t.Fatal("memory 已标 error 但仍携带旧值时必须拒绝")
	}
}

func TestSnapshotNormalizesStableErrorClasses(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 0, 0, 0, time.UTC)
	snapshot := validTestSnapshot(now)
	snapshot.Metrics.Memory = nil
	snapshot.MetricStates[MetricGroupMemory] = MetricStateError
	snapshot.CollectionStatus = CollectionStatusPartial
	snapshot.ActiveErrorClasses = []string{"memory_collection_failed", " memory_collection_failed "}
	if err := snapshot.NormalizeAndValidate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(snapshot.ActiveErrorClasses) != 1 || snapshot.ActiveErrorClasses[0] != "memory_collection_failed" {
		t.Fatalf("errors=%v", snapshot.ActiveErrorClasses)
	}
}

func TestSnapshotRejectsIdentityAndErrorStateContradictions(t *testing.T) {
	now := time.Date(2026, 7, 19, 3, 30, 0, 0, time.UTC)
	unstableConfigured := validTestSnapshot(now)
	unstableConfigured.Identity.Stable = false
	if err := unstableConfigured.NormalizeAndValidate(); err == nil {
		t.Fatal("显式配置身份不得标记为不稳定")
	}

	stableRuntimeHash := validTestSnapshot(now)
	stableRuntimeHash.Identity.Source = IdentitySourceRuntimeIdentityHash
	if err := stableRuntimeHash.NormalizeAndValidate(); err == nil {
		t.Fatal("运行环境摘要身份不得冒充稳定身份")
	}

	missingErrorClass := validTestSnapshot(now)
	missingErrorClass.Metrics.Memory = nil
	missingErrorClass.MetricStates[MetricGroupMemory] = MetricStateError
	missingErrorClass.CollectionStatus = CollectionStatusPartial
	if err := missingErrorClass.NormalizeAndValidate(); err == nil {
		t.Fatal("指标已失败但没有活动错误分类时必须拒绝")
	}

	phantomErrorClass := validTestSnapshot(now)
	phantomErrorClass.ActiveErrorClasses = []string{"memory_collection_failed"}
	if err := phantomErrorClass.NormalizeAndValidate(); err == nil {
		t.Fatal("没有失败指标却携带活动错误分类时必须拒绝")
	}
}

func TestConfiguredIdentityIsStableAndOpaque(t *testing.T) {
	identity, err := ResolveIdentity(Config{NodeID: "edge-cn-01", DisplayName: "北京出口 1"})
	if err != nil {
		t.Fatalf("resolve identity: %v", err)
	}
	if !identity.Stable || identity.Source != IdentitySourceConfigured || identity.NodeID != "edge-cn-01" {
		t.Fatalf("identity=%+v", identity)
	}
	if _, err := ResolveIdentity(Config{NodeID: "Bad Host Name"}); err == nil {
		t.Fatal("包含主机名式空格与大写的 node id 必须拒绝")
	}
}

func validTestSnapshot(now time.Time) Snapshot {
	total, available, usage := uint64(100), uint64(50), float64(50)
	states := make(map[MetricGroup]MetricState, len(MetricGroups))
	for _, group := range MetricGroups {
		states[group] = MetricStateFresh
	}
	return Snapshot{
		Identity:         Identity{NodeID: "node-test-01", DisplayName: "测试节点", Source: IdentitySourceConfigured, Stable: true},
		SourceKind:       SourceKindBuiltin,
		ViewScope:        ViewScopeHost,
		SessionID:        uuid.New(),
		SessionStartedAt: now.Add(-time.Minute),
		Sequence:         1,
		CollectedAt:      now,
		CollectionStatus: CollectionStatusSuccess,
		OSName:           "linux",
		OSArch:           "amd64",
		Metrics: Metrics{
			CPU:        &CPUStats{},
			Memory:     &MemoryStats{TotalBytes: &total, UsedBytes: 50, AvailableBytes: &available, UsagePercent: &usage},
			Swap:       &SwapStats{},
			Load:       &LoadStats{},
			Filesystem: &FilesystemStats{},
			DiskIO:     &DiskIOStats{},
			Network:    &NetworkStats{},
			Uptime:     &UptimeStats{},
			Process:    &ProcessStats{},
		},
		MetricStates: states,
	}
}
