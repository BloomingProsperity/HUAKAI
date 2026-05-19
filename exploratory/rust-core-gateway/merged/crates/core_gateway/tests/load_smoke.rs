// M-rust-10 负载烟雾测试
// 覆盖: 100 / 500 / 1000 并发流式请求下 listener 不 panic / 不死锁;
//       收集 P50 / P95 / P99 延迟 (hdrhistogram); 写报告到 /tmp/huakai_rust_load_smoke.json。
// 运行: cargo test --test load_smoke -- --nocapture

mod common;

use std::{
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
    time::{Duration, Instant},
};

use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{build_router, config::StartupConfig, mock_control_plane::MockControlPlane};
use hdrhistogram::Histogram;
use serde_json::{Value, json};
use tokio::{net::TcpListener, sync::Mutex, task::JoinHandle};

// ─── 测试辅助 ─────────────────────────────────────────────────────────────────

/// 用端口 0 启动完整 axum 服务, 返回 (地址, server JoinHandle)
async fn spawn_server(
    mock_cp_endpoint: String,
    mock_upstream_endpoint: String,
) -> (std::net::SocketAddr, JoinHandle<()>) {
    let config = StartupConfig::from_env_iter(vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        ("HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(), mock_cp_endpoint),
        ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
        (
            "HUAKAI_MOCK_UPSTREAM_ENDPOINT".to_owned(),
            mock_upstream_endpoint,
        ),
        ("HUAKAI_LOG_LEVEL".to_owned(), "warn".to_owned()),
        ("HUAKAI_JSON_LOGS".to_owned(), "false".to_owned()),
        ("HUAKAI_WORKER_THREADS".to_owned(), "4".to_owned()),
    ])
    .expect("load smoke config 解析应成功");

    let listener = TcpListener::bind(config.listen_addr)
        .await
        .expect("server bind 应成功");
    let addr = listener.local_addr().expect("server addr 应存在");
    let router = build_router(config).expect("build_router");

    let handle = tokio::spawn(async move {
        let _ = axum::serve(listener, router).await;
    });

    // 等待 server 就绪
    tokio::time::sleep(Duration::from_millis(30)).await;
    (addr, handle)
}

/// 构造单次 SSE mock 响应 (5 帧)
fn sse_chunks() -> Vec<Bytes> {
    vec![
        Bytes::from_static(
            b"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"A\"}}\n\n",
        ),
        Bytes::from_static(
            b"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"B\"}}\n\n",
        ),
        Bytes::from_static(
            b"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"C\"}}\n\n",
        ),
        Bytes::from_static(
            b"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n",
        ),
        Bytes::from_static(b"data: [DONE]\n\n"),
    ]
}

/// 执行单次 HTTP POST 到 /v1/messages 并计时 (ms), 返回 Ok(elapsed_ms) 或 Err
async fn fire_request(client: &reqwest::Client, addr: std::net::SocketAddr) -> Result<u64, ()> {
    let body = json!({
        "model": "claude-mock",
        "max_tokens": 16,
        "stream": true,
        "messages": [{"role": "user", "content": "hi"}]
    });

    let start = Instant::now();
    let resp = client
        .post(format!("http://{addr}/v1/messages"))
        .header("x-tenant-id", "tenant-load-test")
        .header("content-type", "application/json")
        .json(&body)
        .timeout(Duration::from_secs(10))
        .send()
        .await
        .map_err(|_| ())?;

    // 消费完整响应体 (含 SSE 流)
    let _ = resp.bytes().await.map_err(|_| ())?;
    let elapsed = start.elapsed().as_millis() as u64;
    Ok(elapsed)
}

// ─── 负载运行逻辑 ─────────────────────────────────────────────────────────────

struct LoadResult {
    concurrency: usize,
    total: usize,
    success: usize,
    /// P50 / P95 / P99 单位 ms
    p50_ms: u64,
    p95_ms: u64,
    p99_ms: u64,
    max_ms: u64,
    elapsed_total_ms: u64,
}

async fn run_load_level(concurrency: usize, server_addr: std::net::SocketAddr) -> LoadResult {
    // 每个并发 worker 发 1 个请求 (烟雾测试不追求吞吐量, 只验证并发安全)
    let total = concurrency;

    let success_count = Arc::new(AtomicUsize::new(0));
    let latency_data: Arc<Mutex<Vec<u64>>> = Arc::new(Mutex::new(Vec::with_capacity(total)));
    let wall_start = Instant::now();

    let mut handles = Vec::with_capacity(total);
    let client = Arc::new(
        reqwest::Client::builder()
            .pool_max_idle_per_host(concurrency + 10)
            .timeout(Duration::from_secs(15))
            .build()
            .expect("reqwest client 构建应成功"),
    );

    for _ in 0..total {
        let client = client.clone();
        let sc = success_count.clone();
        let ld = latency_data.clone();
        handles.push(tokio::spawn(async move {
            if let Ok(ms) = fire_request(&client, server_addr).await {
                sc.fetch_add(1, Ordering::Relaxed);
                ld.lock().await.push(ms);
            }
        }));
    }

    // 等待所有请求完成 (最多 30s)
    let _ = tokio::time::timeout(
        Duration::from_secs(30),
        futures_util::future::join_all(handles),
    )
    .await;

    let elapsed_total_ms = wall_start.elapsed().as_millis() as u64;
    let success = success_count.load(Ordering::SeqCst);

    // 计算 hdrhistogram 百分位
    let data = latency_data.lock().await;
    let mut hist = Histogram::<u64>::new_with_bounds(1, 60_000, 3).expect("histogram 参数应有效");
    for &ms in data.iter() {
        let _ = hist.record(ms.max(1));
    }

    let p50_ms = hist.value_at_quantile(0.50);
    let p95_ms = hist.value_at_quantile(0.95);
    let p99_ms = hist.value_at_quantile(0.99);
    let max_ms = hist.max();

    LoadResult {
        concurrency,
        total,
        success,
        p50_ms,
        p95_ms,
        p99_ms,
        max_ms,
        elapsed_total_ms,
    }
}

// ─── 测试: 100 并发 ───────────────────────────────────────────────────────────

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn load_smoke_100_concurrent() {
    let mock_cp = MockControlPlane::spawn(core_gateway::mock_control_plane::mock_route_plan(
        "http://placeholder",
    ))
    .await;
    let mock_up = MockUpstream::spawn(MockBehavior::Sse {
        chunks: sse_chunks(),
        delay: Duration::ZERO,
    })
    .await;
    let (addr, server) = spawn_server(mock_cp.endpoint(), mock_up.endpoint()).await;

    let result = run_load_level(100, addr).await;

    eprintln!(
        "[load_smoke_100] success={}/{} p50={}ms p95={}ms p99={}ms max={}ms wall={}ms",
        result.success,
        result.total,
        result.p50_ms,
        result.p95_ms,
        result.p99_ms,
        result.max_ms,
        result.elapsed_total_ms,
    );

    // 验收: 至少 90% 请求成功 (网络端口竞争下留余量)
    assert!(
        result.success >= result.total * 90 / 100,
        "100 并发: 成功率应 >= 90% (实际 {}/{})",
        result.success,
        result.total,
    );

    server.abort();
    drop(mock_cp);
    drop(mock_up);
}

// ─── 测试: 500 并发 ───────────────────────────────────────────────────────────

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn load_smoke_500_concurrent() {
    let mock_cp = MockControlPlane::spawn(core_gateway::mock_control_plane::mock_route_plan(
        "http://placeholder",
    ))
    .await;
    let mock_up = MockUpstream::spawn(MockBehavior::Sse {
        chunks: sse_chunks(),
        delay: Duration::ZERO,
    })
    .await;
    let (addr, server) = spawn_server(mock_cp.endpoint(), mock_up.endpoint()).await;

    let result = run_load_level(500, addr).await;

    eprintln!(
        "[load_smoke_500] success={}/{} p50={}ms p95={}ms p99={}ms max={}ms wall={}ms",
        result.success,
        result.total,
        result.p50_ms,
        result.p95_ms,
        result.p99_ms,
        result.max_ms,
        result.elapsed_total_ms,
    );

    // 验收: 至少 85% 请求成功
    assert!(
        result.success >= result.total * 85 / 100,
        "500 并发: 成功率应 >= 85% (实际 {}/{})",
        result.success,
        result.total,
    );

    server.abort();
    drop(mock_cp);
    drop(mock_up);
}

// ─── 测试: 1000 并发 ──────────────────────────────────────────────────────────

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn load_smoke_1000_concurrent() {
    let mock_cp = MockControlPlane::spawn(core_gateway::mock_control_plane::mock_route_plan(
        "http://placeholder",
    ))
    .await;
    let mock_up = MockUpstream::spawn(MockBehavior::Sse {
        chunks: sse_chunks(),
        delay: Duration::ZERO,
    })
    .await;
    let (addr, server) = spawn_server(mock_cp.endpoint(), mock_up.endpoint()).await;

    let result = run_load_level(1000, addr).await;

    eprintln!(
        "[load_smoke_1000] success={}/{} p50={}ms p95={}ms p99={}ms max={}ms wall={}ms",
        result.success,
        result.total,
        result.p50_ms,
        result.p95_ms,
        result.p99_ms,
        result.max_ms,
        result.elapsed_total_ms,
    );

    // 验收: 至少 80% 请求成功 (沙箱环境 fd 限制下留充足余量)
    assert!(
        result.success >= result.total * 80 / 100,
        "1000 并发: 成功率应 >= 80% (实际 {}/{})",
        result.success,
        result.total,
    );

    server.abort();
    drop(mock_cp);
    drop(mock_up);
}

// ─── 测试: 写 JSON 报告 ───────────────────────────────────────────────────────

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn load_smoke_write_report() {
    // 用较小的 200 并发生成报告, 避免 CI 超时
    let mock_cp = MockControlPlane::spawn(core_gateway::mock_control_plane::mock_route_plan(
        "http://placeholder",
    ))
    .await;
    let mock_up = MockUpstream::spawn(MockBehavior::Sse {
        chunks: sse_chunks(),
        delay: Duration::ZERO,
    })
    .await;
    let (addr, server) = spawn_server(mock_cp.endpoint(), mock_up.endpoint()).await;

    // 跑三个层级并收集结果
    let r100 = run_load_level(100, addr).await;
    let r500 = run_load_level(500, addr).await;
    let r1000 = run_load_level(1000, addr).await;

    let report = json!({
        "harness": "huakai_rust_load_smoke",
        "version": "M-rust-10",
        "timestamp": chrono_now_utc(),
        "levels": [
            level_json(&r100),
            level_json(&r500),
            level_json(&r1000),
        ]
    });

    let path = "/tmp/huakai_rust_load_smoke.json";
    std::fs::write(path, serde_json::to_string_pretty(&report).unwrap())
        .expect("写 load smoke 报告应成功");

    eprintln!("[load_smoke_report] 报告已写入 {path}");

    // 验证文件存在且可解析
    let content = std::fs::read_to_string(path).expect("读取报告应成功");
    let parsed: Value = serde_json::from_str(&content).expect("报告应为合法 JSON");
    assert_eq!(parsed["harness"], "huakai_rust_load_smoke");

    server.abort();
    drop(mock_cp);
    drop(mock_up);
}

// ─── 测试: 无 panic / 无死锁基准 ─────────────────────────────────────────────

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn load_smoke_no_panic_no_deadlock() {
    // 用 Error5xx 行为验证 listener 在上游持续报错时不崩溃
    let mock_cp = MockControlPlane::spawn(core_gateway::mock_control_plane::mock_route_plan(
        "http://placeholder",
    ))
    .await;
    let mock_up = MockUpstream::spawn(MockBehavior::Error5xx).await;
    let (addr, server) = spawn_server(mock_cp.endpoint(), mock_up.endpoint()).await;

    // 发 50 个并发错误请求, 任何 panic 都会让 tokio runtime crash
    let client = Arc::new(
        reqwest::Client::builder()
            .timeout(Duration::from_secs(5))
            .build()
            .unwrap(),
    );
    let mut handles = Vec::with_capacity(50);
    for _ in 0..50 {
        let client = client.clone();
        handles.push(tokio::spawn(async move {
            let _ = fire_request(&client, addr).await;
        }));
    }
    let _ = tokio::time::timeout(
        Duration::from_secs(15),
        futures_util::future::join_all(handles),
    )
    .await;

    // server 应仍然存活 (没有 panic 的情况下 handle 不会 finished)
    assert!(!server.is_finished(), "server 应在 Error5xx 负载下仍存活");

    server.abort();
    drop(mock_cp);
    drop(mock_up);
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

fn level_json(r: &LoadResult) -> Value {
    json!({
        "concurrency": r.concurrency,
        "total_requests": r.total,
        "success": r.success,
        "success_rate_pct": r.success * 100 / r.total.max(1),
        "p50_ms": r.p50_ms,
        "p95_ms": r.p95_ms,
        "p99_ms": r.p99_ms,
        "max_ms": r.max_ms,
        "wall_ms": r.elapsed_total_ms,
    })
}

fn chrono_now_utc() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    format!("unix_epoch_s:{secs}")
}
