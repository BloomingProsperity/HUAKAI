package healthhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

const defaultReadinessTimeout = 3 * time.Second

// ReadinessCheck 描述一个必须可用的运行时依赖。
type ReadinessCheck struct {
	Name string
	Run  func(context.Context) error
}

// Readiness 管理启动完成、依赖健康与关停摘流量三个状态。
type Readiness struct {
	started  atomic.Bool
	draining atomic.Bool
	timeout  time.Duration
	checks   []ReadinessCheck
}

func NewReadiness(checks ...ReadinessCheck) *Readiness {
	return &Readiness{
		timeout: defaultReadinessTimeout,
		checks:  append([]ReadinessCheck(nil), checks...),
	}
}

// MarkReady 只在完整依赖树和 worker 都构造成功后调用。
func (r *Readiness) MarkReady() {
	if r != nil && !r.draining.Load() {
		r.started.Store(true)
	}
}

// BeginDrain 在 HTTP Shutdown 前调用，让负载均衡先停止分发新请求。
func (r *Readiness) BeginDrain() {
	if r == nil {
		return
	}
	r.draining.Store(true)
	r.started.Store(false)
}

func (r *Readiness) IsDraining() bool {
	return r != nil && r.draining.Load()
}

// NewReadinessHandler 返回运行时就绪处理器。公开响应只暴露检查名和结果，
// 不回显数据库地址、socket 路径或底层错误。
func NewReadinessHandler(readiness *Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		status, body := readiness.evaluate(req.Context())
		w.WriteHeader(status)
		if req.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (r *Readiness) evaluate(parent context.Context) (int, map[string]any) {
	if r == nil {
		return http.StatusServiceUnavailable, map[string]any{"status": "not_ready"}
	}
	if r.draining.Load() {
		return http.StatusServiceUnavailable, map[string]any{"status": "draining"}
	}
	if !r.started.Load() {
		return http.StatusServiceUnavailable, map[string]any{"status": "not_ready"}
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	checks := make(map[string]string, len(r.checks))
	type checkResult struct {
		name string
		err  error
	}
	results := make(chan checkResult, len(r.checks))
	seen := make(map[string]struct{}, len(r.checks))
	ok := true
	for _, check := range r.checks {
		if check.Name == "" || check.Run == nil {
			ok = false
			continue
		}
		if _, exists := seen[check.Name]; exists {
			ok = false
			continue
		}
		seen[check.Name] = struct{}{}
		checks[check.Name] = "failed"
		go func(check ReadinessCheck) {
			results <- checkResult{name: check.Name, err: check.Run(ctx)}
		}(check)
	}
	for range seen {
		select {
		case result := <-results:
			if result.err != nil {
				ok = false
				continue
			}
			checks[result.name] = "ok"
		case <-ctx.Done():
			return http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "checks": checks}
		}
	}
	if !ok {
		return http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "checks": checks}
	}
	return http.StatusOK, map[string]any{"status": "ok", "checks": checks}
}
