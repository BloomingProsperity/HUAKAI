// M-rust-9 observability — Prometheus 指标注册与暴露
// 命名空间: huakai_rust_* (与 Go 主线 pasr_dispatch 不重叠)
// 极致性能: lazy_static 懒初始化, 静态 bucket, static str label

use std::sync::OnceLock;

use prometheus::{
    Encoder, Histogram, HistogramOpts, IntCounter, IntCounterVec, IntGauge, Opts, Registry,
    TextEncoder,
};

// ─── 全局注册表 ────────────────────────────────────────────────────────────────

/// 独立注册表 (不使用 prometheus 默认全局注册表, 避免测试污染)
static REGISTRY: OnceLock<Registry> = OnceLock::new();

pub fn registry() -> &'static Registry {
    REGISTRY.get_or_init(|| {
        let r = Registry::new();
        register_all(&r);
        r
    })
}

// ─── 延迟毫秒 histogram bucket — 静态定义避免堆分配 ───────────────────────────

/// RPC 延迟 bucket (ms): 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000
static RPC_LATENCY_BUCKETS: &[f64] = &[
    0.5, 1.0, 2.0, 5.0, 10.0, 25.0, 50.0, 100.0, 250.0, 500.0, 1000.0, 2500.0, 5000.0,
];

/// 路由查询延迟 bucket (ms): 与 RPC 共用同一组
static ROUTE_LATENCY_BUCKETS: &[f64] = RPC_LATENCY_BUCKETS;

// ─── 指标句柄 — OnceLock 懒初始化 ─────────────────────────────────────────────

static RPC_LATENCY_MS: OnceLock<Histogram> = OnceLock::new();
static ROUTE_QUERY_LATENCY_MS: OnceLock<Histogram> = OnceLock::new();
static STREAM_FRAMES_IN: OnceLock<IntCounter> = OnceLock::new();
static STREAM_FRAMES_OUT: OnceLock<IntCounter> = OnceLock::new();
static CANCEL_COUNT: OnceLock<IntCounter> = OnceLock::new();
static UPSTREAM_ERROR_TOTAL: OnceLock<IntCounterVec> = OnceLock::new();
static ACTIVE_CONNECTIONS: OnceLock<IntGauge> = OnceLock::new();
static QUEUE_DEPTH: OnceLock<IntGauge> = OnceLock::new();
static OPEN_UPSTREAM_CONNECTIONS: OnceLock<IntGauge> = OnceLock::new();
static INFLIGHT_REQUESTS: OnceLock<IntGauge> = OnceLock::new();
static INFLIGHT_LIMIT: OnceLock<IntGauge> = OnceLock::new();

// W11-A D-1b Phase 2A.4 (D-15 Owner-approved, 2026-05-24): dual-write
// reconciliation outcome counter. Dimensions kept tight per B-R2:
//   kind   ∈ {bearer, x-api-key, none}        (credential kind, NOT raw token)
//   source ∈ {both_match, both_mismatch, manual_only, go_only, none}
// Total time-series at saturation: 3 * 5 = 15. NO tenant_id label
// (cardinality would unbound — each new tenant doubles the series count).
static CLIENT_CREDENTIAL_TENANT_RECONCILE_TOTAL: OnceLock<IntCounterVec> = OnceLock::new();

// ─── 公共访问器 ───────────────────────────────────────────────────────────────

/// RPC 端到端延迟 (ms)
pub fn rpc_latency_ms() -> &'static Histogram {
    RPC_LATENCY_MS.get().expect("metrics 未初始化")
}

/// 路由查询延迟 (ms)
pub fn route_query_latency_ms() -> &'static Histogram {
    ROUTE_QUERY_LATENCY_MS.get().expect("metrics 未初始化")
}

/// 流式帧入计数
pub fn stream_frames_in() -> &'static IntCounter {
    STREAM_FRAMES_IN.get().expect("metrics 未初始化")
}

/// 流式帧出计数
pub fn stream_frames_out() -> &'static IntCounter {
    STREAM_FRAMES_OUT.get().expect("metrics 未初始化")
}

/// 请求取消计数
pub fn cancel_count() -> &'static IntCounter {
    CANCEL_COUNT.get().expect("metrics 未初始化")
}

/// 上游错误计数 (按 vendor label 分区)
pub fn upstream_error_total() -> &'static IntCounterVec {
    UPSTREAM_ERROR_TOTAL.get().expect("metrics 未初始化")
}

/// 当前活跃连接数
pub fn active_connections() -> &'static IntGauge {
    ACTIVE_CONNECTIONS.get().expect("metrics 未初始化")
}

/// 当前队列深度
pub fn queue_depth() -> &'static IntGauge {
    QUEUE_DEPTH.get().expect("metrics 未初始化")
}

/// 当前打开的上游连接数
pub fn open_upstream_connections() -> &'static IntGauge {
    OPEN_UPSTREAM_CONNECTIONS.get().expect("metrics 未初始化")
}

/// 当前业务 in-flight 请求数
pub fn inflight_requests() -> &'static IntGauge {
    INFLIGHT_REQUESTS.get().expect("metrics 未初始化")
}

/// 当前业务 in-flight 请求上限; 0 表示未启用卸载
pub fn inflight_limit() -> &'static IntGauge {
    INFLIGHT_LIMIT.get().expect("metrics 未初始化")
}

/// 设置当前业务 in-flight 请求数
pub fn set_inflight_requests(value: i64) {
    let _ = registry();
    inflight_requests().set(value);
}

/// 设置当前业务 in-flight 请求上限
pub fn set_inflight_limit(value: i64) {
    let _ = registry();
    inflight_limit().set(value);
}

/// W11-A D-1b Phase 2A.4 (D-15 counter, 2026-05-24): dual-write reconciliation
/// outcomes between Manual First and Go control plane derived identity.
/// Labels: kind, source (per B-R2: no tenant_id label).
pub fn client_credential_tenant_reconcile_total() -> &'static IntCounterVec {
    CLIENT_CREDENTIAL_TENANT_RECONCILE_TOTAL
        .get()
        .expect("metrics 未初始化")
}

// ─── 文本格式序列化 (Prometheus scrape) ──────────────────────────────────────

/// 将注册表编码为 Prometheus 文本格式
pub fn encode_metrics() -> String {
    let encoder = TextEncoder::new();
    let metric_families = registry().gather();
    let mut buf = Vec::with_capacity(4096);
    encoder
        .encode(&metric_families, &mut buf)
        .expect("Prometheus 编码不应失败");
    String::from_utf8(buf).expect("Prometheus 输出应为合法 UTF-8")
}

// ─── 注册函数 (在 registry() 首次调用时执行一次) ──────────────────────────────

fn register_all(r: &Registry) {
    // rpc_latency_ms
    let h = Histogram::with_opts(
        HistogramOpts::new("huakai_rust_rpc_latency_ms", "端到端 RPC 延迟 (毫秒)")
            .buckets(RPC_LATENCY_BUCKETS.to_vec()),
    )
    .expect("histogram 创建应成功");
    r.register(Box::new(h.clone())).expect("注册应成功");
    let _ = RPC_LATENCY_MS.set(h);

    // route_query_latency_ms
    let h = Histogram::with_opts(
        HistogramOpts::new("huakai_rust_route_query_latency_ms", "路由查询延迟 (毫秒)")
            .buckets(ROUTE_LATENCY_BUCKETS.to_vec()),
    )
    .expect("histogram 创建应成功");
    r.register(Box::new(h.clone())).expect("注册应成功");
    let _ = ROUTE_QUERY_LATENCY_MS.set(h);

    // stream_frames_in
    let c = IntCounter::new("huakai_rust_stream_frames_in_total", "接收的流式帧总数")
        .expect("counter 创建应成功");
    r.register(Box::new(c.clone())).expect("注册应成功");
    let _ = STREAM_FRAMES_IN.set(c);

    // stream_frames_out
    let c = IntCounter::new("huakai_rust_stream_frames_out_total", "发出的流式帧总数")
        .expect("counter 创建应成功");
    r.register(Box::new(c.clone())).expect("注册应成功");
    let _ = STREAM_FRAMES_OUT.set(c);

    // cancel_count
    let c =
        IntCounter::new("huakai_rust_cancel_total", "请求取消总数").expect("counter 创建应成功");
    r.register(Box::new(c.clone())).expect("注册应成功");
    let _ = CANCEL_COUNT.set(c);

    // upstream_error_total (按 vendor 分区)
    let cv = IntCounterVec::new(
        Opts::new(
            "huakai_rust_upstream_error_total",
            "上游错误总数 (按 vendor)",
        ),
        &["vendor"],
    )
    .expect("counter_vec 创建应成功");
    r.register(Box::new(cv.clone())).expect("注册应成功");
    let _ = UPSTREAM_ERROR_TOTAL.set(cv);

    // active_connections
    let g = IntGauge::new("huakai_rust_active_connections", "当前活跃 HTTP 连接数")
        .expect("gauge 创建应成功");
    r.register(Box::new(g.clone())).expect("注册应成功");
    let _ = ACTIVE_CONNECTIONS.set(g);

    // queue_depth
    let g = IntGauge::new("huakai_rust_queue_depth", "当前请求队列深度").expect("gauge 创建应成功");
    r.register(Box::new(g.clone())).expect("注册应成功");
    let _ = QUEUE_DEPTH.set(g);

    // open_upstream_connections
    let g = IntGauge::new(
        "huakai_rust_open_upstream_connections",
        "当前打开的上游连接数",
    )
    .expect("gauge 创建应成功");
    r.register(Box::new(g.clone())).expect("注册应成功");
    let _ = OPEN_UPSTREAM_CONNECTIONS.set(g);

    // P4 in-flight requests
    let g = IntGauge::new("huakai_inflight_requests", "当前业务 in-flight 请求数")
        .expect("gauge 创建应成功");
    r.register(Box::new(g.clone())).expect("注册应成功");
    let _ = INFLIGHT_REQUESTS.set(g);

    // P4 in-flight limit
    let g = IntGauge::new(
        "huakai_inflight_limit",
        "业务 in-flight 请求上限; 0 表示未启用卸载",
    )
    .expect("gauge 创建应成功");
    r.register(Box::new(g.clone())).expect("注册应成功");
    let _ = INFLIGHT_LIMIT.set(g);

    // W11-A D-1b Phase 2A.4 (D-15 + B-R2, 2026-05-24): tenant reconciliation
    // counter — labels kind+source, NO tenant_id (cardinality防爆).
    let cv = IntCounterVec::new(
        Opts::new(
            "huakai_client_credential_tenant_reconcile_total",
            "Dual-write reconciliation outcomes between Manual First tenant and Go control plane derived tenant (W11-A D-1b Phase 2A.4)",
        ),
        &["kind", "source"],
    )
    .expect("counter_vec 创建应成功");
    r.register(Box::new(cv.clone())).expect("注册应成功");
    let _ = CLIENT_CREDENTIAL_TENANT_RECONCILE_TOTAL.set(cv);
}

// ─── 单元测试 ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn metrics_registry_initializes_once() {
        // 两次调用应返回同一注册表
        let r1 = registry() as *const Registry;
        let r2 = registry() as *const Registry;
        assert_eq!(r1, r2, "registry 应为单例");
    }

    #[test]
    fn encode_metrics_contains_huakai_rust_prefix() {
        // 触发初始化
        let _ = registry();
        let output = encode_metrics();
        assert!(
            output.contains("huakai_rust_"),
            "Prometheus 输出应含 huakai_rust_ 前缀, 实际:\n{output}"
        );
    }

    #[test]
    fn encode_metrics_contains_p4_inflight_gauges() {
        let _ = registry();
        let output = encode_metrics();

        assert!(
            output.contains("huakai_inflight_requests"),
            "P4 当前在途请求 gauge 必须始终导出, 实际:\n{output}"
        );
        assert!(
            output.contains("huakai_inflight_limit"),
            "P4 在途请求上限 gauge 必须始终导出, 实际:\n{output}"
        );
    }

    #[test]
    fn rpc_latency_histogram_records_observation() {
        let _ = registry();
        rpc_latency_ms().observe(42.0);
        // 验证 encode 不崩溃且含 metric 名称
        let output = encode_metrics();
        assert!(output.contains("huakai_rust_rpc_latency_ms"));
    }

    #[test]
    fn counters_increment_correctly() {
        let _ = registry();
        stream_frames_in().inc();
        stream_frames_out().inc_by(3);
        cancel_count().inc();
        upstream_error_total().with_label_values(&["openai"]).inc();
        let output = encode_metrics();
        assert!(output.contains("huakai_rust_stream_frames_in_total"));
        assert!(output.contains("huakai_rust_upstream_error_total"));
    }

    #[test]
    fn gauges_set_and_decrement() {
        let _ = registry();
        active_connections().set(5);
        active_connections().dec();
        queue_depth().inc();
        open_upstream_connections().set(10);
        inflight_requests().set(2);
        inflight_limit().set(8);
        let output = encode_metrics();
        assert!(output.contains("huakai_rust_active_connections"));
        assert!(output.contains("huakai_rust_queue_depth"));
        assert!(output.contains("huakai_rust_open_upstream_connections"));
        assert!(output.contains("huakai_inflight_requests"));
        assert!(output.contains("huakai_inflight_limit"));
    }
}
