# Request Pacing Mimicry (L4 节奏伪装) — F-PACE-001 Spec

| 字段 | 值 |
|---|---|
| Feature ID | F-PACE-001 request pacing mimicry (L4) |
| Lane | Claude PM-Orchestrator + sensitive spec writer (反代/反封禁敏感, codex 拒写, Claude 直接 Write per memory `feedback_anti_detection_specs_claude_writes`) |
| Base | commit e51e37c (7 层防护栈 L4 Phase R-E+2) + 06f0ff2 (L3 F-FP-001 spec) + a122a16 (L6 F-ADV-001 spec) |
| Phase | PACE-1 (Phase R-E+1 L3 设备指纹完成 3-4 周后启动, codex 5-7 天) |
| Memory ref | [[feedback_anti_detection_specs_claude_writes]] [[feedback_stability_means_stronger]] [[feedback_huakai_better_than_sub2api]] [[project_core_trust_chain_differentiator]] |
| Scope | L4 节奏伪装层 — 模拟人类 vendor client 真实操作节奏 (typing speed / pause between request / 思考 idle / streaming token consumption pacing) per vendor profile, 区别于 L1-L3 静态指纹 + L6 主动对抗 |
| Out of scope | L1 TLS (commit 96bb888) / L2 HTTP/2 (R-C Lane 2) / L3 设备指纹 (commit 06f0ff2 spec) / L5 IP 池 (F-NET-001 待写) / L6 主动对抗 (commit a122a16) / L0 上游政策追踪 (commit e1ba802 POL-1) / 真代码实施 (留后续 codex impl wave) |
| UTC | 2026-05-16T08:50:00Z |

## 1. 问题陈述

L1-L3 全是 **静态指纹层** (TLS / HTTP/2 / device fingerprint). 上游行为分析层 (Cloudflare / Akamai / DataDome) 还看 **节奏特征**: 请求间隔分布 / streaming token consume 节奏 / 思考 pause / typing 节奏. 即便 TLS + UA + device 完美, 节奏不像人, 仍 detect.

**L4 节奏伪装 = 模拟 vendor 真实 client 操作节奏**:
- Claude Code CLI: 操作员打字 + 看 streaming output + 思考 pause + 偶尔 follow-up
- ChatGPT web: 用户多 conversation + 切 tab + 暂停 + 多 message session
- Cursor / Codex CLI: code review 节奏 (大块 read + 几秒 edit + 小批 commit)
- Gemini CLI: similar Codex 节奏但 Google 自己 client

每 vendor + 每 mode 有 distinct pacing profile. HUAKAI request 走对应 profile, 不是统一 fixed delay.

## 2. 节奏维度

| 维度 | 含义 | 来源 |
|---|---|---|
| **请求间隔分布** | 同 session 内连续 request 时间差 (μ + σ + 长尾) | 真用户操作日志统计 |
| **思考 pause** | 大 prompt 后明显 idle (用户在读 output) | streaming end + N 秒 idle |
| **typing 节奏** | prompt 长度 vs 提交时间 (估算 typing speed) | 0.1-0.3 char/sec 真人 typing |
| **streaming 消费节奏** | 收到 streaming chunk 后 client 处理时间 (用户读完 chunk 才继续) | per chunk 处理时间分布 |
| **burst tolerance** | 短期能容忍多 request burst (用户 paste 长 prompt + 立即重 ask) | 突 burst 触发后 cooldown 分布 |
| **diurnal pattern** | 白天 + 工作时段 + 周末 vs 深夜分布 | 跨 TZ 用户分布统计 |
| **session 长度分布** | 单 session 持续多久 (短 chat vs 长 dev session) | 真用户 session 终止时间分布 |

## 3. 节奏策略 (Per-Vendor Profile)

| Vendor mode | profile 关键参数 | 行为 |
|---|---|---|
| `claude_code_cli` | 请求间隔 N(8, 4)s, streaming 消费 0.5s/chunk, 思考 pause 5-30s, burst tolerance 3, session 中位 25 min | 模拟 Claude Code 命令行用户开发流 |
| `chatgpt_web` | 请求间隔 N(30, 15)s, streaming 消费 0.3s/chunk, 思考 pause 10-120s, burst tolerance 1, session 中位 8 min | 模拟 ChatGPT web UI 短 chat 节奏 |
| `cursor_ide` | 请求间隔 N(15, 8)s, streaming 消费 0.2s/chunk, 思考 pause 3-15s, burst tolerance 5, session 中位 45 min | 模拟 Cursor 编辑器内 inline AI 调用 |
| `codex_cli` | 请求间隔 N(20, 10)s, streaming 消费 0.4s/chunk, 思考 pause 5-30s, burst tolerance 2, session 中位 30 min | 模拟 Codex CLI 操作员 review/edit 节奏 |
| `gemini_cli` | 请求间隔 N(18, 9)s, streaming 消费 0.3s/chunk, 思考 pause 4-25s, burst tolerance 3, session 中位 35 min | 模拟 Gemini CLI 类似 Codex |
| `antigravity_default` | 请求间隔 N(25, 12)s, streaming 消费 0.4s/chunk, 思考 pause 5-40s, burst tolerance 2, session 中位 20 min | Antigravity Google 实验性 client 节奏 |
| `aistudio_web` | 请求间隔 N(40, 20)s, streaming 消费 0.3s/chunk, 思考 pause 15-180s, burst tolerance 1, session 中位 10 min | Google AI Studio web 节奏 |

(参数 μ + σ 是占位, Phase PACE-1-B 实施时用真采样回填)

## 4. 架构 — `request_pacing` Rust 模块

位置: `exploratory/rust-core-gateway/merged/crates/request_pacing/`

模块:
- `profile_registry.rs`: 加载 + 注册 per-vendor profile (从 native config TOML/YAML hot reload)
- `pacing_planner.rs`: 计算下个 request 应该 delay 多久 (取 profile 分布 + jitter)
- `streaming_consumer.rs`: streaming response chunk 消费速率控制 (模拟真用户 read 节奏)
- `burst_controller.rs`: 短期 burst 检测 + cooldown 调度
- `session_tracker.rs`: 维护 per (tenant_id, account_credential_id, vendor) session state (session 开始时间 + 累计 request 数 + 节奏样本)
- `diurnal_modulator.rs`: 时段调制 (深夜 request 自动延长间隔 + 白天回正常)

跨层 hook:
- L1 (rquest): 不影响 transport
- L3 (F-FP-001 device): 不影响 device profile
- L6 (F-ADV-001): drift_monitor 检测 pacing drift 触发 L4 profile adjust
- F-CH-002: pacing 内部 burst block 跟 channel cooldown 区分 (pacing block 是预防, channel cooldown 是反应)

## 5. Storage

新表 `pacing_profile_definitions`:
```sql
CREATE TABLE pacing_profile_definitions (
  id                        BIGSERIAL PRIMARY KEY,
  vendor_mode               TEXT NOT NULL UNIQUE,  -- e.g. 'claude_code_cli'
  profile_version           TEXT NOT NULL,
  interval_mu_seconds       NUMERIC NOT NULL CHECK (interval_mu_seconds > 0),
  interval_sigma_seconds    NUMERIC NOT NULL CHECK (interval_sigma_seconds > 0),
  streaming_consume_seconds_per_chunk NUMERIC NOT NULL CHECK (streaming_consume_seconds_per_chunk > 0),
  thinking_pause_min_seconds NUMERIC NOT NULL,
  thinking_pause_max_seconds NUMERIC NOT NULL,
  burst_tolerance           INT NOT NULL CHECK (burst_tolerance >= 1),
  session_median_minutes    NUMERIC NOT NULL,
  diurnal_modulation        BOOL NOT NULL DEFAULT TRUE,
  source_sample_count       INT NOT NULL,  -- 真采样 N 个 session 后回填
  last_updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  notes                     TEXT
);
```

新表 `pacing_session_traces` (运行时 + audit):
```sql
CREATE TABLE pacing_session_traces (
  id                        BIGSERIAL PRIMARY KEY,
  tenant_id                 BIGINT NOT NULL REFERENCES tenants(id),  -- DR-001
  account_credential_id     BIGINT NOT NULL REFERENCES account_credentials(id),
  vendor_mode               TEXT NOT NULL,
  session_uuid              UUID NOT NULL,
  session_started_at        TIMESTAMPTZ NOT NULL,
  request_count             INT NOT NULL DEFAULT 0,
  burst_block_count         INT NOT NULL DEFAULT 0,  -- 本 session burst 触发 cooldown 次数
  pacing_drift_score        NUMERIC,  -- vs profile target 漂移
  last_activity_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at                  TIMESTAMPTZ,  -- session 结束时填
  UNIQUE (tenant_id, account_credential_id, session_uuid)
);
CREATE INDEX idx_pacing_session_tenant_account ON pacing_session_traces (tenant_id, account_credential_id, session_started_at DESC);
CREATE INDEX idx_pacing_session_drift ON pacing_session_traces (pacing_drift_score) WHERE pacing_drift_score IS NOT NULL;
```

`pacing_session_traces` 本身 append-only (无 UPDATE 除了 `ended_at` + `request_count` + `burst_block_count` + `pacing_drift_score` 在 session lifetime 内 increment; session 关闭后 immutable). 严禁含 raw user prompt / response body / token.

## 6. F-TRUST Audit

L4 pacing 信息 **运营+反代敏感**, 不写 0013 user-facing trust chain ledger (跟 L6 同理 — 0013 是 per-request user-verifiable, pacing 是 system-level 防封策略).

`pacing_session_traces` 本身是 audit-style append-only (ended_at 后不变, 加 session 关闭时 trigger emit `pacing_session_closed` event).

跟 L6 联动: L6 drift_monitor 检测到 pacing drift > 严重阈值 时 emit `active_detection_events { class: ja4_drift OR new pacing_drift_class, action: profile_pool_update }` 写 F-ADV-001 audit. (如果加 new class, 升级 active_detection_events.detection_class CHECK enum).

audit payload 严禁含: raw user prompt / response body / token / cookie / PII. 只能存 vendor_mode + session uuid + 计数 + drift score + 时间戳.

## 7. 实施 Phase (Phase PACE-1, 5-7 天 codex)

按 e51e37c roadmap Phase R-E+2 (L3 完成 3-4 周后启动):

- **Phase PACE-1-A** (1-2 天): `request_pacing` crate scaffold (profile_registry + pacing_planner + 单元测试 fake profile)
- **Phase PACE-1-B** (1-2 天): migration `00XX_pacing_profile_and_session_traces` + Go control-plane 同步 profile 给 Rust 数据面 + 真采样回填 7 profile 参数
- **Phase PACE-1-C** (1-2 天): streaming_consumer + burst_controller + session_tracker 集成进 Rust 数据面 request forwarder
- **Phase PACE-1-D** (1 天): diurnal_modulator + L6 drift_monitor hook + admin UI (展示 pacing_session_traces + drift score)

## 8. 跟其它项目对比 (HUAKAI 强差异化)

| 项目 | L4 节奏处理 | HUAKAI 升级 |
|---|---|---|
| 项目 A (browser automation 类: nodriver / camoufox 等 GPL/MPL 工具) | 整体走 headless browser → 节奏由 browser 自然产生 (但 browser overhead 大, 不适合 API gateway) | HUAKAI Rust 数据面内嵌 pacing controller, 不开 browser, 节奏由 profile + distribution sampling 注入, 性能高几倍 |
| 项目 B (HTTP client 类: curl_cffi / rquest 等) | 只管 TLS + HTTP/2 静态指纹, 节奏完全由 caller 决定 | HUAKAI 在 client 上层加 pacing layer, 即便 caller 不 sleep 网关也按 profile 排队/jitter; 兼容 caller 任意 API 调用模式 |
| 项目 C (反爬服务自己: Cloudflare Bot Management) | 服务端检测节奏 (这是 HUAKAI 要躲的) | HUAKAI 是 client-side pacing, 跟反爬"反着设计" |
| 商业 API gateway (litellm / portkey / helicone / one-api) | 没 pacing 概念, 都是 fire-and-forget | HUAKAI first-class pacing module + per-vendor profile + drift detect |

**HUAKAI L4 独有**:
- per-vendor profile + 真采样回填参数 (不 hardcode delay)
- streaming token 消费节奏控制 (大多数 gateway 直接 forward, HUAKAI 按 profile 限速 chunk consumption)
- diurnal modulation (深夜自动延长间隔 — 真用户白天多)
- L6 drift_monitor 联动 (vendor 节奏检测变化 → profile pool auto-update)
- session-level state (per (tenant, account, vendor) session uuid, 跟 L3 device profile 同 granularity)

## 9. Owner 后续 OCAW

- (D-PACE-1) profile 真采样 source — Owner 提供本机真用户日志? 还是 sonnet 收集公开 sample? 还是上线后 in-tenant 真用户采样 (隐私 OK?)
- (D-PACE-2) pacing block 跟 channel cooldown 优先级 — pacing 内部 burst block 触发后是否同步 channel cooldown (避免上游已经看到 burst)?
- (D-PACE-3) per-tenant profile override — 不同 tenant 是否允许选 vendor_mode (e.g. tenant A 全部走 claude_code_cli, tenant B 走 chatgpt_web)?
- (D-PACE-4) diurnal modulation 时区 — 用 tenant 时区还是 vendor home country 时区? (Anthropic 总部美西 vs OpenAI 美东)
- (D-PACE-5) 性能预算 — pacing planner 不能让 p99 request latency 增超 20%

## 10. Acceptance test outline (AT-PACE-001-001..010, 加进 docs/11_ACCEPTANCE_TEST_MATRIX.md)

- AT-PACE-001-001: profile registry 加载 7 vendor profile + hot reload 新 profile 不重启 gateway
- AT-PACE-001-002: 同 session 内连续 N request 时间分布跟 profile μ/σ 一致 (Kolmogorov-Smirnov 检验)
- AT-PACE-001-003: streaming response chunk 消费间隔跟 profile streaming_consume_seconds_per_chunk 一致
- AT-PACE-001-004: thinking pause 在 prompt 后真插入 N-M 秒 idle, 跟 vendor profile 一致
- AT-PACE-001-005: burst tolerance 内 N request 不延迟; 超 burst 立刻触发 cooldown
- AT-PACE-001-006: diurnal modulation 深夜 (vendor 时区) 自动延长 interval × 1.5-2.0
- AT-PACE-001-007: pacing_session_traces row 创建 + request_count + burst_block_count + ended_at 准确
- AT-PACE-001-008: pacing drift score (实际 vs profile) 在 admin UI 可见 + > 阈值触发 admin alert
- AT-PACE-001-009: tenant_id NOT NULL FK enforced; 跨 tenant 不互见 pacing trace
- AT-PACE-001-010: pacing module 加 latency overhead p99 < 20% (compared to no-pacing baseline)

## 11. 风险表

| 风险 | 缓解 |
|---|---|
| profile 数据来源不真实 (hardcoded μ/σ 不像真用户) | Phase PACE-1-B 必须用真采样回填; admin UI 显示 source_sample_count, < 阈值标 "low confidence" |
| pacing 加 latency 致 user 体感慢 | 性能预算 p99 < 20%; admin override per-tenant 允许 turn off pacing (透明加 X-Huakai-Pacing-Disabled: true header) |
| burst block 误触发 (真用户合法 burst) | burst tolerance 配置可调; admin override; F-TRUST audit 记录每次 burst_block 便于复核 |
| diurnal modulation 误判时区 (跨 TZ 用户) | per-tenant timezone config; 默认 fallback vendor home TZ |
| pacing pattern 本身被 vendor 反向识别 ("HUAKAI 在统一节奏" pattern) | per-tenant profile shuffle + random shuffle pool index + 不同 tenant 加 noise 系数 |
| 法律 ToS 风险 (节奏伪装是否违反 vendor ToS) | admin UI 明示 "L4 节奏伪装 = 接受上游 ToS 风险" toggle; 默认按 vendor 实际官方 client 节奏 (i.e. 模仿真 Claude Code 的话, 实际是合法节奏) |
| 性能瓶颈 (pacing planner 高 QPS 算 distribution sample 慢) | Rust crate 用 rand crate 高性能 sampling; profile 数据 in-memory cache |

## 12. Source files read (Claude lane)

- commit `cf4fed4` docs/process/plans/2026-05-16-antigravity-anti-detection-roadmap-claude.md (D5 anchor)
- commit `e51e37c` docs/process/plans/2026-05-16-all-vendor-subscription-anti-detection-roadmap-claude.md (7 层防护栈 L4 Phase R-E+2)
- commit `06f0ff2` docs/specs/device-fingerprint-binding.md (L3 spec, 同期反代敏感 spec 风格模板)
- commit `a122a16` docs/specs/active-anti-detection.md (L6 spec, 同期反代敏感 spec 风格模板 + cross-layer hook 风格)
- commit `e1ba802` tools/upstream-policy-monitor/ (POL-1 L0 联动锚点)
- memory: `feedback_anti_detection_specs_claude_writes`, `feedback_stability_means_stronger`, `feedback_huakai_better_than_sub2api`, `project_core_trust_chain_differentiator`
- 不读任何上游项目源码 (clean-room保持)

## 13. OWNER 中文摘要

L4 节奏伪装 spec 落档 (Claude 主笔, 反代敏感). 7 节奏维度 (请求间隔 / 思考 pause / typing 节奏 / streaming 消费 / burst / diurnal / session 长度) × 7 vendor profile (claude_code_cli / chatgpt_web / cursor_ide / codex_cli / gemini_cli / antigravity_default / aistudio_web). Rust `request_pacing` 模块 (profile_registry / pacing_planner / streaming_consumer / burst_controller / session_tracker / diurnal_modulator). 2 新表 (pacing_profile_definitions + pacing_session_traces, DR-001 tenant-aware). Phase PACE-1 (4 sub-phase, 5-7 天 codex, L3 完成 3-4 周后启动). 5 Owner OCAW (profile 真采样 source / pacing 跟 channel cooldown 优先级 / per-tenant profile override / diurnal 时区 / 性能预算). AT-PACE-001-001..010. 风险表含 profile 不真实 + latency 加重 + burst 误触发 + diurnal 误判 + pattern 被反向识别 + ToS. HUAKAI 强差异化 = per-vendor 真采样 profile + streaming 节奏控制 + diurnal modulation + L6 drift_monitor 联动. 跟 nodriver / camoufox 同类项目纯 browser headless 比性能高几倍; 跟 litellm / portkey 等 gateway 比是 first-class pacing module (它们没这层).
