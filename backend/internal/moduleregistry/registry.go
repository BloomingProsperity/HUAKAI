package moduleregistry

import (
	"context"
	"sort"
	"sync"
	"time"
)

// DefaultProbeTimeout 为 Snapshot 内每个健康探针设置上限。在此时间窗内未
// 返回的探针会被报告为 StatusUnknown(「无法确定」),而非被允许拖住
// 运维视图。
const DefaultProbeTimeout = 750 * time.Millisecond

// Registry 是线程安全的、进程内的模块知识主干。它在接线时构造一次,各模块
// 在运行时构建过程中注册进来。
type Registry struct {
	mu      sync.RWMutex
	byID    map[string]ModuleDescriptor
	timeout time.Duration
}

// New 返回一个使用 DefaultProbeTimeout 的空 Registry。它委托给
// NewWithProbeTimeout,使可配置的构造函数始终处于活跃路径上。
func New() *Registry {
	return NewWithProbeTimeout(DefaultProbeTimeout)
}

// NewWithProbeTimeout 返回一个带自定义每探针超时的空 Registry。非正的超时
// 会回退到 DefaultProbeTimeout。这是唯一的底层构造函数(New 用默认值包装
// 它),因此未来的接线可在不新增构造函数的情况下调节探针时间预算。
func NewWithProbeTimeout(d time.Duration) *Registry {
	if d <= 0 {
		d = DefaultProbeTimeout
	}
	return &Registry{
		byID:    make(map[string]ModuleDescriptor),
		timeout: d,
	}
}

// Register 添加(或替换)一个 descriptor。
//
// 重复 ID 策略:后者胜出(LAST-WINS),幂等。用同一 ID 重新注册会覆盖先前的
// descriptor 并返回 nil。这是刻意为之 —— 接线每进程只运行一次,而后者胜出的
// 契约可避免重跑 / 测试重新初始化时报错,同时仍保持确定性。空 ID 是唯一被
// 拒绝的输入(返回 ErrEmptyID),因为无法寻址的模块是编程错误,而非合法的
// 覆盖。
func (r *Registry) Register(d ModuleDescriptor) error {
	if d.ID == "" {
		return ErrEmptyID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[d.ID] = d
	return nil
}

// Get 返回 id 对应的 descriptor 以及是否找到。
func (r *Registry) Get(id string) (ModuleDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.byID[id]
	return d, ok
}

// List 返回按 ID 排序的所有 descriptor(稳定顺序,用于确定性的运维视图
// 和测试)。
func (r *Registry) List() []ModuleDescriptor {
	r.mu.RLock()
	out := make([]ModuleDescriptor, 0, len(r.byID))
	for _, d := range r.byID {
		out = append(out, d)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListByCategory 返回 Category 等于 cat 的 descriptor,按 ID 排序。
func (r *Registry) ListByCategory(cat string) []ModuleDescriptor {
	all := r.List()
	out := make([]ModuleDescriptor, 0, len(all))
	for _, d := range all {
		if d.Category == cat {
			out = append(out, d)
		}
	}
	return out
}

// ModuleSnapshot 是一个模块的 descriptor 与其实时探针结果的合并。
type ModuleSnapshot struct {
	Descriptor ModuleDescriptor `json:"descriptor"`
	Probe      ProbeResult      `json:"probe"`
}

// Snapshot 并发运行每个已注册模块的健康探针,受 registry 每探针超时的约束,
// 并返回按 ID 排序的合并视图。
//
// 保证:
//   - 没有探针的模块产出 StatusUnknown(没有探针 != 错误)。
//   - 超过超时的探针产出 StatusUnknown —— snapshot 不会等待它;该 goroutine
//     被放弃(它会观察到 ctx 取消),这样单个缓慢探针就无法拖住整个运维视图。
//   - panic 的探针会被恢复为 StatusError(一个坏探针不能让运维路径崩溃)。
//
// Snapshot 尊重调用方的 ctx:若 ctx 被取消,仍在进行中的探针会解析为
// StatusUnknown,函数随即迅速返回。
func (r *Registry) Snapshot(ctx context.Context) []ModuleSnapshot {
	descs := r.List()
	out := make([]ModuleSnapshot, len(descs))

	var wg sync.WaitGroup
	for i := range descs {
		d := descs[i]
		out[i] = ModuleSnapshot{Descriptor: d, Probe: ProbeResult{Status: StatusUnknown}}
		if d.HealthProbe == nil {
			continue // 没有探针 → 保持为 unknown
		}
		wg.Add(1)
		go func(idx int, probe HealthProbe) {
			defer wg.Done()
			out[idx].Probe = runProbe(ctx, probe, r.timeout)
		}(i, d.HealthProbe)
	}
	wg.Wait()
	return out
}

// runProbe 以每探针超时执行单个探针。若探针及时完成则返回其结果;若超时或
// ctx 先被取消则返回 StatusUnknown;若探针 panic 则返回 StatusError。探针在
// 自己的 goroutine 中运行,因此挂起的探针永远不会阻塞有界等待 —— 该
// goroutine 被留下,自行观察派生 ctx 的取消并退出。
func runProbe(parent context.Context, probe HealthProbe, timeout time.Duration) ProbeResult {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	resCh := make(chan ProbeResult, 1) // 带缓冲:迟到的探针发送永不阻塞
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				resCh <- ProbeResult{Status: StatusError, Detail: "probe panic"}
			}
		}()
		resCh <- probe(ctx)
	}()

	select {
	case res := <-resCh:
		if res.Status == "" {
			res.Status = StatusUnknown
		}
		return res
	case <-ctx.Done():
		// 超时(或父 ctx 被取消):我们不等待探针。报告 "unknown" ——
		// 我们无法确定 —— 绝不报 "error",也绝不挂起。
		return ProbeResult{Status: StatusUnknown, Detail: "probe timeout"}
	}
}
