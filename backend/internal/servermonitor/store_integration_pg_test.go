//go:build integration_pg

package servermonitor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/workerlease"
)

func TestPostgresStoreSessionOrderingRecoveryHistoryAndCleanup(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	if dsn == "" {
		t.Skip("未设置 HUAKAI_DATABASE_URL，跳过服务器监测 PostgreSQL 集成测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	defer pool.Close()
	store := NewPostgresStore(pool)
	nodeID := "test-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM server_monitor_nodes WHERE node_id = $1`, nodeID)
	})

	t0 := time.Now().UTC().Truncate(time.Minute)
	future := validTestSnapshot(t0.Add(10 * time.Minute))
	future.Identity.NodeID = nodeID
	if err := store.WriteSnapshot(ctx, future); !errors.Is(err, ErrSnapshotClockSkew) {
		t.Fatalf("未来时钟快照 err=%v want ErrSnapshotClockSkew", err)
	}
	first := validTestSnapshot(t0)
	first.Identity.NodeID = nodeID
	first.SessionStartedAt = t0.Add(-time.Minute)
	first.Metrics.Memory = nil
	first.MetricStates[MetricGroupMemory] = MetricStateError
	first.CollectionStatus = CollectionStatusPartial
	first.ActiveErrorClasses = []string{"memory_collection_failed"}
	if err := store.WriteSnapshot(ctx, first); err != nil {
		t.Fatalf("写首个部分失败快照: %v", err)
	}

	stale := first
	stale.CollectedAt = t0.Add(20 * time.Second)
	if err := store.WriteSnapshot(ctx, stale); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("同会话重复序号 err=%v want ErrStaleSnapshot", err)
	}
	nonMonotonic := first
	nonMonotonic.Sequence = 2
	if err := store.WriteSnapshot(ctx, nonMonotonic); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("同会话序号增加但采集时间未增加 err=%v want ErrStaleSnapshot", err)
	}

	recovered := validTestSnapshot(t0.Add(30 * time.Second))
	recovered.Identity.NodeID = nodeID
	recovered.SessionID = first.SessionID
	recovered.SessionStartedAt = first.SessionStartedAt
	recovered.Sequence = 2
	if err := store.WriteSnapshot(ctx, recovered); err != nil {
		t.Fatalf("写恢复快照: %v", err)
	}
	node, err := store.GetNode(ctx, nodeID, recovered.CollectedAt, DefaultOfflineAfter)
	if err != nil {
		t.Fatalf("读取当前节点: %v", err)
	}
	if node.LastSequence != 2 || node.CollectionStatus != CollectionStatusSuccess || len(node.ActiveErrorClasses) != 0 {
		t.Fatalf("恢复后节点=%+v", node)
	}
	if node.LastErrorAt == nil || !node.LastErrorAt.Equal(t0) {
		t.Fatalf("last_error_at=%v want %v", node.LastErrorAt, t0)
	}
	if node.LastRecoveredAt == nil || !node.LastRecoveredAt.Equal(recovered.CollectedAt) {
		t.Fatalf("last_recovered_at=%v want %v", node.LastRecoveredAt, recovered.CollectedAt)
	}
	if node.LastSuccessAt == nil || !node.LastSuccessAt.Equal(recovered.CollectedAt) {
		t.Fatalf("last_success_at=%v want %v", node.LastSuccessAt, recovered.CollectedAt)
	}

	tieSession := validTestSnapshot(recovered.CollectedAt)
	tieSession.Identity.NodeID = nodeID
	tieSession.SessionStartedAt = t0.Add(15 * time.Second)
	if err := store.WriteSnapshot(ctx, tieSession); err != nil {
		t.Fatalf("同采集时间的新世代接管: %v", err)
	}

	olderSession := validTestSnapshot(t0.Add(2 * time.Minute))
	olderSession.Identity.NodeID = nodeID
	olderSession.SessionStartedAt = first.SessionStartedAt.Add(-time.Minute)
	if err := store.WriteSnapshot(ctx, olderSession); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("旧世代延迟写入 err=%v want ErrStaleSnapshot", err)
	}

	newSession := validTestSnapshot(t0.Add(2 * time.Minute))
	newSession.Identity.NodeID = nodeID
	newSession.SessionStartedAt = t0.Add(time.Minute)
	newSession.Sequence = 1
	if err := store.WriteSnapshot(ctx, newSession); err != nil {
		t.Fatalf("新世代接管: %v", err)
	}
	node, err = store.GetNode(ctx, nodeID, newSession.CollectedAt, DefaultOfflineAfter)
	if err != nil {
		t.Fatalf("读取新世代节点: %v", err)
	}
	if node.SessionID != newSession.SessionID || node.LastSequence != 1 {
		t.Fatalf("新世代未接管: session=%s sequence=%d", node.SessionID, node.LastSequence)
	}

	points, err := store.ListHistory(ctx, nodeID, t0.Add(-time.Minute), t0.Add(3*time.Minute), 100)
	if err != nil {
		t.Fatalf("读取历史: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("历史点数=%d want 2；同一分钟只能保留最后一份，空桶不得补写", len(points))
	}
	if points[0].SessionID != tieSession.SessionID || points[0].Sequence != 1 || points[1].SessionID != newSession.SessionID {
		t.Fatalf("历史内容=%+v", points)
	}

	summary, err := store.Summary(ctx, newSession.CollectedAt.Add(2*DefaultOfflineAfter), DefaultOfflineAfter)
	if err != nil {
		t.Fatalf("汇总离线状态: %v", err)
	}
	if summary.Total < 1 || summary.Offline < 1 {
		t.Fatalf("离线汇总=%+v", summary)
	}

	cleanup, err := store.Cleanup(ctx, newSession.CollectedAt.Add(time.Second), DefaultCleanupBatch)
	if err != nil {
		t.Fatalf("清理过期节点: %v", err)
	}
	if cleanup.NodesDeleted < 1 {
		t.Fatalf("清理结果=%+v want nodes_deleted>=1", cleanup)
	}
	if _, err := store.GetNode(ctx, nodeID, newSession.CollectedAt, DefaultOfflineAfter); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("清理后 GetNode err=%v want ErrNodeNotFound", err)
	}
}

func TestPostgresNodeLeaseRejectsDuplicateWorkerAndAllowsCleanTakeover(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HUAKAI_DATABASE_URL"))
	if dsn == "" {
		t.Skip("未设置 HUAKAI_DATABASE_URL，跳过服务器监测租约集成测试")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("打开 PostgreSQL: %v", err)
	}
	defer pool.Close()
	nodeID := "test-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM server_monitor_nodes WHERE node_id = $1`, nodeID)
	})
	identity := Identity{NodeID: nodeID, DisplayName: "租约测试节点", Source: IdentitySourceConfigured, Stable: true}
	t0 := time.Now().UTC().Truncate(time.Second)
	firstSession := Session{ID: uuid.New(), StartedAt: t0.Add(-time.Second)}
	first, err := NewWorker(WorkerConfig{
		Identity:  identity,
		Session:   firstSession,
		Collector: &fakeCollector{collection: validCollection(t0)},
		Store:     NewPostgresStore(pool),
		NodeLease: workerlease.NewPostgres(pool, NodeLeaseKey(nodeID), "server_monitor_test_first"),
		Interval:  time.Hour,
	})
	if err != nil {
		t.Fatalf("构造首 worker: %v", err)
	}
	if err := first.Start(ctx); err != nil {
		t.Fatalf("启动首 worker: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = first.Stop(stopCtx)
	}()

	secondSession := Session{ID: uuid.New(), StartedAt: t0.Add(time.Second)}
	second, err := NewWorker(WorkerConfig{
		Identity:  identity,
		Session:   secondSession,
		Collector: &fakeCollector{collection: validCollection(t0.Add(2 * time.Second))},
		Store:     NewPostgresStore(pool),
		NodeLease: workerlease.NewPostgres(pool, NodeLeaseKey(nodeID), "server_monitor_test_second"),
		Interval:  time.Hour,
	})
	if err != nil {
		t.Fatalf("构造第二 worker: %v", err)
	}
	if err := second.Start(ctx); !errors.Is(err, ErrNodeIdentityInUse) {
		t.Fatalf("重复身份启动 err=%v want ErrNodeIdentityInUse", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := first.Stop(stopCtx); err != nil {
		cancel()
		t.Fatalf("停止首 worker: %v", err)
	}
	cancel()
	if err := second.Start(ctx); err != nil {
		t.Fatalf("租约释放后新世代接管: %v", err)
	}
	stopCtx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := second.Stop(stopCtx); err != nil {
		t.Fatalf("停止第二 worker: %v", err)
	}
	node, err := NewPostgresStore(pool).GetNode(ctx, nodeID, t0.Add(2*time.Second), DefaultOfflineAfter)
	if err != nil {
		t.Fatalf("读取接管后的节点: %v", err)
	}
	if node.SessionID != secondSession.ID || node.LastSequence != 1 {
		t.Fatalf("接管后的节点 session=%s sequence=%d", node.SessionID, node.LastSequence)
	}
}
