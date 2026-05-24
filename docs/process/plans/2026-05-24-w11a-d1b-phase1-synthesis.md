# W11-A D-1b Phase 1 — synthesis (claude + codex + 3 新引证)

> **Owner directive (2026-05-24)**：「协调 go 和解析逻辑一起做」+ 后续追加「(A) routiium / helicone / agentgateway 3 个新项目按 specifier 车道再挖一遍, fusion-upgrade delta 重写 plan §2 + §5, 再 codex 平行稿对比」
>
> **本稿**：综合 `2026-05-24-w11a-d1b-phase1-claude.md` v2（含 §11 fusion-upgrade delta）+ `2026-05-24-w11a-d1b-phase1-codex.md`。共识度 ~85%，3 处主要差异已裁定，5 处 gap 互补全部采纳。Owner 决策点合并为 6 项（剩余争议点）。

## §1 双稿共识（直接 lock）

两稿一致 lock 的设计：

1. **Scope in**：proto `RouteQueryRequest` +1 `client_credential` 字段；Rust 新模块解析 Authorization / x-api-key；listener 401 短路；account_planner 注入；redaction helper；Manual First 静态 hash→tenant 映射；mock_control_plane 已捕获新字段不需改。
2. **Scope out**：Go control plane runtime 消费（Phase 2）；真生产可计费流量（§7-J）；γ trusted-header；Rust user/auth 系统；git config 改动。
3. **Acceptance gates A1..A5** mutation-resistant：A1 缺凭据 → 401 + route query 未发出；A2 D-1a 已闭合不重测；A3 `x-tenant-id` 永不被信任；A4 raw credential 不进 log；A5 Manual First ON/OFF 双 branch 测试。
4. **Field number**：`client_credential = 10`（首空闲字段号，1..9 已用）。
5. **执行顺序骨架**：proto 改 → build 重生成 → 新模块加 → config 扩字段 → listener 短路 → account_planner 注入 → redaction → tests → cargo test 全绿 → codex review → 单 commit + Clean-room-attestation。
6. **单 commit grouping**：synthesis §6 "D-1b + Manual First + P-1 字段一起一个 commit"。
7. **Manual First 生产模式守门**：生产模式启动时若 Manual First ON 则 fail-fast（除非 Owner emergency override）。
8. **新引证（§10.5 R6/R7/R8/R9）核验**：5 个 synthesis 已引项目 + 4 个新引证全部在 90 天内 active（one-api 唯一 stale 不依赖）。
9. **HUAKAI fusion-upgrade delta**（3 维度归类，见 claude v2 §11）：单 RPC RouteQuery + β strict + UDS + 三阶段 phasing + A3 不变量 unit-tested，**无任何 reference project at our precision does this combination**。

## §2 差异 + 裁定

### §2.1 Manual First 默认值（争议 → 已收敛）

| 稿 | 推荐 |
|---|---|
| Claude v1 | **ON**（Phase 1 默认 Go 未上线时 fallback default-tenant） |
| Codex | **OFF**（OFF 时 client_credential 写新字段 / tenant_id 留空 / Phase 1 contract smoke） |
| Claude v2（依 §11 fusion + Helicone 反面教材） | **OFF + 生产 reject ON 启动守门** |

**裁定**：**OFF**。理由收敛 3 处证据（Codex round 2 P1 finding 2026-05-24 修: 加 `<repo>@<HEAD-pull-date>:<file>` 引证锚点）：
- Codex 平行稿独立结论 OFF（见 `docs/process/plans/2026-05-24-w11a-d1b-phase1-codex.md` §"Default Manual First flag value"）
- `Helicone/ai-gateway@HEAD-2026-05-24:ai-gateway/src/control_plane/control_plane_state.rs` 显示 self-host 模式 data plane 持身份权威（key 表本地缓存 + control plane push update），与 β"data plane 无身份权威" 直接冲突 → 反面教材
- `labiium/routiium@HEAD-2026-05-24:docs/SECURITY_MODEL.md` 明确「single-operator deployments, 无 tenant/identity 多租户框架」——HUAKAI 是多租户网关, 不能复用其单租户默认

**实施细节**：
- `client_auth_manual_first_enabled` 默认 `false`
- 生产模式（`HUAKAI_RUNTIME_MODE=production`）+ Manual First ON → 启动 fail-fast 拒绝（除非 Owner 显式 `HUAKAI_MANUAL_FIRST_PRODUCTION_OVERRIDE=true` + warn log）
- Phase 1 dev/staging/internal-smoke 通过显式 ON opt-in

### §2.2 ClientCredential 表达形态（争议 → Codex 胜）

| 稿 | 推荐 |
|---|---|
| Claude v1+v2 | 嵌套 `ClientCredential` message（`kind` enum + `value` bytes + `header_source` string） |
| Codex | flat `string client_credential = 10`，canonical `"bearer:<token>"` / `"x-api-key:<key>"` |

**裁定**：**Codex 的 flat string + kind-prefix**。理由：
- proto3 nested message 占字段 + 增 Go 端 unmarshal 复杂度（Phase 2 Go 实现成本提高）；
- flat string 前缀已天然类型化（Go regex match `^bearer:|^x-api-key:` 足够）；
- HUAKAI proto field budget 已紧（route.proto 137 行），减少 message 体；
- Helicone (R6) 也是把 credential 当 opaque string 在 control plane 间传输（control-plane → dataplane 的 key 表增量更新消息载荷也是 string，paraphrased per CLAUDE.md #11，file:line 锚点 `ai-gateway/src/control_plane/control_plane_state.rs` 保留）。

**Rust 端封装**：仍保留 Claude v1 提议的 `ClientCredential` struct + enum `ClientCredentialKind` 作 in-memory 类型化；序列化到 proto 时 `as_route_proto_value() -> String` 走 flat。

### §2.3 文件拆分（Codex round 1 P1 finding 2026-05-24 修正：必须用子模块）

| 稿 | 推荐 |
|---|---|
| Claude | 单 `crates/core_gateway/src/client_auth.rs`（~150 行） |
| Codex | `client_credential.rs` + `manual_first.rs` 两 root file |
| **Codex per-commit review round 1 P1**（2026-05-24）反对 | src/ 当前 19 root `.rs`, +2 = 21 跨 AGENTS.md ~20 文件预算; 必须改为子模块 |
| **修正裁定** | **拆 2 文件但放进子模块 `src/client_auth/`**（含 `mod.rs` + `credential.rs` + `manual_first.rs`）→ src/ root +1 directory 不加 root `.rs` 计数 |

**裁定**：**Codex 拆 2 文件 + 放进 `src/client_auth/` 子模块**。理由：
- CLAUDE.md #13 责任分离 — credential extraction（HTTP 解析）vs Manual First config 解析（disk JSON parse）是两种 responsibility
- AGENTS.md ~20 文件预算 — `src/` 当前 19 root `.rs`, 增 1 directory `client_auth/` 比加 2 root `.rs` 更合规
- 测试按文件隔离更清晰 — credential.rs 单测 + manual_first.rs 单测各自一组
- mod.rs 仅作 facade re-export `pub use credential::{ClientCredential, ClientCredentialKind, ClientCredentialError}; pub use manual_first::{ManualFirstConfig, ManualFirstResolver};`

### §2.4 sha2 dep 加入（Codex 显示主动 flag, Owner 决策项）

Codex 平行稿明确把 `sha2` crate 列为 Owner 决策项；Claude v1/v2 默认假定可用。

**裁定**：**升为 Owner 决策点 D-10**（见 §3）。fallback：若 Owner 拒 sha2，redaction 改用 raw prefix（前 4 + `...` + 后 4），但 length oracle 风险升级。

### §2.5 build.rs skip_debug + route_proto/redacting_debug.rs（Codex 新发现）

Codex 平行稿指出 HUAKAI 已有 `route_proto/redacting_debug.rs` 模式（专门为 RoutePlan / UpstreamAuthMaterial 等敏感 message 做手写 Debug impl）；A4 应复用此 pattern，**proto build.rs 加 `.skip_debug(".huakai.route.v1.RouteQueryRequest")`** 阻止自动生成 Debug，然后在 redacting_debug.rs 手写 Debug 渲染 credential 仅为 fingerprint。

**裁定**：**采纳 Codex 此发现**。Claude v1 漏读 redacting_debug.rs 模块；这是更架构正确的做法（已有 pattern, 不引入新 Debug 实现风格）。

### §2.6 unknown credential + Manual First ON 处理（Codex 新决策点）

Codex 平行稿提出：Manual First ON 时若 credential hash 不在静态表内 → 行为是 fail-closed 401 还是 forward 给 Go 让 Go 派生？

**裁定**：**fail-closed 401**（Codex 推荐）。理由：
- Manual First 是 Phase 1 canary authority，map 内没有 = 未被授权
- 若 forward → Go 在 Phase 1 不消费 → 实际跑 default-tenant 流量违反 §7-J
- A1 acceptance gate 已要求"凭据无效 → 401"，扩展到"凭据未登记 → 401"是一致的

升为 Owner 决策点 D-11。

### §2.7 both-present (Authorization + x-api-key) 处理（Codex 新决策点）

Codex 平行稿提出：两个 header 都在时 → fail-closed 401 还是 Bearer 优先？

**裁定**：**fail-closed 401 `ambiguous_client_credential`**（Codex 推荐）。理由：
- 客户端误配两个 header → 静默选一个会让 audit 路径不一致
- HUAKAI 双协议（Anthropic x-api-key + OpenAI Bearer）每条路径只该有一种 credential
- Claude v1 推荐 "Bearer 优先 + warn log" 留隐性歧义路径，反而违反 A1 严格性
- Helicone (R6) 只支持 Bearer 没此问题；routiium (R7) 是 library 由 caller 处理——HUAKAI 无 caller layer，必须 fail-closed

升为 Owner 决策点 D-12。

## §3 最终 Owner 决策点（6 项, 已收敛）

> 推荐项已 highlight；Owner 在 ☐ 打勾即可（或写 "全部默认推荐" 一句话）。

| # | 决策点 | 推荐 | 替代 | 关键证据 |
|---|---|---|---|---|
| **D-3** | proto 字段号 | **`client_credential = 10`** | 跳 10-19 留指纹用 → 选 30 | 1..9 已用；§4.5 P-1 未指定；P-2（rate_limit_reset）在 AttemptReportRequest 不冲突 |
| **D-7** | Manual First flag 默认 | **OFF**（生产模式 ON 启动 fail-fast） | ON | §2.1 三处证据收敛：Codex+Helicone 反面+routiium 单租户无 tenant 模型 |
| **D-8** | `client_auth_require_credential` 默认 | **ON**（A1 严格 401） | OFF | A1 acceptance gate 强制 enforce；OFF 让 D-1b 形同虚设 |
| **D-10 新** | 加 `sha2` crate dep 做 SHA-256 fingerprint | **批准 sha2** | 拒绝 → fallback raw prefix（length oracle 风险） | Codex 平行稿主动 flag；workspace `unsafe_code = "forbid"` 不冲突；Helicone `types/secret.rs` 是另一种 isolation pattern 非 fingerprint |
| **D-11 新** | unknown credential + Manual First ON | **fail-closed 401** | forward 给 Go 派生（Phase 1 Go 不消费 → 跑 default-tenant 流量违反 §7-J） | Codex 平行稿推荐；与 A1 严格性一致 |
| **D-12 新** | both-present (Authorization + x-api-key) | **fail-closed 401 `ambiguous_client_credential`** | Bearer 优先 + warn log（Claude v1） | Codex 平行稿推荐；HUAKAI 双协议每条路径只该一种 credential；audit 路径一致性 |

**Owner 答复栏**：

- [ ] D-3 ☐ field 10 / ☐ other：____
- [ ] D-7 Manual First default ☐ OFF / ☐ ON
- [ ] D-8 require_credential default ☐ ON / ☐ OFF
- [ ] D-10 sha2 dep ☐ 批准 / ☐ 拒绝 fallback：____
- [ ] D-11 unknown + Manual First ON ☐ fail-closed 401 / ☐ forward
- [ ] D-12 both-present ☐ fail-closed 401 / ☐ Bearer 优先 warn
- 或 ☐ **全部默认推荐**

## §4 双稿已收敛、Owner 不需决策的项

下列已 lock 不上 §3（双稿一致或新引证已裁定）：

- **D-1 字段类型**：`string`（per §2.2 + Codex + Helicone opaque-string 同型）
- **D-2 kind 表达**：flat string canonical `"bearer:<token>"` / `"x-api-key:<key>"`（per §2.2）
- **D-4 凭据 kind 范围**：Bearer + ApiKey（Anthropic 强制 x-api-key, Helicone Bearer-only 不够覆盖）
- **D-5 Manual First 配置载体**：JSON file `HUAKAI_MANUAL_FIRST_KEYS_FILE` 含 `{kind, secret_sha256, tenant_id, label}` 条目（hash-only，raw secret 永不入 config）—— Codex+Helicone+routiium 三方一致
- **D-6 redaction 形式**：SHA-256 first 8-16 hex prefix（per D-10 一并决定；fallback 见 D-10）
- **D-9 401 body**：JSON envelope `{"error":{"type":"unauthorized","message":"..."}}` 兼容 Anthropic+OpenAI client（双稿一致）

## §5 文件触点（双稿合并）

```text
proto/route.proto                                              -- +1 field
crates/core_gateway/build.rs                                   -- +skip_debug RouteQueryRequest
crates/core_gateway/Cargo.toml                                 -- +sha2 dep (D-10 Owner 2026-05-24 "全部默认" 已批; license MIT/Apache-2.0 dual, RustCrypto org maintained; cargo-deny 不冲突)
crates/core_gateway/src/lib.rs                                 -- pub mod client_auth;
crates/core_gateway/src/client_auth/mod.rs                     -- 新文件: facade re-export (mod credential; mod manual_first; pub use ...)
crates/core_gateway/src/client_auth/credential.rs              -- 新文件 (~150 行): kind enum + struct + from_headers + fingerprint
crates/core_gateway/src/client_auth/manual_first.rs            -- 新文件 (~120 行): ManualFirstConfig + ManualFirstResolver + JSON file loader
crates/core_gateway/src/config.rs                              -- +3 field + validate + env loader
crates/core_gateway/src/listener.rs                            -- 解析 + 401 + 注入 RouteIdentity
crates/core_gateway/src/account_planner.rs                    -- +1 参 build_route_query + Manual First branch + 删 TENANT_ID_HEADER
crates/core_gateway/src/route_proto/redacting_debug.rs        -- +RouteQueryRequest 手写 Debug (fingerprint only)
crates/core_gateway/src/redaction.rs                           -- +redact_client_credential_for_debug
crates/core_gateway/src/mock_control_plane.rs                  -- 无改 (last_route_query 自动捕获新字段)
crates/core_gateway/tests/listener_test.rs                     -- A1 + D-11 + D-12
crates/core_gateway/tests/route_client_test.rs                 -- A3 + A5
crates/core_gateway/tests/observability_test.rs                -- A4 redaction
```

**包责任检查（CLAUDE.md #13 + AGENTS.md ~20 文件预算）**：`crates/core_gateway/src/` 当前 19 root `.rs` 文件; 本 commit 增 1 directory entry `client_auth/`（含 3 文件 mod.rs + credential.rs + manual_first.rs）, root `.rs` 计数仍 = 19, directory entry +1 → src/ root 总 entry = 20 (vs Codex round 1 P1 fixed: 不再用 root-level `client_credential.rs` + `manual_first.rs` 直接 +2 root `.rs` 撑破 21)。`git status` 时确认无 unintended drift。

## §6 执行顺序（synthesis 定稿）

> 单 commit, body 含 Clean-room-attestation + 引用本 synthesis 路径 + §3 决策 ID 列表。
> **测试 fixture 用显式 fake 占位符**：`FAKE-d1b-bearer-fixture` / `FAKE-d1b-apikey-fixture` —— 不带任何真 vendor key 前缀，避免 secret scanner 误报 + 防 fixture 误入 prod。

1. **baseline**: `cd exploratory/rust-core-gateway/merged && cargo build && cargo test` 全绿
2. **proto P-1**: 加 `string client_credential = 10` 到 RouteQueryRequest
3. **build.rs**: `.skip_debug(".huakai.route.v1.RouteQueryRequest")`
4. **cargo build** 触发 prost regen + 所有 RouteQueryRequest literal 补 `client_credential: String::new()`
5. **`src/client_auth/mod.rs` + `src/client_auth/credential.rs`**: mod.rs facade `mod credential; mod manual_first; pub use credential::{ClientCredential, ClientCredentialKind, ClientCredentialError}; pub use manual_first::{ManualFirstConfig, ManualFirstResolver};` + credential.rs 含 enum kind + struct + `from_headers()` + `as_route_proto_value()` + `fingerprint()` + 单元测试（5+: missing / Bearer / x-api-key / both-present → ambiguous / cookie-not-credential / oversize）
6. **`src/client_auth/manual_first.rs`**: `ManualFirstConfig` + `ManualFirstKeyEntry` + JSON file loader + `ManualFirstResolver::resolve_tenant(&fingerprint)` + 单元测试（4+: file 不存 / 空 entry / 匹配命中 / hash 不匹配 → None）
7. **config.rs**: 3 字段 + env loader（`HUAKAI_MANUAL_FIRST_ENABLED`, `HUAKAI_MANUAL_FIRST_KEYS_FILE`, `HUAKAI_CLIENT_AUTH_REQUIRE_CREDENTIAL`）+ validate 生产模式 ON 守门 + 单元测试（dev OK, prod OFF + Manual First ON → fail-fast, valid JSON file parses）
8. **route_proto/redacting_debug.rs**: RouteQueryRequest 手写 Debug 渲染 credential 仅 `fingerprint=bearer:sha256:abcd1234`
9. **redaction.rs**: `redact_client_credential_for_debug()` helper
10. **account_planner.rs**:
    - 删 `header_str(headers, TENANT_ID_HEADER)` 任何调用（A3 守门）
    - `build_route_query` 多 1 `&RouteIdentity` 参
    - Manual First ON + map 命中 → `tenant_id = resolved`；Manual First OFF / map 未命中 → `tenant_id = String::new()`（强制 Go 派生）
    - 写 `client_credential` 字段（mandatory）
    - 单元测试 A3 + A5（ON / OFF 各 1）
11. **listener.rs**:
    - 解析 `ClientCredential::from_headers(headers)` → 缺失 + require ON → 401 JSON envelope（mock 分支跳过 require 检查保持现有 fixture）
    - both-present → 401 `ambiguous_client_credential`（D-12）
    - Manual First ON + map 未命中 → 401（D-11）
    - 解析成功 → 构造 `RouteIdentity { client_credential, manual_first_tenant_id }` 注入 build_route_query
    - 集成测试 A1（生产分支 401 + `route_queries_seen == 0`；mock 分支不阻断）
12. **tests/observability_test.rs**: A4 — `format!("{:?}", route_query_request)` 不含 raw fixture string `FAKE-d1b-distinctive-token-DO-NOT-LOG`；含 `fingerprint=` 子串
13. **cargo test -p core_gateway --lib --tests** 全绿
14. **bash tools/feature-matrix/verify.sh** 4 组合全绿
15. **codex exec review --uncommitted --full-auto** → 修 HIGH/P1（CLAUDE.md #8 2 round termination）
16. **git add 指定文件**（**不要 -A**, 避免误带 lib.rs 残留改）
17. **git commit**（commit body 见 §6 模板，含 Clean-room-attestation）
18. **git push** via transient `-c user.email=... -c user.name=...` 到 `https://github.com/BloomingProsperity/HUAKAI.git claude/rust-hardening`
19. **post-commit retro**: `codex exec review --commit HEAD --full-auto`（CLAUDE.md #8 optional）

## §6.5 Commit body 模板

```text
account_planner + proto 客户端凭据透传与 Manual First 兜底

W11-A D-1b Phase 1 (P1-1 per docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md §5.2).
实施 Owner 2026-05-23 已决 §7-H β 控制面权威身份 + §4.5 P-1 受控 proto 字段 + §7-J
Phase 1 mock/staging only 默认约束.

综合 docs/process/plans/2026-05-24-w11a-d1b-phase1-synthesis.md §3 决策点:
- D-3 field 10 / D-7 Manual First OFF + 生产守门 / D-8 require_credential ON /
  D-10 sha2 / D-11 unknown+Manual First ON fail-closed / D-12 both-present fail-closed

A1 缺凭据 → 401 + route query 未发出 (mutation: 删 listener 守门 → 红).
A3 x-tenant-id 永不被信任 (mutation: 回读 header → 红).
A4 raw credential 永不进 log (SHA-256 first 8 hex prefix + 手写 Debug; mutation: derive Debug → 红).
A5 Manual First OFF 强制 Go 派生 / ON 双写 (mutation: ON 时跳 client_credential → 红).

Phase 2 Go 控制面消费 + 双写对账留待 Owner 启动 Go 线 spec.

Clean-room-attestation: original HUAKAI implementation; no copied
source/comments/tests/schemas from non-permissive references.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
```

## §7 风险登记

- **R1 Phase 1 误推真生产可计费流量** → 启动日志 ON warn + 生产模式 ON 启动 fail-fast（D-7 兜底）+ synthesis §7-J bake-in 默认约束
- **R2 sha2 dep 加入引 review 反弹** → D-10 显式 Owner 批准；fallback raw prefix 提案备用
- **R3 proto 字段号未来冲突** → 选 10 是当前首空闲；synthesis §4.5 P-1/P-2 不冲突
- **R4 credential leak via tracing span auto-format** → A4 Debug override + 集成测试断言 + grep reviewer 检查清单
- **R5 Manual First key 表 stale** → file path 而非 env，operator 显式重启刷新；启动 parse 时 warn entry 数 + hash truncated
- **R6 `x-tenant-id` header sneak back via other code path** → A3 mutation 单元测试 + reviewer grep `TENANT_ID_HEADER` 守门
- **R7 both-present 误判为单 credential** → D-12 fail-closed 401 ambiguous，A1 测试覆盖
- **R8 Codex per-commit review 出 round-3 finding 拖延** → CLAUDE.md #8 2 round termination；P1 实质修齐

## §8 与现有 docs 的关系

- **取代**：无（claude.md / codex.md 历史平行稿保留作演进证据）
- **补充**：`docs/process/plans/2026-05-22-rust-hardening-plan.md` §2 D-1b + §4.5 P-1 + §7-H/J → 本稿是 W11-A D-1b Phase 1 实施 source of truth
- **新增**：本 synthesis + claude.md v2 (含 §11 fusion-upgrade delta) + codex.md
- **关联**：`docs/process/clean-room/mechanical-enforcement.md`（M1-M5）; `docs/process/citation-cleanup-backlog.md`（P2/P3 form findings backlog）

## §9 Change log

- **2026-05-24 v1**：synthesis 创建。基于 Claude v2（含 §11 fusion-upgrade delta + R6/R7/R8/R9 specifier）+ Codex 平行稿。共识 ~85%，3 处差异裁定，5 处 gap 互补；Owner 决策点收敛为 6 项（D-3/D-7/D-8/D-10/D-11/D-12）。

---

**Clean-room-attestation**: original HUAKAI synthesis; reads only HUAKAI L0 source + claude/codex parallel drafts + behavior summaries from R6/R7/R8/R9 specifier dig; no copied source/comments/tests/schemas from any reference project.
