package healthhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessRequiresStartupAndAllDependencies(t *testing.T) {
	databaseOK := true
	sidecarOK := true
	readiness := NewReadiness(
		ReadinessCheck{Name: "database", Run: func(context.Context) error {
			if !databaseOK {
				return errors.New("数据库不可用")
			}
			return nil
		}},
		ReadinessCheck{Name: "tls_sidecar", Run: func(context.Context) error {
			if !sidecarOK {
				return errors.New("sidecar 不可用")
			}
			return nil
		}},
	)
	handler := NewReadinessHandler(readiness)

	before := httptest.NewRecorder()
	handler(before, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if before.Code != http.StatusServiceUnavailable || !strings.Contains(before.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("启动完成前 status/body=%d/%s", before.Code, before.Body.String())
	}

	readiness.MarkReady()
	healthy := httptest.NewRecorder()
	handler(healthy, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if healthy.Code != http.StatusOK || !strings.Contains(healthy.Body.String(), `"tls_sidecar":"ok"`) {
		t.Fatalf("依赖健康时 status/body=%d/%s", healthy.Code, healthy.Body.String())
	}

	sidecarOK = false
	unhealthy := httptest.NewRecorder()
	handler(unhealthy, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if unhealthy.Code != http.StatusServiceUnavailable || !strings.Contains(unhealthy.Body.String(), `"tls_sidecar":"failed"`) {
		t.Fatalf("sidecar 故障时 status/body=%d/%s", unhealthy.Code, unhealthy.Body.String())
	}
	if strings.Contains(unhealthy.Body.String(), "sidecar 不可用") {
		t.Fatalf("公开响应泄露底层错误：%s", unhealthy.Body.String())
	}

	sidecarOK = true
	databaseOK = false
	dbDown := httptest.NewRecorder()
	handler(dbDown, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if dbDown.Code != http.StatusServiceUnavailable || !strings.Contains(dbDown.Body.String(), `"database":"failed"`) {
		t.Fatalf("数据库故障时 status/body=%d/%s", dbDown.Code, dbDown.Body.String())
	}
}

func TestReadinessDrainAndHead(t *testing.T) {
	readiness := NewReadiness()
	readiness.MarkReady()
	handler := NewReadinessHandler(readiness)

	head := httptest.NewRecorder()
	handler(head, httptest.NewRequest(http.MethodHead, "/readyz", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD status/body=%d/%q", head.Code, head.Body.String())
	}

	readiness.BeginDrain()
	if !readiness.IsDraining() {
		t.Fatal("BeginDrain 未记录 draining 状态")
	}
	draining := httptest.NewRecorder()
	handler(draining, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if draining.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining status=%d want 503", draining.Code)
	}
}

func TestReadinessChecksDependenciesConcurrently(t *testing.T) {
	fastRan := make(chan struct{})
	readiness := NewReadiness(
		ReadinessCheck{Name: "database", Run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
		ReadinessCheck{Name: "tls_sidecar", Run: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			close(fastRan)
			return nil
		}},
	)
	readiness.timeout = 50 * time.Millisecond
	readiness.MarkReady()

	recorder := httptest.NewRecorder()
	NewReadinessHandler(readiness)(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503，单个依赖超时必须阻止就绪", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"database":"failed"`) || !strings.Contains(body, `"tls_sidecar":"ok"`) {
		t.Fatalf("并发检查结果=%s，慢数据库不能把健康 sidecar 误报为失败", body)
	}
	select {
	case <-fastRan:
	default:
		t.Fatal("快速依赖未在总截止时间内执行")
	}
}
