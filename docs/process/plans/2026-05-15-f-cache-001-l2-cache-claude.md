# 2026-05-15 F-CACHE-001 简单 L2 响应缓存 (Claude 独立 plan)

| Lane | SPECIFIER (Claude); 平行 codex |
| Source | F-CACHE-001 row + memory project_pasr_real_diff_matrix (LiteLLM 已有 cache locality routing) |
| Agent | Claude Opus 4.7 (1M context) |
| UTC | 2026-05-15T13:54:00Z |

## scope (Go 还是 Rust?)

**推 Go 实现** (虽然技术上 Rust 数据面也能装,但):
- F-CACHE-001 spec 已 Released 在 Go backend 语境
- Tx2 settlement 已在 Go,cache hit 计费在 Go 更直接
- Rust 数据面 mainline 期间不引入新职责
- Owner 2026-05-15 临时解冻 Go,本 feature 在解冻范围

key 设计: `cache_key = sha256(vendor + model + canonical(request_body))`,canonical 后归一化(移除 metadata/timestamp 等)。

## file-by-file impact

- `backend/internal/cache/` (新建): 内存 LRU + 持久化层
- `backend/internal/cache/store.go`: 接口 Get/Set/Stats; backend 选 LRU+TTL OR Redis (D1)
- `backend/internal/cache/key.go`: vendor + model + body hash canonical
- `backend/internal/proxy/proxy.go` hot path: 入口加 cache lookup; cache hit → 直接返回 (跳过 upstream)
- `backend/internal/observability/cache_metrics.go`: Prometheus `huakai_cache_{hit,miss,size}_total{vendor,model}`
- `backend/internal/admin/cache_handler.go`: 操作员 API GET /admin/cache/stats + DELETE /admin/cache/{key}

## cache backend (D1)

3 选项:
- (A) in-memory LRU + TTL — 最简单,单进程局限;personal edition 够用
- (B) Redis — 多进程 / SaaS scale;但加运维负担
- (C) disk-backed (BadgerDB) — 单机持久 + 重启不丢

**推 A 作 Phase 1 + B 作 Phase 2 (SaaS edition 启用)**

## stream cache semantics (D2 — 棘手)

流式响应缓存难点:
- (a) 缓存完整 SSE 序列回放? — 第一次完整收 + 之后回放
- (b) 只缓存最终 message + 跳过 SSE 序列? — 客户端不能再要 stream
- (c) 跳过流式缓存? — 安全但放弃大部分价值

**推 (a) 缓存完整 SSE** + per-vendor TTL (anthropic 1h / openai 30min / gemini 1h, 因调用成本不同)。

## TTL strategy

- default TTL: 1h
- per-vendor override (config)
- per-request override via header `X-Huakai-Cache-TTL: 600s` (但 Owner 决定是否暴露)

## test plan

- unit: key canonicalization (timestamps/metadata 不影响 key)
- unit: LRU eviction order
- integration: cache hit 第二次请求不打上游 (mock 上游 0 调用)
- integration: stream cache replay 正确 SSE 序列
- chaos: cache backend down 时 fail-open 直接打上游 (不阻塞)
- E2E: hot path 加 cache 后 p95 latency 应降 50%+ for cacheable requests

## time estimate

3-5 天 codex + 1 天 review = 4-6 天

## blast radius

低 — fail-open 设计,cache miss/down 都直接走上游。

## decision points

(D1) backend 选型: LRU vs Redis vs Badger  
(D2) stream cache 缓存什么 (完整 SSE vs final-only vs skip)  
(D3) cache key 是否带 user_id (按用户隔离)  
(D4) X-Huakai-Cache-TTL header 是否对外暴露  
(D5) cache invalidation: 操作员手动 + auto-on-account-rotate

## clean-room

声誉级:
- LiteLLM 据闻有 cache_control + Redis backend
- Helicone / Portkey 据闻 gateway-level cache
- HUAKAI 升级点 (架构 + 算法 + 生态): per-vendor TTL + stream SSE replay + canonical key 三合一; 与 PASR cache-locality routing (memory project_pasr_real_diff_matrix) 协同 (PASR 路由 + L2 cache 双层降本)

## sources read

- F-CACHE-001 row
- memory project_pasr_real_diff_matrix
- (未读) 上游 reference 源码 — 仅声誉级引用
