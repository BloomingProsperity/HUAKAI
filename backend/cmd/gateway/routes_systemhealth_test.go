package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/servermonitor"
)

type systemHealthDBStub struct {
	pingErr error
	rows    []pgx.Row
	queries []string
}

func (s *systemHealthDBStub) Ping(context.Context) error { return s.pingErr }

func (s *systemHealthDBStub) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	s.queries = append(s.queries, query)
	if len(args) != 0 {
		return systemHealthRowStub{err: errors.New("平台级健康统计不应携带租户参数")}
	}
	if len(s.rows) == 0 {
		return systemHealthRowStub{err: errors.New("缺少测试查询结果")}
	}
	row := s.rows[0]
	s.rows = s.rows[1:]
	return row
}

type systemHealthRowStub struct {
	values []int64
	err    error
}

func (r systemHealthRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("健康统计扫描列数不匹配")
	}
	for i, value := range r.values {
		ptr, ok := dest[i].(*int64)
		if !ok {
			return errors.New("健康统计扫描类型不匹配")
		}
		*ptr = value
	}
	return nil
}

func TestSystemNodeRoutesFailClosedWithoutAdminResolver(t *testing.T) {
	router := chi.NewRouter()
	mountSystemHealthRoutes(router, &deps{serverMonitorOffline: time.Minute})
	for _, path := range []string{
		"/v1/admin/system/nodes",
		"/v1/admin/system/nodes/node-test-01",
		"/v1/admin/system/nodes/node-test-01/history",
		"/admin/v1/system/nodes",
		"/admin/v1/system/nodes/node-test-01",
		"/admin/v1/system/nodes/node-test-01/history",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "admin_gate_not_configured") {
			t.Fatalf("GET %s status=%d body=%s，服务器监测路由必须经过 adminGate", path, rec.Code, rec.Body.String())
		}
	}
}

func TestBuildSystemHealthSourceWiresDatabaseAndServerMonitor(t *testing.T) {
	monitorStore := servermonitor.NewPostgresStore(nil)
	source, ok := buildSystemHealthSource(&deps{
		serverMonitorStore:   monitorStore,
		serverMonitorEnabled: true,
		serverMonitorOffline: 90 * time.Second,
	}).(*gatewaySystemHealthSource)
	if !ok {
		t.Fatalf("source type=%T", source)
	}
	if source.db != nil {
		t.Fatal("nil 数据库连接池不能伪装成可用的健康数据源")
	}
	if source.monitorStore != monitorStore || !source.monitorEnabled || source.monitorOffline != 90*time.Second {
		t.Fatalf("服务器监测依赖未完整注入: %+v", source)
	}
}

func TestGatewaySystemHealthSourceUsesExactGlobalCounts(t *testing.T) {
	db := &systemHealthDBStub{rows: []pgx.Row{
		systemHealthRowStub{values: []int64{12, 4}},
		systemHealthRowStub{values: []int64{1203}},
		systemHealthRowStub{values: []int64{7}},
	}}
	source := &gatewaySystemHealthSource{db: db}

	total, unhealthy, err := source.ChannelHealthSummary(context.Background())
	if err != nil || total != 12 || unhealthy != 4 {
		t.Fatalf("渠道健康统计=(%d,%d,%v)，want (12,4,nil)", total, unhealthy, err)
	}
	depth, err := source.DLQPendingDepth(context.Background())
	if err != nil || depth != 1203 {
		t.Fatalf("DLQ 深度=(%d,%v)，want (1203,nil)，不得被列表分页截断为 200", depth, err)
	}
	firing, err := source.AlertingFiringCount(context.Background())
	if err != nil || firing != 7 {
		t.Fatalf("触发中告警数=(%d,%v)，want (7,nil)", firing, err)
	}

	joined := strings.Join(db.queries, "\n")
	for _, table := range []string{"channel_health_state", "usage_record_dlq", "alert_events"} {
		if !strings.Contains(joined, table) {
			t.Fatalf("平台健康统计缺少 %s 的全局查询: %s", table, joined)
		}
	}
	if strings.Contains(joined, "tenant_id") {
		t.Fatalf("平台级聚合不应使用魔法 tenant_id=0: %s", joined)
	}
}

func TestGatewaySystemHealthSourceFailsClosedWithoutDatabase(t *testing.T) {
	source := &gatewaySystemHealthSource{}
	if err := source.DBPing(context.Background()); err == nil {
		t.Fatal("缺少数据库时 DBPing 不得伪装健康")
	}
	if _, _, err := source.ChannelHealthSummary(context.Background()); err == nil {
		t.Fatal("缺少数据库时渠道健康统计不得伪装为空")
	}
	if _, err := source.DLQPendingDepth(context.Background()); err == nil {
		t.Fatal("缺少数据库时 DLQ 深度不得伪装为 0")
	}
	if _, err := source.AlertingFiringCount(context.Background()); err == nil {
		t.Fatal("缺少数据库时告警数不得伪装为 0")
	}
}
