# W11-A D-1b Phase 1 — claude lane plan (2026-05-24)

> **Owner directive (quote, 2026-05-24)**：「协调 go 和解析逻辑一起做」
>
> **解读**：Owner 直接拍板把 P1-1 W11-A D-1b Phase 1 推进——含 §4.5 P-1 受控 proto 字段
> 落地（Rust 写入侧）+ Rust 数据面 client credential 解析器/scaffold + Manual First
> feature flag。Phase 1 默认实施约束（§7-J）：仅 mock/staging/internal-smoke 流量，
> **不承载可计费真实租户流量**——这一句已在 synthesis spec 内 bake-in。
>
> Lane: claude（与 codex 平行稿同步起草，事后 synthesis）
> Time: 2026-05-24T07:30Z

## 1. 元数据

| 字段 | 内容 |
|---|---|
| Slice ID | W11-A D-1b Phase 1（synthesis §5.2 P1-1） |
| 上游 spec | `docs/process/plans/2026-05-22-rust-hardening-plan.md` §2 D-1b + §4.5 P-1 + §7-H + §7-J + §8 W11-A A1..A5 |
| 上游优先级裁定 | `docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md` §4.3 → P1 |
| Owner 已决 | H (β scheme)、G.1 (P-1 字段加入本轮)、J (Phase 1 默认 mock/staging only) |
| 影响面 | `proto/route.proto` + `crates/core_gateway/src/{listener,account_planner,config,redaction,mock_control_plane}.rs` + 新子模块 `crates/core_gateway/src/client_auth/{mod,credential,manual_first}.rs`（Codex round 1 P1 finding 2026-05-24: 用子模块替代 2 个 root file 维持 src/ ~20 root entry budget） |
| 不动 | Go control plane runtime、计费账本、DB、UI、前端、`backend/` |
| 估时 | 1-2 codex-day（含 Codex per-commit review iteration） |
| Clean-room | L0（行为 parity，无 source-copying）。源仅看本仓 spec + HUAKAI 现有代码 + proto3 公开文档 |
| 分支 | `claude/rust-hardening`（不动主线 `claude/phase-1`） |
| Commit 风格 | 单 commit「account_planner + proto 客户端凭据透传与 Manual First 兜底」按 synthesis §6 recommended grouping |

## 2. Scope

### In

1. **`route.proto` P-1 变更**：新增 `ClientCredential` message + `RouteQueryRequest.client_credential` 字段。
2. **`crates/core_gateway/src/client_auth/` 新子模块**（per CLAUDE.md #13 + Codex round 1 P1 finding 2026-05-24 — 新责任进子模块, 保持 src/ root entry ≤ 20）：
   - `client_auth/mod.rs`: facade `mod credential; mod manual_first; pub use ...;`
   - `client_auth/credential.rs`:
     - `ClientCredentialKind` enum：`Bearer` / `ApiKey`（按 vendor 习惯：OpenAI 用 `Authorization: Bearer ...`，Anthropic 用 `x-api-key`）。
     - `ClientCredential` struct：携带 kind + redactable value + header source（用于审计与日志哈希）。
     - `ClientCredential::from_headers(headers: &HeaderMap) -> Result<Option<Self>, ClientAuthError>`：从 HTTP headers 派生。
     - `ClientCredential::fingerprint() -> ClientCredentialFingerprint`：返回 `kind=bearer sha256=abcd1234`（仅 prefix）形式。
   - `client_auth/manual_first.rs`: `ManualFirstConfig` + `ManualFirstResolver` + JSON file loader（D-5 已批）。
3. **`config.rs` 扩字段**：
   - `client_auth_manual_first_enabled: bool`（默认 ON in Phase 1 — Rust 仍走旧 tenant_id 派生路径；ON 时双写 client_credential 给 Go control plane 备消费）。
   - `client_auth_manual_first_keys: Vec<ManualFirstEntry>`（静态 hash→tenant 映射，operator 启动时显式配置；空表 + Manual First ON 即"无 fallback" Phase 1 也可工作但凭据缺失走 401）。
   - `client_auth_require_credential: bool`（默认 ON：A1 缺凭据 → 401；可显式 opt-out 给极少数 internal smoke 路径但启动时 warn log）。
4. **`listener.rs` 入口改造**：
   - 解析 client credential（mock 分支 / 正常分支均走解析，**但 mock 分支不阻断**——synthesis §7-J Phase 1 = mock/staging，凭据缺失在 mock 分支允许 default-tenant 直通保持现有测试 fixture 兼容；正常分支根据 `client_auth_require_credential` 决定是否 401）。
   - 把派生的 `ClientCredential` 注入 `build_route_query` 参数链。
5. **`account_planner.rs` 写入侧**：
   - `build_route_query` 接收新参 `Option<&ClientCredential>` → 写入 `RouteQueryRequest.client_credential` 字段。
   - **永远不再读 `x-tenant-id` header**（A3 acceptance gate；当前 `tenant_id` 仍允许从 `header_str` 读旁路 fallback default-tenant，按 Phase 1 双写策略保留旧路径但加 `\\\` mutation marker）。
   - Manual First ON：tenant_id 仍按现有逻辑（保留 `default-tenant` 兜底，**不读 `x-tenant-id`**——区分 W11-A D-1b A3 严格约束）；OFF：tenant_id 字段填空字符串 → Go control plane 必须从 credential 派生（Phase 2 Go 上线后切到 OFF）。
6. **`redaction.rs` 扩展**：
   - 新增 `redact_client_credential_for_debug(&ClientCredential) -> &'static str` → 恒返回 `[CLIENT_CREDENTIAL_REDACTED]`。
   - `ClientCredential` 的 `Debug` impl 调用上面 helper（防 trace span auto-format 误泄）。
7. **`mock_control_plane.rs`**：**无需改动**——`last_route_query: Mutex<Option<RouteQueryRequest>>` 已捕获整 message 含新字段；测试可断言 `last.client_credential.is_some()`。
8. **测试（mutation-resistant，按 CLAUDE.md #14 + §8 W11-A A1-A5）**：
   - A1：缺凭据 → listener 直接返回 401，**`mock_control_plane.route_queries_seen` 不增**（route query 未发出）。Mutation: 删 listener 401 短路 → 测试红。
   - A3：请求带 `x-tenant-id: B` + 有效凭据派生 tenant=A → 断言 `last_route_query.tenant_id != "B"`。Mutation: 在 `account_planner` 把 tenant_id source 改回 `header_str(..., TENANT_ID_HEADER)` 优先 → 测试红。
   - A4：raw credential `sk-test-secret-12345` → `format!("{cred:?}")` + log redaction 输出**不含** `12345`/`sk-test-secret`，**包含** `[CLIENT_CREDENTIAL_REDACTED]` 或 prefix 哈希。Mutation: 把 `Debug` impl 改回 `derive(Debug)` → 测试红。
   - A5：Manual First flag ON + Authorization Bearer abc → `RouteQueryRequest.tenant_id == "default-tenant"`（旧路径仍工作）且 `client_credential.is_some()`（双写）；Manual First flag OFF + 同请求 → `tenant_id == ""`（force Go 派生）且 `client_credential.is_some()`。Mutation: ON 时跳过 `client_credential` 写入 → 测试红；OFF 时仍填 default-tenant → 测试红。
   - 解析器单元测试：Bearer / x-api-key / 缺失 / 同时存在（按 Bearer 优先）/ raw cookie 误识别（不应识别）→ 至少 5 fixture。
9. **commit 信息**：单 commit，body 含 Clean-room-attestation + synthesis spec 路径与 A1-A5 gate 引用。

### Out

- **Go control plane runtime 消费侧**：本 commit Go 完全不知道 `client_credential` 字段存在；proto3 增字段对 Go 端二进制透明（向后兼容）。Phase 2 Go 实现 + Owner 启动 Go 线 spec 才推进。
- **Phase 2 双写对账**：Rust 旧 `tenant_id` 与 credential 派生 tenant 的 mismatch 拒绝 + 告警 → Go 侧上线后才能联调。
- **Phase 3 移除旧 header path**：`x-tenant-id` 在 Rust 已 A3 永不信任；不属本轮的"移除 Rust 旧 tenant_id field 默认"动作。
- **γ trusted-header opt-in**（synthesis §7-H 已决：不开放）。
- **真生产可计费流量**（§7-J 默认实施约束：Phase 1 仅 mock/staging/internal-smoke）。
- **Authentication core**（key 库、API key resolver、user/session）—— Go control plane 所有。

## 3. Success criteria（W11-A A1..A5 mutation-resistant gate map）

| Gate | 断言 | mutation | 测试位置 |
|---|---|---|---|
| **A1** | 无 Authorization / x-api-key → listener 公共 401 + route query **不发** | 删 listener `client_auth_require_credential` 401 短路 → A1 测试红 | `tests/client_auth_listener_test.rs` 集成测试 + `mock_control_plane.route_queries_seen == 0` |
| **A2** | 已在 D-1a 闭合 — 本轮不重测 | — | 现有 `account_planner` 单元测试 |
| **A3** | `x-tenant-id: B` + 有效凭据 → `RouteQueryRequest.tenant_id != "B"` | 在 `account_planner` 把 `tenant_id` source 改回 `TENANT_ID_HEADER` → A3 测试红 | `account_planner::tests::x_tenant_id_header_never_trusted_in_d1b` |
| **A4** | `format!("{cred:?}")` + tracing field 输出 **不含 raw credential 字符串** | 把 `ClientCredential` Debug 改 derive → A4 测试红 | `client_auth::tests::debug_impl_never_leaks_raw_credential` |
| **A5** | Manual First ON → `tenant_id="default-tenant"` + `client_credential.is_some()`（双写）；OFF → `tenant_id=""` + `client_credential.is_some()` | ON 时跳过 client_credential 写入 → A5-on 红；OFF 时填 default-tenant → A5-off 红 | `account_planner::tests::manual_first_on_dual_writes` + `manual_first_off_forces_control_plane_derivation` |

外加：
- `cargo build -p core_gateway` 干净（所有 feature 组合）
- `cargo test -p core_gateway --lib --tests` 全绿
- `bash tools/feature-matrix/verify.sh quick`（PR 预审）全绿
- `codex exec review --uncommitted --full-auto` 无 P1 finding（per CLAUDE.md #8 termination criteria：2 轮后 P2/P3 form findings freeze）

## 4. Blast radius（每文件触点）

| 文件 | 变更 |
|---|---|
| `exploratory/rust-core-gateway/merged/proto/route.proto` | +1 message (`ClientCredential`) + 1 字段 (`RouteQueryRequest.client_credential`) — 字段号 10（首个空闲；当前 RouteQueryRequest 用 1..9） |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_auth/mod.rs` | 新文件（facade re-export）|
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_auth/credential.rs` | 新文件（client credential extraction + redaction）|
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_auth/manual_first.rs` | 新文件（ManualFirstConfig + ManualFirstResolver）|
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs` | `pub mod client_auth;` (子模块声明 = 1 root entry，含 3 子文件) |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs` | 解析 + 401 短路 + 注入 build_route_query 参数 |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs` | `build_route_query` 多 1 参 + 写 `client_credential` + Manual First branch tenant_id |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs` | +3 字段 (`client_auth_manual_first_enabled`, `client_auth_manual_first_keys`, `client_auth_require_credential`) + validate + env loader |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/redaction.rs` | +1 helper (`redact_client_credential_for_debug`) |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/client_auth_listener_test.rs` | 新集成测试文件（A1 / A3 fixture） |
| `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs` | **无改动**（已捕获整 RouteQueryRequest，含新字段） |

**包责任检查（per CLAUDE.md #13 + AGENTS.md ~20 文件预算）**（Codex round 1 P1 finding 2026-05-24 修正）：`crates/core_gateway/src/` 当前 19 root `.rs` 文件（不是 v1 claimed 30+；v1 高估含子目录文件）。新增 1 子模块目录 `client_auth/`（含 mod.rs + credential.rs + manual_first.rs 3 子文件，但 root entry +1 directory 不加 root `.rs` 计数）→ src/ root 总 entry = 20，刚好不破预算。**listener.rs / account_planner.rs / config.rs 是 W11/W12 已识别的活动改造区**，本 commit 在其内追加方法/字段属允许形态（synthesis §5.4 "唯一允许的添加形态：已有模块内的新方法/新字段"）。

## 5. Decision points needing Owner sign-off

| # | 决策点 | 推荐 | 替代 | 理由 |
|---|---|---|---|---|
| **D-1** | `ClientCredential.value` 字段类型 | `bytes` (proto bytes → Rust `Vec<u8>`) | `string` | bytes 允许任意 binary credential（未来 mTLS cert fingerprint / opaque token）；string 适合 utf8-only。selecting bytes 不损 utf8 case。**推荐 bytes**。 |
| **D-2** | `ClientCredential.kind` 表达 | flat field `string kind = 1;`（"bearer" / "api_key"） | `oneof` per kind | flat field 更简单、前向兼容（新 kind 加 string value 即可）；oneof 类型化更强但 proto3 演进麻烦。**推荐 flat string**。 |
| **D-3** | 字段号选择 | `RouteQueryRequest.client_credential = 10` | 跳 10-19 留指纹/L1canary 用 → 选 30 | 当前 1..9 用，10 是首个 free；synthesis spec §4.5 P-1 未指定字段号。**推荐 10**（线性紧凑）。 |
| **D-4** | 凭据 kind 收集范围 | Bearer + ApiKey（Authorization / x-api-key）→ 任一存在即派生 | 仅 Bearer | OpenAI 系列用 Bearer，Anthropic 用 x-api-key；HUAKAI 同时承载这两族 vendor → **两者都解析**。Cookie / proxy-authorization 不视为客户端凭据（HUAKAI 数据面 mock 分支已 strip）。 |
| **D-5** | Manual First 配置载体 | env var `HUAKAI_CLIENT_AUTH_MANUAL_FIRST_KEYS_JSON=[{"hash":"sha256:...", "tenant":"acme"}]` | 独立 TOML / JSON 文件路径 | Phase 1 仅 mock/staging 用，env var 部署最简；文件路径增 disk IO 失败维度。**推荐 env JSON**（synthesis §7-J Phase 1 mock-only 场景 env 足够）。Phase 2 Go 上线后此配置整体废弃。 |
| **D-6** | Log redaction 形式 | SHA-256 哈希 first 8 hex chars + kind label：`kind=bearer hash=sha256:a1b2c3d4` | HMAC-SHA-256（需 secret） / 全 `[REDACTED]` | 8-char prefix 给运维找同源请求关联性（同 credential 多次请求同 hash），无 secret 依赖；HMAC 引入新 secret 维度。**推荐 SHA-256 first 8 hex**（synthesis A4 仅要求 hash/prefix，无 HMAC 硬要求）。 |
| **D-7** | Manual First flag 默认值 | **ON**（Phase 1 默认） | OFF | Phase 1 Go 控制面尚未消费 P-1 → OFF 会让所有请求 tenant_id="" Go 派生失败。ON 保持现有 default-tenant 行为同时双写新字段。Go 上线后 Owner 显式切 OFF 进 Phase 2。**推荐 ON**。 |
| **D-8** | `client_auth_require_credential` 默认值 | **ON**（A1 严格 enforce 401） | OFF（向后兼容 dev fixture） | A1 是 acceptance gate；OFF 会让 D-1b 形同虚设。dev fixture 可显式 opt-out 单测；mock 分支按 §7-J 不属 Phase 1 真流量，**listener mock 分支不调用 require 检查**。**推荐 ON**。 |
| **D-9** | 401 response body 形式 | JSON `{"error":{"type":"unauthorized","message":"client credential required"}}` | 空 body / plain text | Anthropic + OpenAI 都用 JSON error envelope；保持 client-side compat。**推荐 JSON**。 |

**Owner 答复栏（实施前必须勾选）**：

- [ ] D-1 ☐ bytes / ☐ string
- [ ] D-2 ☐ flat string / ☐ oneof
- [ ] D-3 ☐ field 10 / ☐ other
- [ ] D-4 ☐ Bearer+ApiKey / ☐ Bearer only
- [ ] D-5 ☐ env JSON / ☐ file path
- [ ] D-6 ☐ SHA-256 first 8 / ☐ HMAC / ☐ pure REDACTED
- [ ] D-7 Manual First default ☐ ON / ☐ OFF
- [ ] D-8 require_credential default ☐ ON / ☐ OFF
- [ ] D-9 401 body ☐ JSON envelope / ☐ other

## 6. Pre-execution checklist

- [ ] 分支 `claude/rust-hardening` 已 check out（无 uncommitted 重要改动；`lib.rs` modified 残留先验证非阻塞）
- [ ] `cd exploratory/rust-core-gateway/merged && cargo build -p core_gateway` 干净（baseline）
- [ ] `cargo test -p core_gateway --lib --tests` 全绿（baseline）
- [ ] `bash tools/feature-matrix/verify.sh quick` 全绿（baseline）
- [ ] synthesis spec §7-H + §7-J 与本 plan §3 acceptance gate 对齐复核
- [ ] Owner 确认 §5 decision points D-1..D-9（无 sign-off 不开 Step 1）
- [ ] Codex 平行稿已产出 → synthesis 对比无 P1 冲突

## 7. Execution order（单 commit grouping per synthesis §6）

> **Synthesis §6 recommended**：「D-1b + Manual First + P-1 字段一起一个 commit」。

1. **proto 改**：编辑 `proto/route.proto` 加 `ClientCredential` message + `RouteQueryRequest.client_credential = 10`；运行 `cargo build -p core_gateway`（触发 `build.rs` 重生成 `route_proto`）→ 验证编译通过。
2. **新子模块 `src/client_auth/`**（mod.rs facade + credential.rs + manual_first.rs）：定义 enum + struct + `from_headers()` + `fingerprint()` + Debug impl + 单元测试（credential.rs 5+ fixture, manual_first.rs 4+ fixture）。
3. **`config.rs` 扩字段**：3 新 field + RawStartupConfig + validate + env loader；单元测试覆盖 ON/OFF + Manual First key parsing + invalid JSON fail-fast。
4. **`account_planner.rs` 写入**：`build_route_query` 多 1 参；写 `client_credential`；Manual First branch tenant_id；增 mutation marker `\\\` 注释；单元测试 A3 + A5。
5. **`listener.rs` 短路 + 注入**：解析 credential → 缺失 + `require_credential=true` → 401（JSON envelope）；解析成功 → 注入 build_route_query；mock 分支不强制 require；集成测试 A1（生产分支 401 + mock 分支不阻断）。
6. **`redaction.rs` 扩展**：helper + 测试 A4。
7. **`tests/client_auth_listener_test.rs`**：A1 + A3 + A4 + A5 集成 fixture（端到端通过 axum router + mock_control_plane）。
8. **`cargo test -p core_gateway --lib --tests`** 全绿验证。
9. **`bash tools/feature-matrix/verify.sh`** full（4 组合全绿）。
10. **`codex exec review --uncommitted --full-auto`** → 修 HIGH/P1（per CLAUDE.md #8 2 round termination for P2/P3 form findings）。
11. **`git add` 指定文件**（不要 `-A`）；查 git status 确认无意外文件。
12. **`git commit`** with body：
    ```
    account_planner + proto 客户端凭据透传与 Manual First 兜底
    
    W11-A D-1b Phase 1（P1-1 per docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md §5.2）。
    实施 Owner 2026-05-23 已决 §7-H β 控制面权威身份 + §4.5 P-1 受控 proto 字段 + §7-J
    Phase 1 mock/staging only 默认约束。
    
    A1 缺凭据 → 401 + route query 未发出。A3 x-tenant-id 永不被信任。
    A4 raw credential 永不进 log（SHA-256 first 8 hex prefix + Debug impl override）。
    A5 Manual First ON 双写 / OFF 强制 Go 控制面派生。
    
    Phase 2 Go 控制面消费 + 双写对账留待 Owner 启动 Go 线 spec。
    
    Clean-room-attestation: original HUAKAI implementation; no copied
    source/comments/tests/schemas from non-permissive references.
    
    Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
    ```
13. **`codex exec review --commit HEAD --full-auto`**（post-commit retro check，CLAUDE.md #8 optional）。
14. **`git push https://github.com/BloomingProsperity/HUAKAI.git claude/rust-hardening`** with transient `-c user.email=... -c user.name=...`（never `git config`）。

## 8. Failure modes + mitigations

| 风险 | 后果 | 缓解 |
|---|---|---|
| Phase 1 误推真生产可计费流量 | β policy 实质破坏；audit/billing 误派 tenant | 启动日志 ON warn message：「W11-A D-1b Phase 1 in effect — Manual First fallback active; do NOT serve billable traffic」；synthesis §7-J 已 bake-in 默认约束 |
| proto 字段号冲突 | 未来加字段重号 → 二进制不兼容 | 选 field 10 是当前首空闲；synthesis spec §4.5 P-1 / P-2 都未占；P-2 (rate_limit_reset_at) 在 AttemptReportRequest 不冲突 |
| credential leak via tracing span auto-format | A4 失守 | `Debug` impl override + 集成测试断言；redact_for_log helper 不暴露 raw value |
| Manual First key 表 stale | tenant 误派 | env var 启动 parse 时 warn log condition entry 数 + hash truncated；operator 显式重启刷新 |
| `x-tenant-id` header sneak back via other code path | A3 失守 | reviewer 检查清单 + 项目级 grep `TENANT_ID_HEADER` 守门测试（任何 `account_planner` 外的 `x-tenant-id` 读取都视为缺陷） |
| Bearer + ApiKey 同时存在歧义 | client 误送二者 → 后端不知用哪个 | 解析器 Bearer 优先；同时存在 → log warn 但不拒绝（Phase 1 dev 容忍）；A3 测试覆盖一种存在另一种缺失场景 |
| Codex per-commit review 出 round-3 finding 拖延 | merge 受阻 | CLAUDE.md #8 2 round termination — P2/P3 form 类 freeze 提 backlog；P1 实质修齐 |

## 9. 自验证 (CLAUDE.md #14)

每个 acceptance gate 必须配 mutation 测试：
- **A1 mutation**：注释掉 `if credentials.is_none() && config.require_credential { return 401 }` → A1 测试期望 401 → 实际 200 → 红。
- **A3 mutation**：把 `tenant_id` 派生改回 `header_str(headers, TENANT_ID_HEADER).unwrap_or("default-tenant")` → A3 测试 expect != "B" → 实际 "B" → 红。
- **A4 mutation**：把 `ClientCredential` `impl Debug for` 改成 `#[derive(Debug)]` → A4 测试 expect "[CLIENT_CREDENTIAL_REDACTED]" → 实际 raw value 字符 → 红。
- **A5-on mutation**：在 `build_route_query` Manual First ON branch 跳过 `client_credential = Some(...)` 写入 → A5-on 测试 expect `last.client_credential.is_some()` → 实际 None → 红。
- **A5-off mutation**：OFF branch tenant_id 仍填 "default-tenant"（不清空）→ A5-off 测试 expect `last.tenant_id == ""` → 实际 "default-tenant" → 红。

Mutation 注释直接刻在源码行旁（参考 listener.rs `MOCK_CREDENTIAL_STRIP_COUNT` 模式：`mutation marker: 删本行 → <test_name> 红`）。

## 10. Change log / 演进

- **2026-05-24 v1**：Claude 起草。基于 Owner directive 「协调 go 和解析逻辑一起做」 + synthesis §5.2 P1-1 + §7-H/J Owner 已决项。等待与 codex 平行稿对比 synthesis。
- **2026-05-24 v2**：Owner 要求新增 3 个 reference project specifier 挖矿（routiium / Helicone/ai-gateway / agentgateway）；落地为 §10.5 引证 + §11 fusion-upgrade delta；§5 决策点 D-4 / D-7 增加新证据对比。

## 10.5 引证（per CLAUDE.md #12 first-cite recency + L0 specifier）

> 本节为 HUAKAI fusion-upgrade 多源比对的源头表。所有 cite 形为 `<owner>/<repo>@<sha-or-HEAD>:<file>` 简写；HEAD pull date 即本 plan 写作日 2026-05-24。

| 引证 ID | 项目 | 许可 | 最新 push | star | HUAKAI 对 D-1b 用到的关键证据 |
|---|---|---|---|---|---|
| **R1** | `Wei-Shaw/sub2api@f59d9a5f` | LGPL-3.0 | 2026-05-24 | 23070 | 服务端身份派生 + 拒绝 URL query 凭据（`backend/internal/server/middleware/api_key_auth.go:32-77`）—— synthesis §2 D-1b 已引 |
| **R2** | `router-for-me/CLIProxyAPI` | MIT | 2026-05-23 | 34467 | （synthesis 已引为 W4/W5 多上游路由参照，本 D-1b 旁证） |
| **R3** | `Xerxes-2/clewdr@57626809` | AGPL-3.0 | 2026-05-09 | 1170 | 单租户、账号选择从不读客户端 header（`src/services/cookie_actor.rs:176-199`）—— synthesis §2 D-1b 已引 |
| **R4** | `lightseekorg/smg@9a93938a` | Apache-2.0 | 2026-05-24 | 284 | 优先级链金本位（synthesis §2 D-1b "采纳 smg 优先级链 + Owner 拍板"） |
| **R5** | `majiayu000/litellm-rs@82d0181` | MIT | 2026-05-23 | 59 | （synthesis 已引为 D-3 SSRF 反面教材，本 D-1b 旁证） |
| **R6 新** | `Helicone/ai-gateway` | Apache-2.0 | 2026-05 active | — | **有 control_plane 模块 + 双模式架构**（paraphrased per CLAUDE.md #11 clean-room lane guard，原始 file:line 锚点保留即可）— `ai-gateway/src/middleware/auth.rs` 仅支持 Bearer 单 kind, 提取后两种策略: cloud 模式走每请求一次的远端身份认证 RPC，self-host 模式走本地哈希索引的 key 表查找; `ai-gateway/src/control_plane/{control_plane_state,websocket,types}.rs` 实现 data plane ↔ control plane 长连接的 key 表全量推送 + 增量更新（push 模型而非 pull）。**与 HUAKAI 同型最近的 Rust 参考** |
| **R7 新** | `labiium/routiium@HEAD` | Apache-2.0 | 2026-04-25 | 23 | **本地认证反面教材**（paraphrased per CLAUDE.md #11，file:line 锚点保留）— `src/auth.rs` 暴露一个 Bearer 验证 helper + 一个内存缓存的查表 helper + 一个抽象的 key 存储 trait（in-memory cache warmed from sled/Redis/memory backend）, 无 control plane delegation; `docs/SECURITY_MODEL.md` 明确「single-operator, 无 tenant 模型」; passthrough vs managed 是 **env var 驱动**（特定 vendor key 环境变量在 = managed, 不在 = passthrough），不是运行期开关 |
| **R8 新** | `agentgateway/agentgateway@HEAD` | Apache-2.0 | 2026-05 active | — | Rust dataplane (`crates/agentgateway/`) + Go controller (`controller/pkg/agentgateway/jwks/{cache,fetcher,resolver,store,collections}.go`) — **JWKS 结构化 JWT 校验**（K8s 风格, 客户端凭据是签名 JWT, 由 dataplane 本地验签）; xDS streaming 推送配置 |
| **R9 新** | `envoyproxy/ai-gateway` | Apache-2.0 | 2026-05-23 | 1677 | K8s 原生, credentials 存 K8s secrets, control plane 管 policy, per-request 注入 — synthesis spec §4.5 P-1 "Phase 1/2/3" 与 envoy 的 K8s secret rotation 同型生态 |

**Mechanical enforcement note**：R6/R7/R8 的 Rust 源码本会话仅供 **specifier 行为提取**, 实施会话只读 HUAKAI 自有代码 + 本 plan; 触发 `cargo-deny` AGPL/LGPL block 守门 + 凭据 token-level similarity scan（CLAUDE.md #11 mechanical enforcement）。

## 11. Fusion-upgrade delta (per CLAUDE.md #12 三维度 + multi-source cite)

| 维度 | HUAKAI W11-A D-1b β + Manual First | Helicone (R6) | routiium (R7) | agentgateway (R8) | envoy-ai-gw (R9) | sub2api (R1) | clewdr (R3) | smg (R4) |
|---|---|---|---|---|---|---|---|---|
| **Identity authority** | **Go control plane via 1-shot gRPC RouteQuery (β)** | Cloud=per-req RPC; self-host=local cache (CP push) | 本地查表（无 delegation） | xDS sync + 本地 JWKS 验签 | K8s secrets + per-route policy | 本地中间件解 key + 加载 ctx | 单租户硬编码 | 优先级链本地决策 |
| **Credential kind 支持** | **Bearer + x-api-key**（Anthropic 强制 x-api-key） | **Bearer only** | Bearer-helper（caller 选） | 签名 JWT (structured) | provider 各异 | Authorization + 厂商 key | cookie/session | 多源 |
| **Phase 1 backstop 形态** | **Manual First static hash 表** + Phase 2 Go 上线后切 + Phase 3 删 | 自托管=local-cache 永久模式 | 本地永久 | xDS pre-load | K8s secret pre-provision | 无 phasing | 无 phasing | 无 phasing |
| **Cross-plane RPC 形态** | **1-shot gRPC RouteQuery 返 routing + UpstreamAuthMaterial 同 RPC**（一跳） | 多步: 长连接 WS state sync + per-req authz call | 无（local） | xDS streaming + per-req JWKS verify | per-route policy + K8s secret lookup | 内中间件 | 无 cross-plane | 无 cross-plane |
| **Transport baseline** | **UDS-default (Linux 内核, ~6μs)** | TCP + WebSocket | N/A | xDS gRPC | gRPC | HTTP loopback | HTTP local | gRPC |
| **`x-tenant-id` 不变量** | **永不被信任 (A3 hard gate + reviewer 守门)** | 未表态 | 未表态（单租户） | 未表态 | K8s 边界天然防 | 拒 URL query (类似纪律) | 不适用 | 未表态 |
| **Log redaction** | **SHA-256 first 8 hex prefix + Debug impl override**（A4 mutation 守门） | （未深挖, `types/secret.rs` 有专用类型） | 未表态 | JWT claim 部分可 log | K8s audit | 已有 redact | 已有 redact | 已有 redact |

**HUAKAI fusion-upgrade delta（3 维度归类，per CLAUDE.md #12 architecture-self-research clarification）**：

1. **架构升级**：
   - **单 RPC RouteQuery 合并 routing + upstream auth material**——避免 Helicone 双 RPC（WS sync + per-req authz）的 race condition 与 envoy/agentgateway 多步配置同步的时延窗口；
   - **β strict: data plane 严格无身份权威**——vs Helicone 自托管模式 data plane 缓存全套 key 表（违反 zero-trust dataplane 原则）、vs routiium 本地永久；
   - **UDS-default transport**（Linux 原生 IPC, 不暴露网络面）——vs Helicone WebSocket 暴露需 mTLS 包；
   - **Manual First → Phase 2 → Phase 3 显式三阶段 rollout**——其它项目要么"永久本地"（routiium、clewdr）要么"永久远程"（Helicone cloud、envoy-ai-gw），无明确退路。

2. **算法升级**：
   - **β 把 "credential opaque pass-through" 与 "control-plane-derived tenant" 解耦**——vs Helicone 双模式硬编码（cloud/self-host 在编译时绑死）、routiium 本地查表与 routing 耦合、agentgateway JWKS 解码假设结构化 JWT；
   - **A3 `x-tenant-id` permanent untrust 不变量**——其它项目无此 acceptance gate（sub2api 拒 URL query 凭据但允许某些 header）；
   - **A4 SHA-256 first 8 hex prefix 同时承担"审计相关性"+"防 raw leak"**——比 Helicone `types/secret.rs` 类型隔离更细粒度（fixed-length fingerprint 防 length oracle）。

3. **生态升级**：
   - **Phase 1/2/3 rollout 与 §4.5 P-1 proto field 演进绑定**——synthesis spec 显式列每 phase 的 Go control plane 责任，跨线协调单点化；其它项目无此 cross-plane phasing 纪律；
   - **per-vendor 凭据隔离 + per-request route auth material**——control plane 按 vendor 注 `UpstreamAuthMaterial`（已有 P-1 字段, route.proto:40-45）；vs Helicone gateway-side pre-configured static key、agentgateway xDS 推 static route binding；
   - **mock_control_plane 已就位的"Phase 1 测试 sandbox"**——可直接 assert `last_route_query.client_credential` 而无需新 stub；其它项目 fusion 时需自建。

**Anti-pattern 自检**（CLAUDE.md #12 "no project at our precision does Y"）：
- "no project at HUAKAI's precision does **single-RPC routing + upstream auth + Manual First phased rollout + UDS transport + x-tenant-id permanent-untrust unit-tested gate**" —— 精度维度：跨线 RPC 调用次数（HUAKAI=1, Helicone≥2, agentgateway≥2）+ 显式 phasing（HUAKAI 3 阶段, 其它无）+ 不变量 unit-tested（HUAKAI A3 mutation-resistant, 其它无表态）。

## 12. §5 决策点对新引证的更新

基于 §10.5 + §11 重新校准（**只列发生变化的决策点**）：

- **D-4（凭据 kind 范围）确认**：Helicone (R6) Bearer-only 是 OpenAI 系唯一足够；HUAKAI 必须 Bearer + x-api-key（Anthropic 用后者）—— **fusion delta = 多 vendor 协议跨族支持**，推荐不变（Bearer + ApiKey）。
- **D-7（Manual First flag 默认）警报**：Codex 平行稿 + routiium SECURITY_MODEL 都倾向 OFF；Helicone 自托管"永久本地"模式作反面教材（结构化"data plane 持身份权威"违反 β）。**修正推荐**：Manual First 默认 OFF + 生产模式启动时 reject ON（除非 Owner 单独 emergency override）；Phase 1 dev/staging fixture 通过显式 `HUAKAI_MANUAL_FIRST_ENABLED=true` opt-in。原 v1 推荐 ON 是错的——若 ON 默认会让 production canary 流量在 Go 未上线时跑过 Manual First 静态表（绕过 β policy）。
- **D-5（Manual First 配置载体）警报**：原 v1 推荐 env JSON; Codex 平行稿推荐 JSON file（`HUAKAI_MANUAL_FIRST_KEYS_FILE` 指向磁盘文件, 内含 hashes 而非 raw secrets）。fusion 评估：env 容易在 systemd unit / docker compose 泄漏；file path + hash-only entry 更接近 routiium 的「抽象 key 存储 trait + 磁盘后端」模式（local lookup 但 disk 兜底，file:line 锚点 `src/auth.rs`）+ Helicone 的「control plane 外部维护 key 表 + 推送 dataplane」模式（file:line 锚点 `ai-gateway/src/control_plane/`）。**修正推荐**：JSON file + entries 是 `{kind, secret_sha256, tenant_id, label}`（hash-only）, raw secret 永不入 env / config。
- **D-6（log redaction）**：Codex 平行稿明确指出 sha2 是新依赖，需 Owner 批；fusion 评估：Helicone `types/secret.rs` 是专用类型隔离（不依赖 sha2），routiium 用 trait + cache（不暴露 raw）。**修正补充**：sha2 dep 是 Owner 决策项，否则改用 `&str` 的 length-bounded raw prefix（前 4 char + `...` + 后 4 char）作 audit fingerprint 备选——但 length oracle 风险高于 SHA-256 prefix。**推荐 sha2 + 8 hex** 仍优；Owner 拒 sha2 → fallback 用 internal `xxhash`-style 简易哈希（已在 cargo workspace 间接依赖？需复核）。

---

**Clean-room-attestation**: original HUAKAI plan; reads only synthesis spec + Owner decisions + HUAKAI L0 source + behavior summaries from R6/R7/R8/R9 specifier dig (no source/comment/test/schema copying from any reference); cite-only architectural patterns per CLAUDE.md #12 multi-source fusion discipline.
