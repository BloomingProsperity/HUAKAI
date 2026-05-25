# Outbound IP Pool (L5 IP 池) — F-NET-001 Spec

| 字段 | 值 |
|---|---|
| Feature ID | F-NET-001 outbound IP pool & per-account binding (L5) |
| Lane | Claude PM-Orchestrator + sensitive spec writer (反代/反封禁敏感, codex 拒写, Claude 直接 Write per memory `feedback_anti_detection_specs_claude_writes`) |
| Base | commit e51e37c (7 层防护栈 L5 Phase R-E+2 后) + 06f0ff2 (L3 F-FP-001 spec, 同 per-account binding pattern) + a122a16 (L6 F-ADV-001 spec) + 本 wave 之前 (L4 F-PACE-001 spec) |
| Phase | NET-1 (Phase R-E+2 L4 节奏完成 2-3 周后启动, codex 5-8 天) |
| Memory ref | [[feedback_anti_detection_specs_claude_writes]] [[feedback_stability_means_stronger]] [[feedback_huakai_better_than_sub2api]] [[project_core_trust_chain_differentiator]] |
| Scope | L5 IP 池层 — outbound IP 多源池 + per-account 稳定 binding + 自动 rotation on burn + 跨账号同 IP 检测 + 地区 distribution (避免单 IP 多账号触发 vendor 风控) |
| Out of scope | L1 TLS (commit 96bb888) / L2 HTTP/2 / L3 device fingerprint (commit 06f0ff2) / L4 节奏 (本 wave 之前 commit) / L6 主动对抗 (commit a122a16) / L0 上游政策追踪 (commit e1ba802) / 入站 IP / API rate-limit 算法 / 真代码实施 (留后续 codex impl wave) |
| UTC | 2026-05-16T08:55:00Z |

## 1. 问题陈述

L1-L4 解决 application-layer 指纹 + 节奏伪装. 但 **网络层 IP** 也是 vendor 风控关键信号:
- 同 IP 短期多 account 登录 → vendor 立刻关联 ban
- 数据中心 IP (AWS / GCP / Azure cluster) → vendor 检测出"非真用户" 风险高
- 同 IP 单 account 长期 → 跟 device fingerprint 形成 "稳定真用户" 信号 (好)
- IP 跨地区跳 (per request 换 IP) → vendor 检测出 "可疑" (坏)

**L5 IP 池 = 给每 account 绑稳定 outbound IP (跟 L3 device 同 granularity), 池里多 IP 来源 (residential / mobile / VPS 混合), 单 IP burn 后整池协同 rotation**.

跟 L3 device profile binding 同 pattern: 每 account 启用时分配 IP, refresh 不变 IP, ban 后 burn IP + 重新分配.

## 2. IP 池来源 (混合策略)

| 来源类型 | 风险信号 | 占比建议 (per pool) | 备注 |
|---|---|---|---|
| **Residential proxy** | 看似真家庭用户 IP, vendor 信任度高 | 50-70% | 商用 residential pool (Bright Data / Oxylabs / Smartproxy 类, 走 SOCKS5/HTTPS proxy) |
| **Mobile carrier IP** | 移动运营商分配, NAT 池, vendor 难关联 | 10-20% | 通过 mobile proxy 服务或自建 mobile gateway |
| **小 VPS / 自建** | 数据中心 IP 但 ASN 不知名, 单 IP 单账号长期 | 10-20% | 自购 small VPS (DigitalOcean / Hetzner / Linode, ASN 多样化) |
| **Tor exit (实验)** | Tor 网络出口, 上游普遍拒绝 | 0-5% (实验性) | 仅特定 vendor + 配合 manual override; vendor 风控通常封 Tor |

**禁混 cloud burst IP** (AWS / GCP / Azure 大段 ASN, vendor 立刻 detect 数据中心).

## 3. Per-Account Binding 策略

| 策略 | 行为 | 适用场景 |
|---|---|---|
| **stable_per_account** (默认) | 每 account 启用时从 pool 分配 1 IP + 长期固定, refresh 不变, ban 后 burn 整 IP + 重分配 | 大多数 vendor (Anthropic / OpenAI / Gemini), 跟 L3 device binding 配对 |
| **stable_per_session** | 每 session (per L4 pacing session uuid) 分配 1 IP, session 结束释放回 pool | 短 session vendor (ChatGPT web 用户多 session, IP 跟 session 一致) |
| **rotate_per_request** | 每 request 换 IP (随机从 pool 抽) | 仅特殊 vendor 已 detect 节奏 + 反 IP-tracking, NOT 默认 |
| **manual_pin** | 操作员手动指定 specific IP for specific account | 调试 / debug / 特殊 ToS 要求 (例 vendor 要求企业级 IP allowlist) |

策略由 admin per-account / per-vendor 配置, 默认 `stable_per_account`.

## 4. 架构 — `outbound_ip_pool` Rust 模块 + Go control plane sync

位置:
- Rust 数据面: `exploratory/rust-core-gateway/merged/crates/outbound_ip_pool/`
- Go 控制面: `backend/internal/ippool/` (control plane API + admin handlers)

Rust 模块:
- `pool_registry.rs`: 加载 pool 配置 (来自 control plane), 维护 in-memory IP pool by tenant + vendor
- `binding_allocator.rs`: per-account IP 分配算法 (stable_per_account / stable_per_session / rotate_per_request / manual_pin)
- `proxy_client.rs`: 出站走 SOCKS5/HTTPS proxy (residential / mobile pool 用), 透明 wrap 在 rquest http client 上
- `health_probe.rs`: 定期 probe pool IP 可用性 (ban / 限速 / 不可达)
- `burn_correlator.rs`: 跟 F-CH-002 ban_signal + L3 fingerprint burn 联动 (account 被 ban 时 IP 也 burn)

Go 控制面:
- `pool_config_store.go`: pool 配置 CRUD + per-tenant pool 划分
- `binding_store.go`: per-account IP binding 记录 + sync 给 Rust 数据面
- `admin/ippool_handler.go`: admin API (列 pool / 加 pool source / burn IP / rotate / manual_pin)

跨层 hook:
- L1 (rquest): IP pool 通过 proxy_client wrap http client, transport 透明
- L3 (F-FP-001 device): 同 account 同 IP + 同 device profile, cross-layer family enforce
- L4 (F-PACE-001 节奏): IP pool 不影响节奏, 但 IP burn 时 pacing session 标 ended
- L6 (F-ADV-001): cross_account_ban 关联 检测同 IP 多 account 时升级到 burn IP + 重分配
- F-CH-002: ban_signal 触发 fingerprint burn 同时 IP burn (cooldown 期间 IP 暂停, 期满后回 pool 或永久 retire)

## 5. Storage

新表 `outbound_ip_pool_sources`:
```sql
CREATE TABLE outbound_ip_pool_sources (
  id                BIGSERIAL PRIMARY KEY,
  tenant_id         BIGINT NOT NULL REFERENCES tenants(id),  -- DR-001
  source_name       TEXT NOT NULL,                            -- e.g. 'brightdata-residential-us', 'self-vps-de'
  source_type       TEXT NOT NULL CHECK (source_type IN ('residential','mobile','small_vps','tor','manual')),
  proxy_endpoint    TEXT,                                     -- SOCKS5/HTTPS proxy URL (encrypted at rest, AES-GCM)
  proxy_credential_id BIGINT REFERENCES account_credentials(id),  -- 复用 F-AUTH-005 credential 机制存 proxy auth
  ip_subnet         CIDR[],                                   -- 已知 IP 段 (用于 health probe 范围)
  geo_country       TEXT,                                     -- ISO country code
  geo_region        TEXT,                                     -- e.g. 'us-west', 'eu-central'
  asn               INT,                                      -- ASN 数 (检测 cluster ASN avoid)
  active            BOOL NOT NULL DEFAULT TRUE,
  total_quota       INT,                                      -- 可用 IP 数上限 (residential pool 计费 cap)
  used_count        INT NOT NULL DEFAULT 0,
  burned_count      INT NOT NULL DEFAULT 0,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_health_check TIMESTAMPTZ,
  UNIQUE (tenant_id, source_name)
);
CREATE INDEX idx_ip_pool_source_tenant_active ON outbound_ip_pool_sources (tenant_id, active, source_type);
```

新表 `outbound_ip_bindings`:
```sql
CREATE TABLE outbound_ip_bindings (
  id                        BIGSERIAL PRIMARY KEY,
  tenant_id                 BIGINT NOT NULL REFERENCES tenants(id),  -- DR-001
  account_credential_id     BIGINT NOT NULL REFERENCES account_credentials(id),
  vendor                    TEXT NOT NULL,                        -- 冗余 vendor 给 query 性能
  binding_strategy          TEXT NOT NULL CHECK (binding_strategy IN ('stable_per_account','stable_per_session','rotate_per_request','manual_pin')),
  source_id                 BIGINT NOT NULL REFERENCES outbound_ip_pool_sources(id),
  assigned_ip               INET,                                 -- 实际分配的 IP (rotate 模式可为 NULL)
  assigned_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  status                    TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','burned','rotating','manual_pinned')),
  burned_at                 TIMESTAMPTZ,
  burn_reason               TEXT,                                 -- e.g. 'ch_002_ban_signal','manual_admin','health_probe_unreachable'
  UNIQUE (tenant_id, account_credential_id)                       -- 同 account 同时只能 1 binding (rotate 模式动态填 assigned_ip)
);
CREATE INDEX idx_ip_binding_source ON outbound_ip_bindings (source_id, status);
CREATE INDEX idx_ip_binding_ip_lookup ON outbound_ip_bindings (assigned_ip, status) WHERE assigned_ip IS NOT NULL;
```

新表 `outbound_ip_burn_events` (append-only audit):
```sql
CREATE TABLE outbound_ip_burn_events (
  id                        BIGSERIAL PRIMARY KEY,
  tenant_id                 BIGINT NOT NULL REFERENCES tenants(id),  -- DR-001
  binding_id                BIGINT NOT NULL REFERENCES outbound_ip_bindings(id),
  burned_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  burn_reason               TEXT NOT NULL,
  triggered_by              TEXT NOT NULL CHECK (triggered_by IN ('ch_002_ban','fp_001_burn','admin_manual','health_probe','cross_account_ban_l6')),
  evidence_redacted         JSONB NOT NULL                        -- enum + 计数 + ID; 严禁 raw upstream body / cookie / token
);
CREATE INDEX idx_ip_burn_tenant_time ON outbound_ip_burn_events (tenant_id, burned_at DESC);
```

`outbound_ip_pool_sources.proxy_endpoint` + `proxy_credential_id` 必须 encrypted at rest (复用 F-AUTH-005 KeyProvider). `assigned_ip` 是 system-internal 不暴露给 end user. burn_events 严禁含 raw upstream response.

## 6. F-TRUST Audit

跟 L3 / L4 / L6 同模式: pool 内部 audit 走 append-only `outbound_ip_burn_events` + 跟 channel_health_state / device_fingerprint_bindings 状态变更 **同 tx 原子**, 失败整体 rollback.

不写 0013 user-facing ledger (IP 信息是 operator-facing 内部 audit, 不暴露给 end user 验证 per-request signature).

但跟 L6 联动: cross_account_ban detection 触发 IP burn 时, L6 active_detection_events 应该 reference outbound_ip_burn_events.id (cross-layer audit trace).

audit payload 严禁含: raw cookie / raw upstream body / token / user prompt / credential bytes / proxy auth credential / 个人识别信息 (PII). assigned_ip 只在 tenant-scope admin UI 可见, 跨 tenant 不暴露.

## 7. 实施 Phase (Phase NET-1, 5-8 天 codex)

按 e51e37c roadmap Phase R-E+2 之后 (L4 节奏完成 2-3 周后启动):

- **Phase NET-1-A** (1-2 天): migration `00XX_outbound_ip_pool` 3 表 + Go control plane CRUD (pool source / binding / burn event)
- **Phase NET-1-B** (1-2 天): Rust `outbound_ip_pool` crate scaffold (pool_registry + binding_allocator + proxy_client wrap rquest)
- **Phase NET-1-C** (1-2 天): admin handler 5 endpoint (列 pool / 加 source / list bindings / burn IP manual / rotate strategy change)
- **Phase NET-1-D** (1-2 天): health_probe + burn_correlator (F-CH-002 / F-FP-001 / L6 联动) + AT 集成测试

## 8. 跟其它项目对比 (HUAKAI 强差异化)

| 项目 | L5 IP 处理 | HUAKAI 升级 |
|---|---|---|
| 商业 proxy 服务 (Bright Data / Oxylabs / Smartproxy) | 卖 IP pool, 但 caller 自己管 binding + rotation 策略 | HUAKAI 把 pool client 集成到 gateway, 实现 per-account stable binding + 跨层 burn 关联 + admin UI, 不是裸 proxy 客户端 |
| 项目 D (scraper 工具类: scrapy-rotating-proxies / scrapoxy 等) | per-request rotate (默认), 适合 web crawl 不适合 vendor account | HUAKAI 默认 stable_per_account (账号长期同 IP 看起来真用户), 跟 L3 device binding 同 granularity |
| sub2api / new-api / litellm / portkey | 没 IP pool 概念, 都走 caller machine IP | HUAKAI first-class IP pool + per-tenant 配置 + 4 binding strategy + 跟反封禁层 (L3/L4/L6) 联动 |
| 项目 E (browser proxy 类: anti-bot bypass 商业服务) | 节奏 + IP + browser fingerprint 一体 (但只 web scraping 用) | HUAKAI 拆开 L1-L6 各层独立 module, IP pool 单独配置可调 |

**HUAKAI L5 独有**:
- per-account stable binding (跟 L3 device + L4 session granularity 一致, 整体看起来"真稳定用户")
- 多源 pool 混合 (residential 主 + mobile 副 + 自建 VPS 兜底)
- 自动 burn 关联 (F-CH-002 ban → 同时 burn IP + L3 device + cooldown account)
- cross-layer audit (IP burn 跟 L6 active_detection_events 双向 reference)
- proxy 凭证复用 F-AUTH-005 加密机制
- per-tenant pool 划分 (大 tenant 自己买 pool, 小 tenant 共享 default pool)

## 9. Owner 后续 OCAW

- (D-NET-1) 默认 binding strategy — `stable_per_account` (默认推) vs `stable_per_session` (短 session 适合)?
- (D-NET-2) 商用 residential pool 采购 — Owner 自购 Bright Data / Oxylabs / 其他? 还是 HUAKAI 自带 default pool (但成本谁担)?
- (D-NET-3) 单 IP 同时绑多 account 阈值 — 默认 1:1 (最稳) 还是 1:N (节约 IP)?
- (D-NET-4) burned IP 复用策略 — 永久 retire 还是 cooldown 24-72h 后回池?
- (D-NET-5) Tor exit 是否启用 — 默认 disabled (vendor 普遍封), 还是给特殊 use case 开 toggle?
- (D-NET-6) 性能预算 — proxy 加 latency, p99 < 30% 接受?

## 10. Acceptance test outline (AT-NET-001-001..012, 加进 docs/11_ACCEPTANCE_TEST_MATRIX.md)

- AT-NET-001-001: account 启用时自动从 active pool 分配 1 IP + outbound_ip_bindings row 创建 + status=active
- AT-NET-001-002: 同 account 多 outbound request → 全部走同 IP (stable_per_account 策略生效)
- AT-NET-001-003: 不同 account → 不同 IP (collision <5% per pool)
- AT-NET-001-004: F-CH-002 ban_signal 触发 → outbound_ip_bindings.status=burned + outbound_ip_burn_events row 写入 + 新 binding 不复用此 IP
- AT-NET-001-005: 跨层 burn 同 tx 原子 — IP burn + device burn + channel cooldown 一起 commit/rollback
- AT-NET-001-006: account 删除时 binding cascade 删除 (FK CASCADE)
- AT-NET-001-007: health_probe 检测 pool IP 不可达 → 标 inactive + 不再分配 + admin alert
- AT-NET-001-008: rotate_per_request 策略 — 每 request 不同 IP (按 pool 分布)
- AT-NET-001-009: manual_pin 策略 — 指定 IP 后 binding 不自动 rotate, ban 时仍触发 alert 但 status 不变 burned (除非 admin manual 改)
- AT-NET-001-010: cross-account 同 IP 检测 — 同 IP 绑 N+ account 时 L6 触发 cross_account_ban 关联
- AT-NET-001-011: tenant_id NOT NULL FK enforced; 跨 tenant 不互见 pool / binding
- AT-NET-001-012: outbound_ip_burn_events 严格 redacted (无 raw cookie / body / token); proxy_endpoint encrypted at rest 不 leak

## 11. 风险表

| 风险 | 缓解 |
|---|---|
| 商业 residential pool 成本高 (按 GB 计费) | per-tenant quota + admin UI 显示用量 + cost alert; 小 tenant 共享 default pool, 大 tenant 自购 |
| pool 全 burned 致 account 无 IP 可用 | pool exhausted alert + admin manual 加 source + fallback 走 caller machine IP (transparent header `X-Huakai-IP-Pool-Exhausted: true`) |
| 商业 proxy 服务自身被 vendor block (Bright Data 公司被 OpenAI 拉黑) | 多 provider 混合 + 定期 health probe + 单 provider 故障不影响其他 source |
| Tor exit 触发 vendor 立刻 ban | 默认 disabled; 仅特殊 use case 开 toggle + admin 明示 ToS 风险 |
| proxy auth credential leak (encrypted at rest 但仍在 memory) | 复用 F-AUTH-005 KeyProvider; memory zeroize on shutdown; audit 严禁含 proxy auth |
| 性能 (proxy 加 latency 致 p99 涨) | health_probe 排除 slow proxy; per-tenant 性能 SLO; admin override 允许特定 account turn off proxy (透明) |
| 法律 ToS 风险 (residential proxy 来源合规性) | 仅用 KYC 合规 vendor (Bright Data 等); admin UI 明示 "L5 IP pool = 接受合规 ToS 风险" toggle; 自建 VPS 选项不依赖第三方 |
| 同 IP 多 account 反触发 vendor 关联 ban | D-NET-3 默认 1:1; cross_account_ban 检测 (L6) 强制升级 burn IP |

## 12. Source files read (Claude lane)

- commit `cf4fed4` docs/process/plans/2026-05-16-antigravity-anti-detection-roadmap-claude.md (D5 anchor)
- commit `e51e37c` docs/process/plans/2026-05-16-all-vendor-subscription-anti-detection-roadmap-claude.md (7 层防护栈 L5 IP 池标"待写")
- commit `06f0ff2` docs/specs/device-fingerprint-binding.md (L3 spec, 同 per-account binding pattern 模板)
- commit `a122a16` docs/specs/active-anti-detection.md (L6 spec, cross-layer hook 模板)
- 本 wave 同期 docs/specs/request-pacing-mimicry.md (L4 spec, session granularity 锚点)
- commit `e1ba802` tools/upstream-policy-monitor/ (POL-1 L0 联动锚点)
- memory: `feedback_anti_detection_specs_claude_writes`, `feedback_stability_means_stronger`, `feedback_huakai_better_than_sub2api`, `project_core_trust_chain_differentiator`
- 不读任何上游项目源码 (clean-room保持)

## 13. OWNER 中文摘要

L5 IP 池 spec 落档 (Claude 主笔, 反代敏感). 4 来源类型 (residential 主 / mobile / small VPS / Tor 实验) + 4 binding 策略 (stable_per_account 默认 / stable_per_session / rotate_per_request / manual_pin). Rust `outbound_ip_pool` crate (pool_registry / binding_allocator / proxy_client / health_probe / burn_correlator) + Go control plane (CRUD + admin handler). 3 新表 (outbound_ip_pool_sources + outbound_ip_bindings + outbound_ip_burn_events, DR-001 tenant-aware). Phase NET-1 (4 sub-phase, 5-8 天 codex, L4 完成 2-3 周后启动). 6 Owner OCAW (默认 binding 策略 / 商用 pool 采购 / 单 IP 多 account 阈值 / burned IP 复用 / Tor / 性能预算). AT-NET-001-001..012. 风险表含 pool 成本 / pool exhausted / proxy 被 vendor block / Tor / proxy auth leak / latency / 法律 ToS / 同 IP 多 account 关联. HUAKAI 强差异化 = per-account stable binding (跟 L3/L4 同 granularity 整体真稳定用户) + 多源混合 + 自动 burn 跨层关联 + per-tenant pool 划分. 跟商业 proxy 服务比是 first-class integrated; 跟 sub2api / new-api / litellm / portkey 比是它们完全没这层; 跟 scraper 工具默认 rotate 比是 HUAKAI 默认稳定 (账号长期真用户 pattern).
