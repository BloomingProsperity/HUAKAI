# Python Offline Analytics 子项目 — PLAN

## 1. 元信息

- 日期: 2026-05-09
- Lane: sonnet planner — Python 探索子项目
- 路径: `exploratory/python-offline-analytics/`
- 性质: **探索性子目录**, 暂不接入 HUAKAI 主线 runtime; 不进 Go module, 不被 backend import, 不部署到生产
- Owner 战略 directive (2026-05-09): split-plane 架构补充 Python 子项目, 用于离线分析 / 压测报告 / 数据工具
- 与之并列: `exploratory/rust-core-gateway/` (独立 lane, 本 plan 不涉及)
- 项目主线索引 (只读输入源):
  - `backend/internal/pool/pasr_metrics.go` — `pasr` expvar map (8 keys)
  - `backend/internal/pool/pasr_dispatch_metrics.go` — `pasr_dispatch` expvar map (16 keys)
  - `backend/internal/cachemetrics/` — `cache_token_count` per-account vendor cache 命中量
  - `backend/cmd/gateway/main.go:198` — `/debug/vars` HTTP 暴露端点
  - PG `accounts` / `account_slots` / `request_audit` / `cache_segments_audit` 表 (经 dump 离线读)

## 2. 目标 / 非目标

### 目标 (in scope)

1. **离线分析**: 解析 `/debug/vars` 抓取 dump 的 `pasr` / `pasr_dispatch` 子树, 算 `shadow_match_ratio`、`canary_diff_ratio`、`cache_hit_ratio`、`segment_count_evolution`, 输出可读 markdown / CSV 报告
2. **压测报告**: 围绕 k6 / locust 压测脚本生成结构化结果 (P50/P95/P99、QPS、错误码分布), 与同窗口 `pasr_dispatch` 快照对照, 让 Owner 看到 "PASR 切到 30% canary 时 P99 是否退步"
3. **数据工具**:
   - PG dump 转化 (account 表 + slot 表 + audit 表) → DataFrame
   - vendor 端 prompt cache 命中率 (`cache_token_count`) vs PASR 段命中率 (`pasr_first_pick_total / total`) 对比, 算 cost saving 估算
   - 真实流量 SSE log → mock 上游回放, 离线验证 PASR cache locality (是否同 prompt 真的路由到同 account)
   - 段表快照 (5min ticker 落盘的 JSON) 时间序列可视化, 看 LRU evict 节奏 + cache_seen bitmap 分布

### 非目标 (out of scope)

- **不接入热路径**: 不写运行时代码, 不被 Go backend 调, 不暴露 HTTP service
- **不改 PG schema**: 仅消费已有表 dump, 不发起 DDL
- **不写 web 服务**: 输出静态 markdown / CSV / 静态 HTML, 不起 Flask/FastAPI 长进程
- **不做训练 / ML**: 暂不引入 sklearn/torch, 仅描述性统计 + 可视化
- **不写实际压测脚本**: M-py-2 仅搭框架与报告 schema, 实际 k6/locust 脚本由后续 atom 单独立项
- **不连主项目 import path**: 独立 Python 包, `pyproject.toml` 自治, 不与 backend Go module 交叉

## 3. 模块拆分

```
exploratory/python-offline-analytics/
├── pyproject.toml
├── README.md
├── PLAN.md  (本文件)
├── src/huakai_offline/
│   ├── __init__.py
│   ├── pasr_metrics_parser/         # M-py-1
│   ├── load_test_runner/            # M-py-2
│   ├── traffic_replayer/            # M-py-3
│   ├── cache_economics_analyzer/    # M-py-4
│   ├── segment_evolution_visualizer/# M-py-5
│   ├── common/                      # 通用: pydantic schema、redact、IO helper
│   └── cli/                         # uv run 入口, argparse / typer 子命令
├── tests/
│   └── (与 src 镜像)
├── reports/                         # 生成产物: *.md / *.csv / *.html (gitignore)
└── fixtures/                        # 脱敏后的小样本 expvar / k6 JSON / SSE log
```

### 3.1 `pasr_metrics_parser` — expvar 解析器

- 输入: `/debug/vars` JSON snapshot (单点 dump 或时间序列目录, 文件名带 epoch 戳)
- 解析 `pasr` 子树 8 个 key + `pasr_dispatch` 子树 16 个 key, 映射到 pydantic model `PASRSnapshot` / `PASRDispatchSnapshot` (字段名与 Go 端 Snapshot struct 一一对应, 漂移即测试红)
- 计算派生比率:
  - `shadow_match_ratio = ShadowMatch / max(ShadowSampled, 1)`
  - `shadow_diff_ratio = ShadowDiff / max(ShadowSampled, 1)`
  - `shadow_failure_ratio = (ShadowDrop + ShadowPanic + ShadowTimeout + ShadowPASRErr) / max(ShadowSampled, 1)`
  - `canary_pasr_share = CanaryPASRUsed / (CanaryPASRUsed + CanaryDefaultUsed)`
  - `cache_hit_ratio = FirstPickTotal / (FirstPickTotal + FailoverTotal + FullRingFallback)`
  - `cold_miss_rate = SegmentCreatesTotal / total_request_estimate`
- 输出: 单 dump → markdown 摘要; 多 dump → 时间序列 CSV + matplotlib PNG

### 3.2 `load_test_runner` — 压测报告框架

- **本 atom 不写实际 k6 / locust 脚本**, 仅约定:
  - 报告 schema (pydantic): `LoadTestRun(timestamp, scenario, qps, p50, p95, p99, error_codes, pasr_snapshot_before, pasr_snapshot_after)`
  - JSON 输入约定 (k6 `--summary-export` 格式 + locust `--csv` 格式适配器各一个)
  - 报告生成: 把同窗口 `pasr_dispatch` 快照拼到 LoadTestRun 里, 输出 markdown 表格 (含 mode 分布 + diff/sampled 比率)
- 后续 atom 再写实际 k6 scenario 脚本 (cache locality / failover / canary ramp 三类)

### 3.3 `traffic_replayer` — 流量回放器

- 输入: 真实 anthropic / openai SSE 请求 log (`backend/internal/audit/` 落盘的 jsonl 或 PG `request_audit` 表 dump)
- 强制 redact 步骤 (见风险 8.1):
  - 抹掉 `messages[].content` 全部 prompt 文本 (只留 token 数 + hash)
  - 抹掉 `Authorization` / `x-api-key` / `Bearer` / cookie / `huakai_client_id`
  - 抹掉 user prompt / system prompt 全文 — 替换为 `<REDACTED prompt N tokens hash=…>`
- 输出: 脱敏 jsonl + 可回放给 mock 上游 (httpserver fixture) 的 trace, 离线验证 "同 prompt hash 是否真路由到同 account"
- 关键校验: PASR cache locality 实测命中率 (回放 N 次, 看 first_pick_total 是否单调增、segment_count 是否在预期 LRU 范围)

### 3.4 `cache_economics_analyzer` — 缓存经济分析器

- 输入:
  - vendor 端 cache 命中: PG `cachemetrics` 落盘表 / expvar `cache_token_count` 时间序列
  - PASR 段命中: `pasr_first_pick_total` 时间序列
  - 价目: 硬编码 vendor pricing yaml (Anthropic cache_read_input_tokens 是 cache_creation 的 ~10%; 走 cache 比 fresh 省 ~90%)
- 计算:
  - vendor cache 命中率 = `cache_read_tokens / (cache_read + cache_creation + uncached_input)`
  - PASR 段命中率 = `first_pick / (first_pick + failover + full_ring)` (Track P 段路由命中)
  - 估算 cost saving = `cache_read_tokens * (input_price - cache_read_price) + first_pick_routed_to_warm_account * vendor_cache_amplification`
  - 输出 cost-by-account / cost-by-vendor / cost-by-day 分组 markdown 表
- **注意**: 不接 stripe / billing core, 估算结果仅供 Owner 评估算法收益, 不写回主库

### 3.5 `segment_evolution_visualizer` — 段表演化可视化

- 输入: `cache_segments_audit` 表 5min 快照, 每行 `(timestamp, segment_id, account_id, last_used_ts, cache_seen_bitmap, evict_reason)`
- 主项目侧前置依赖 (本 plan **不实施**, 仅记入 7.M-py-5): backend 需要每 5min 把段表落盘到 PG (LRU evict 已有计数, 但 segment 内容/bitmap 未落盘)
- 替代 fallback: 从 `pasr_evictions_total` + `pasr_segment_count` + `pasr_segment_creates_total` 三计数器 + 时间序列, 可推断 "段表大小演化" 但拿不到 bitmap 内部分布
- 输出: matplotlib (或 plotly) 静态 HTML 时间轴 — 段表大小、evict 速率、cache_seen 位图热度 (若可得)

## 4. 技术栈

| 选型 | 决定 | 理由 |
|------|------|------|
| Python | 3.12+ | match-case、TaskGroup、PEP 695 type alias |
| 包管理 | `uv` | rust 实现, 比 poetry/pip 快 10x; lockfile 友好 |
| 数据 | `pandas` 2.x + `pyarrow` | parquet IO 快; 大 dump 内存友好 |
| 校验 | `pydantic` v2 | expvar JSON → typed snapshot 防漂移 |
| 可视化 | `matplotlib` + `plotly` | static PNG (md 嵌入) + interactive HTML (Owner 本机看) |
| 测试 | `pytest` + `pytest-cov` | 与 Go 端 t.Run 子表风格对齐 |
| Lint | `ruff` + `pyright`(basic) | ruff 是 rust 实现 fast; pyright 比 mypy 增量友好 |
| CLI | `typer` | 比 argparse 简洁, 自动 help |
| PG dump 读 | 离线 `psycopg[binary]` 或直接读 `pg_dump --format=custom` | 不用 ORM, SQL 直读 |

## 5. 与主项目接口

### 输入数据来源 (只读, 离线)

1. **expvar dump**: `curl http://gateway:8080/debug/vars > snapshot_<ts>.json` (运维侧脚本, 5min/次), Python 侧只读静态文件
2. **PG dump**: `pg_dump --table=accounts --table=account_slots --table=request_audit --table=cache_segments_audit --format=custom` → 离线 restore 到本地 dev PG, 或直接 `psql -c "COPY ... TO STDOUT"` 抓 CSV
3. **SSE log**: backend audit 模块落盘的 jsonl, 经 redact 后再用
4. **k6 / locust 报告**: 运维侧压测产物 JSON / CSV

### 输出产物

- `reports/*.md` — 人读, 嵌入 PNG 图表
- `reports/*.csv` — 长期对比 / Owner 自己拉表
- `reports/*.html` — plotly 交互式 (本机打开看, 不部署 web)
- `reports/cost_estimate_*.json` — 结构化 cost saving 估算给 Owner 决策用

### 与主项目零运行时耦合

- 不 import Go 代码, 不写 Go FFI
- 不被 backend/`go run` 调
- 不写 PG (`psycopg` 全程只读 SELECT, 测试 fixture 用本地 SQLite 也行)
- key 字段名一致性靠 **快照测试** 守 — 见 M-py-1 验收

## 6. Milestones

每 atom 范围 ≤ 200 LoC + tests ≤ 200 LoC, 估时按 1 人 sonnet+codex 并行 lane。

### M-py-0 — Bootstrap (2h)

- 范围: `pyproject.toml` (uv) + `src/huakai_offline/__init__.py` + `tests/conftest.py` + `ruff.toml` + `.gitignore` (排除 `reports/`、`.venv/`、`fixtures/private/`)
- 依赖: 无
- 验收:
  - `uv sync && uv run pytest` 绿 (含 1 个 hello-world 测试)
  - `uv run ruff check .` 通过
  - `pyproject.toml` 声明 dependencies + dev-dependencies 分组

### M-py-1 — pasr_metrics_parser (1d)

- 范围:
  - `common/expvar_schema.py` — pydantic `PASRSnapshot` / `PASRDispatchSnapshot` (字段对齐 Go Snapshot struct)
  - `pasr_metrics_parser/parser.py` — 单 dump 解析 + 多 dump 时间序列解析
  - `pasr_metrics_parser/derived.py` — 派生比率 (shadow_match_ratio 等)
  - `pasr_metrics_parser/report.py` — markdown 报告生成
  - tests: 用 `fixtures/expvar_sample.json` (脱敏 fake snapshot 3 个时间点) 做 golden test
- 依赖: M-py-0
- 验收:
  - 解析 fixture → 派生比率精度 ≥ 4 位小数
  - 字段漂移测试: pydantic strict mode, fixture 多 / 少 key 都触发明确错误
  - markdown 报告 4 节 (overview / shadow / canary / cache), 命名锁定供下游引用

### M-py-2 — load_test_runner 框架 (0.5d)

- 范围:
  - `load_test_runner/schema.py` — `LoadTestRun` / `Scenario` / `LatencyBucket` pydantic
  - `load_test_runner/k6_adapter.py` — 解析 k6 `--summary-export` JSON
  - `load_test_runner/locust_adapter.py` — 解析 locust `--csv` 三件套
  - `load_test_runner/report.py` — 拼接 PASR 快照 + 压测结果, markdown 表
  - **不写**实际 k6 scenario 脚本
- 依赖: M-py-1 (引用 PASRSnapshot)
- 验收:
  - k6 / locust 各 1 fixture 跑通, markdown 表含 P50/P95/P99 + mode 分布列

### M-py-3 — traffic_replayer + redact (1d)

- 范围:
  - `common/redact.py` — 正则 + 字段白名单, 抹 `Authorization` / `Bearer` / `x-api-key` / `messages[].content` / `system` / `user` / `huakai_client_id`
  - `traffic_replayer/loader.py` — jsonl → typed event
  - `traffic_replayer/replayer.py` — 重放给 `httpserver` fixture mock 上游, 收集 PASR locality 指标
  - tests: 必含 redact 单元测试 (输入带 fake API key + fake prompt → 输出全 REDACTED)
- 依赖: M-py-1, M-py-0
- 验收:
  - redact 100% 命中 fixture 内人造敏感字段 (覆盖 ≥ 12 种 pattern)
  - 回放器输出 cache locality 报告 (相同 prompt hash 路由 account 一致率)

### M-py-4 — cache_economics_analyzer (1d)

- 范围:
  - `cache_economics_analyzer/pricing.yaml` — vendor cache pricing 静态表
  - `cache_economics_analyzer/joiner.py` — vendor cache 时间序列 + PASR 时间序列 时间对齐 (5min bucket)
  - `cache_economics_analyzer/estimator.py` — cost saving 估算
  - `cache_economics_analyzer/report.py` — by-vendor / by-account / by-day 分组 markdown
- 依赖: M-py-1
- 验收:
  - 单元测试: 已知 cache_read_tokens + first_pick_total → 估算 cost 在 ±5% 内
  - 报告输出含 "若 PASR 段命中提升 10%, 估省 $N/day" 的边际分析

### M-py-5 — segment_evolution_visualizer (1d)

- 范围:
  - `segment_evolution_visualizer/snapshot_loader.py` — 读 5min 段表快照 (PG 表 / JSON 文件双源)
  - `segment_evolution_visualizer/timeseries.py` — 段表大小、evict 速率、cache_seen 分布
  - `segment_evolution_visualizer/render.py` — plotly 静态 HTML 输出
- 依赖: M-py-1
- 验收:
  - 给 fixture (24h fake 段表, 288 个 5min 点), 输出 HTML 含 3 张图: count 演化 / evict rate / cache_seen heatmap
  - 文档说明 "若主项目尚未落盘段表内容, 仅 fallback 到三计数器路径"

### M-py-6 — CLI 集成 + README (0.5d)

- 范围:
  - `cli/main.py` — typer root + 5 子命令 (`parse-pasr` / `lt-report` / `replay` / `cache-econ` / `seg-evo`)
  - `README.md` — 快速上手 + 各子命令示例 + 数据隐私警告 (M-py-3 redact 是强制前置)
- 依赖: M-py-1..5
- 验收:
  - `uv run huakai-offline --help` 列 5 子命令
  - 每个子命令带 `--input` / `--output` / `--format` 标准三参

**累计估时**: 2h + 1d + 0.5d + 1d + 1d + 1d + 0.5d ≈ **5.25 人天** (sonnet+codex 并行 lane 可压到 3 天墙钟)

## 7. 风险登记 (≥ 4)

### R-py-1: 数据隐私 — 真实流量 log 含 user prompt / API key

- **后果**: 误把 prompt 或 token 写入 git / report → license / 合规事故
- **缓解**: M-py-3 redact 是强制前置; `fixtures/private/` 入 `.gitignore`; redact 单测覆盖 ≥ 12 种 pattern; CI 加 grep 拦截 `sk-`、`Bearer`、`x-api-key` 字面值进 `reports/`
- **残余**: redact 漏 pattern (如 vendor 新加 header) — 缓解办法: 每次添加 vendor support 时触发 redact regex review

### R-py-2: PG dump 大数据集内存溢出

- **后果**: 万人级 `request_audit` 表 dump 上 GB, pandas `read_csv` 直接 OOM
- **缓解**: 默认走 `pyarrow` 分批读 + `chunksize`; analyzer 用 streaming aggregation (不全表 in-mem); fixture 用小样本 (≤ 10MB)
- **残余**: 极端大表 (> 100GB) 仍需 DuckDB 兜底 — M-py-4 后置 atom

### R-py-3: expvar 格式漂移 — 主项目改了 metrics key

- **后果**: 主项目 PASR M3/M4 atom 加 / 改 key 名, Python parser 静默错位
- **缓解**:
  - pydantic strict mode + extra=forbid: 多 key 直接报错
  - golden fixture 提交时附 `from_commit_sha` (Go 主项目 commit), 测试启动校验 SHA 在 git log 中可见
  - M-py-1 验收强制: 字段名与 Go `PASRMetricsSnapshot` / `PASRDispatchSnapshot` struct 一致, 加新 key 同步更新

### R-py-4: Python 维护成本 — 单人项目兜不住

- **后果**: Owner / Claude / Codex 主线全在 Go, Python 代码无人维护
- **缓解**:
  - 严格 200 LoC / atom 上限, 模块物理隔离, 删 1 个不影响其他 4 个
  - 不引大型框架 (django / airflow), 仅 stdlib + pandas + pydantic 三层
  - CI 不强制 (本子项目独立 workflow, 失败不 block 主线 release)

### R-py-5: 估算精度误导 cost saving 决策

- **后果**: cache_economics_analyzer 输出的 cost saving 是粗估, Owner 误以为可写到 SLA / 对外宣传
- **缓解**:
  - 报告 header 强制带 "ESTIMATION ONLY — NOT BILLABLE" 水印
  - 估算公式与假设全部明文写在 markdown report 顶部
  - 估算误差区间 ±N% 一并输出

### R-py-6: 探索子项目走向腐烂 (无人 Owner)

- **后果**: 写完 5 个 atom 后无人续, 半年后 expvar 漂移 / pricing 过期, fixture 失真
- **缓解**:
  - 每 atom 写 README "last_validated_at" 日期 + 主项目对应 commit SHA
  - 定季度由 Owner 决定 archive / 续维护, 不维护则物理删 (子目录隔离让删除零代价)

## 8. 决策点 — 给 Owner

### 决策点 D-py-1: PG 读源选 psycopg vs CSV/parquet 中转

- **A**: `psycopg[binary]` 直连 dev PG (Owner 本机), 实时 SQL — 优点低延迟、可交互; 缺点要 PG 凭据
- **B**: 运维侧定期 `pg_dump --format=custom` 落盘 → Python 读 dump 文件 — 优点零凭据、可分发 fixture; 缺点不实时
- **C**: 双支持, M-py-1 先 B (dump), 后续按需加 A
- **建议默认 C** (探索子项目零凭据风险更低), Owner 决定是否需 A

### 决策点 D-py-2: 可视化层选 matplotlib vs plotly

- **A**: matplotlib only — PNG 嵌入 markdown, 体积小, CI 友好
- **B**: plotly only — interactive HTML, Owner 本机交互看, 但 HTML 体积大 (单文件 MB 级)
- **C**: 双支持, 主输出 markdown + PNG (matplotlib), 次输出 HTML (plotly), CLI flag 切换
- **建议默认 C**, 主项目 release-readiness gate 不依赖 plotly HTML

### 决策点 D-py-3: 是否在主项目侧加 5min 段表内容快照

- **背景**: M-py-5 想看 cache_seen bitmap 分布, 但当前主项目仅暴露段表大小, 不暴露段内容
- **A**: 主项目加表 + 5min ticker 落盘段表内容 — 信号最强但侵入主线
- **B**: 仅靠 `pasr_segment_count` + `pasr_evictions_total` + `pasr_segment_creates_total` 三计数推断 — 信号弱, 但零侵入
- **C**: M-py-5 走 B 路径先出, 若 Owner 评估发现盲点强烈再回头要 A
- **建议 C**, A 单列 backlog, 不阻塞本子项目

## 9. Out of Scope (不做的)

- ML / 训练 / embedding 计算 — 暂不引 sklearn / torch / transformers
- 实时流处理 — 不用 kafka / redis-stream, 全离线批
- web 服务 / API endpoint — 不起 Flask / FastAPI 长进程
- 接入 Prometheus / Grafana / OTel — 主项目当前 expvar 兜底, Python 侧也只读 expvar
- 写 PG / 改 schema / 触发 backend HTTP 调用
- 直接驱动 k6 / locust 进程 — 仅消费它们的 JSON / CSV 报告
- 真实压测脚本 (k6 scenario 代码) — 单列 atom, 不在本 plan
- 多语言 i18n — 报告默认中文 + 字段英文 (与主项目一致)
- 多用户协作 / RBAC / 审计 — 探索子项目, 假定单 Owner 本机使用

## 10. 文件路径回执

- 本 plan: `/home/codex/HUAKAI/exploratory/python-offline-analytics/PLAN.md`
- 后续 atom 落地路径: `/home/codex/HUAKAI/exploratory/python-offline-analytics/src/huakai_offline/<module>/`
- 报告产物 (gitignore): `/home/codex/HUAKAI/exploratory/python-offline-analytics/reports/`
