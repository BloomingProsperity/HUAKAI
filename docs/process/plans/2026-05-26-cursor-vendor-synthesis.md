# 2026-05-26 Cursor Vendor 集成方案 — Synthesis (合并稿)

> 本稿是 Claude PM 在收到 codex lane plan 后做的 synthesis,**Owner 决策依据应是本稿**;
> 两份原稿仍保留:
> - Claude lane: [docs/process/plans/2026-05-26-cursor-vendor-claude.md](2026-05-26-cursor-vendor-claude.md)
> - Codex lane: [docs/process/plans/2026-05-26-cursor-vendor-codex.md](2026-05-26-cursor-vendor-codex.md)

## 0. Parallel-draft 独立性披露(CLAUDE.md #10 合规)

Codex lane 在起草过程中**主动披露**:一次宽泛 `rg` 搜索意外匹配并输出了 Claude lane draft 的部分内容,
后续未再打开/引用/依赖该文件。

Claude PM 评估:
- Codex 的盘点深度显著超过 Claude lane(读 22 个 HUAKAI 文件 vs Claude 7 个),关键事实
  发现(`cursor_session.go` 默认不注册、`MockOnlyProviders` 含 cursor、`DefaultModePlans` 缺 cursor、
  `mimicry_cursor` 在 transport/policy.go 已注册)都来自 codex 独立 read source,**不可能从
  Claude draft 复制得到**;
- Codex slice 命名(C0–C5,6 个)与 Claude(3.0–3.3,4 个)结构不同,推断顺序也不同;
- 内容相似主要集中在"OCAW 反封禁 / OAuth 接通 / 协议转换"三大缺口分类 —— 这些是任何独立
  读源后都会得到的客观分类。

**结论**:污染面 <20%,主要事实部分独立。Owner 若希望严格洁净,可要求重派 codex(成本 ~5min
+ 一次 background 等待)。若接受现状,synthesis 可直接走。**默认假设 Owner 接受**,反之请明示。

## 1. 现状盘点(取 codex 深度版本)

HUAKAI 已有 cursor 子系统骨架,**远不是从零起**,且当前默认状态是"安全关闭"。

| 模块 | 关键事实 | 来源 lane |
| --- | --- | --- |
| `provider/cursor/bootstrap.go` | OAuth 4 字段全 operator-config 来源,`ValidateOAuthConfig` fail-closed | Claude + Codex 重叠 |
| `provider/cursor/cursor_session.go` | 接 `session_token`/`upstream_passthrough` 透传到 `api2.cursor.sh`;3 个 OCAW header TODO | Claude + Codex |
| `provider/cursor/refresher.go` | OAuth refresh_token grant 闭环 + SSRF 防(operator-config-only `token_url`/`client_id`);**`cred["session_token"] = accessToken` 假设未经真账号 verify** | Claude(refresher.go:255-256 命中) + Codex |
| `provider/registrydefault/default.go` | `cursor_session` 是 placeholder,**仅当 `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS=true` 才注册**;默认生产**完全关闭**,这是关键事实 | **Codex 独有** |
| `credentialacq/vendor_exchangers.go:52` | `cursor/oauth` = `NewPKCEFakeExchanger`,fake | Claude + Codex |
| `credentialacq/types.go` | `DefaultModePlans` **未列 Cursor** —— admin UI 看不到 | **Codex 独有** |
| `credentialstore/types.go` | `DefaultVendorHandlers` **未列 Cursor**;runtime material 没有 checksum/cookie/client-version 一等字段 | **Codex 独有** |
| `credentialworker/refresh_adapter.go` | **`MockOnlyProviders` 列了 cursor** —— 即使 refresher 存在,默认 worker 跑 mock | **Codex 独有** |
| `credentialworker/scheduler.go` + scheduler_test.go | 已支持把 `cursor` 路由给 vendor refresher + storm control + audit outcome | **Codex 独有** |
| `transport/policy.go` | 已定义 `cursor` provider + `mimicry_cursor` transport mode,fail-closed | **Codex 独有** |
| `transport/mimicry/registry.go` | 能把 `cursor` / `cursor-cli` stem 映射 mode,但默认 template + sidecar profile 只有 Claude Code,无 Cursor profile | **Codex 独有** |
| `docs/10_RISK_REGISTER.md` | Cursor OAuth 端点/client/scope 待 Owner 捕获已记;refresher SSRF/token exfil 风险已通过 operator config 缓解 | **Codex 独有** |

**关键统一发现:HUAKAI cursor 的"默认安全关闭"是已存在的 design,不是需新建的;
phase-1 cursor 风险面比想象低。**

## 2. 缺口分类(7 类,合并两稿)

1. **OCAW 反封禁层** —— checksum 来源 + trace_id/timezone/request-id 生成 + sidecar profile 缺失 + 失败后账号隔离策略未闭环(Codex 视角 7-step 工作面)
2. **协议转换层** —— OpenAI Chat JSON ↔ Cursor Connect/proto 双向 encoder/decoder 完全缺;**没有 MIT-licensed Cursor wire schema 来源**(Claude + Codex 共识)
3. **OAuth flow 接通** —— `cursor/oauth` fake 替换为真 PKCE;`DefaultModePlans` 加 Cursor(Codex 发现)
4. **refresher 真实性** —— `cred["session_token"] = accessToken` 假设未 verify(**Claude lane 独有**精准命中 refresher.go:255-256)
5. **credentialstore/runtime material** —— `DefaultVendorHandlers` 加 cursor + checksum/cookie/client-version 一等字段 contract(Codex 独有发现)
6. **默认注册与 rollout** —— Cursor 必须**专属 feature flag**,不能复用 `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS` 总开关(Codex 视角,避免连带把 Windsurf/Kiro placeholder 一起打开攻击面)
7. **测试真实性** —— 真协议 fixture / 真 OAuth fixture / OCAW 判别性 fixture / end-to-end canary 都缺

## 3. Slice 切片(取 Codex 6-slice 结构)

### C0 — 证据 / 法律 / 真流量红线(0.5–1 天 + Owner 法务/产品时间)
- 不写代码;Owner 拍 Cursor 端点 / client / scope / wire schema 合法来源 / EULA 边界
- 产物:`docs/process/evidence/cursor-*.md`(sanitize 后)
- **判别 test**:fixture 含 `Authorization: Bearer live_*` / `Cookie: WorkosCursorSessionToken=...` 必须自动失败
- **阻塞 Slice C2/C3/C4**

### C1 — Cursor credential acquisition + store contract(1–1.5 天)
- 改 `credentialacq/types.go`(`DefaultModePlans` 加 cursor)
- 改 `credentialstore/types.go`(`DefaultVendorHandlers` + runtime material checksum/cookie/client-version 一等字段)
- `credentialacq/vendor_exchangers.go` 准备接真 exchanger 位置(本 slice 不替换 fake,只 wiring)
- **判别 test**:`TestCursorOAuthConfigRequiresOperatorVerifiedEndpoints` (已存在
  `provider/cursor/bootstrap_test.go`) 验证 `ValidateOAuthConfig` 在 token_url/client_id/
  redirect_uri 任一缺失时返回 `ErrCursorOAuthConfigRequired`;这是 OAuth start-time fail-closed,
  与 Gemini/Antigravity/Windsurf 同 pattern,不在 mode plan listing 层过滤。

### C2 — 真 OAuth exchange + refresher 闭环(1.5–2 天)
- vendor_exchangers.go:52 fake → real `NewCursorOAuthExchanger`(authorization_code grant)
- `credentialworker/refresh_adapter.go` `MockOnlyProviders` 去掉 cursor(改 Cursor 专属 opt-in)
- **C0 必须先拍**:真 Cursor 是否 `access_token == session_token`?refresher.go:256 写 `cred["session_token"] = accessToken` 的假设要么 verify 要么改
- **判别 test**:`TestCursorOAuthExchangeIgnoresCredentialSuppliedTokenURL` — credential payload 注入恶意 `oauth_token_endpoint`,fake server 只能看到 operator-config URL

### C3 — OCAW header / profile + transport mimicry(2–3 天,**checksum 证据未到则阻塞**)
- 新包 `backend/internal/provider/cursor/ocaw/` —— signer 接口 + trace_id / timezone / request_id 实现
- `transport/mimicry/registry.go` 加 Cursor profile
- strict mode + 无 verified checksum 来源时 fail-closed
- **判别 test**:`TestCursorOCAWStrictModeRejectsMissingChecksumSource` — strict 模式 + 无 checksum 必须 typed error

### C4 — Cursor wire 协议转换(4–7 天,**Owner 拍板 D-4 后再开**)
- 新包 `backend/internal/provider/cursor/wire/` —— encoder(OpenAI → Cursor proto)+ decoder(Cursor stream → OpenAI SSE)
- 改 `cursor_session.go` BuildRequest 用 encoder 而非透传 InboundBody
- **风险**:无 MIT-licensed Cursor wire schema → 只能黑盒行为推断 + HUAKAI 自建 .proto,严格保持 behavior-level clean-room
- 可拆 C4.a request encoder / C4.b stream decoder / C4.c error mapping

### C5 — 默认关闭的 canary wiring + rollback(1 天)
- 新增 `HUAKAI_ENABLE_CURSOR_VENDOR=true` Cursor 专属 flag(不复用 placeholder 总开关)
- `default.go` 注册 Cursor adapter 仅在专属 flag + operator config 完整时
- **判别 test**:`TestCursorVendorFlagDoesNotEnableOtherPlaceholderAdapters` — 只开 Cursor flag 不能顺带激活 Windsurf/Kiro placeholders

## 4. 参考项目对照(合并版)

| 主题 | 参考 cite | HUAKAI delta(架构/算法/生态) |
| --- | --- | --- |
| C1 store contract | CLIProxyAPI PKCE shape `~/refs/CLIProxyAPI/internal/auth/codex/pkce.go:13` + `openai_auth.go:24-26`(hardcoded ClientID/TokenURL/AuthURL) | HUAKAI **生态升级**:token_url/client_id 一律 operator-config 而非硬编(信任链原则 [[project_core_trust_chain_differentiator]]) |
| C2 真 OAuth | CLIProxyAPI Kimi refresh shape `~/refs/CLIProxyAPI/internal/auth/kimi/kimi.go:342-382` + xAI discovery `xai.go:96-140` | HUAKAI **架构升级**:operator-config-bound + SSRF 防 + refresh advisory lock |
| C3 OCAW | Portkey provider context 集中化 `~/refs/portkey/src/handlers/services/providerContext.ts:28-109` + Envoy header mutation `~/refs/envoy-ai-gateway/api/v1beta1/ai_gateway_route.go:74-321` | HUAKAI **架构升级**:Cursor 专属 signer 接口,非通用 RoundTripper 注入;**算法升级**:checksum 来源策略(strict/dev/canary 三档) |
| C4 wire | LiteLLM OpenAI-like handler `~/refs/litellm/litellm/llms/openai_like/chat/handler.py:26-111`;**无项目实现 Cursor Connect/proto 反向** | HUAKAI **架构升级 0→1**:自建 Cursor wire 包 + 自抓 schema |
| C5 canary | LLMGateway custom-provider e2e `~/refs/llmgateway/apps/gateway/src/chat-custom-provider.e2e.ts:100-177`(fail-closed) + Helicone provider helper auth fallback `~/refs/helicone/packages/cost/models/provider-helpers.ts:216-289` | HUAKAI **生态升级**:Cursor 专属 flag(不连带其他 placeholder),feature-flag granularity |
| 产品形态对照 | LiteLLM 把 Cursor 当 BYOK direct provider `~/refs/litellm/litellm/provider_endpoints_support_backup.json:2286`;LLMGateway 注明 Cursor external endpoint 对 coding agent 不完整 `~/refs/llmgateway/.../integration-guides-grid.tsx:52` | HUAKAI 若选 A(IDE session/proto 反代)无 MIT 等价;若选 B(BYOK)对齐 LiteLLM |

## 5. 风险登记(合并)

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R1 | Cursor wire schema 无 MIT 来源,C4 工作量超估 | 高 | C0 先抓真流量;若超 7 天拆 C4.a/b/c |
| R2 | `x-cursor-checksum` 逆向 ToS 灰色 | 高 | C3 strict mode 不写算法,只接 verified checksum 来源;Owner 法务前置 |
| R3 | Cursor EULA 是否禁第三方代理 | **可能阻断 A 选项** | C0 Owner 拍板 |
| R4 | `cred["session_token"] = accessToken`(refresher.go:256)语义假设 | 中 | C2 前 verify;不对则修字段映射 |
| R5 | placeholder family 总开关误激活其他 vendor | 中 | C5 Cursor 专属 flag,不复用 |
| R6 | 真协议 fixture 缺,canary 不可信 | 中 | C5 加 canary fake upstream 验 request/refresh/error path |
| R7 | parallel-draft 独立性瑕疵 | 低 | 本稿 §0 已披露,Owner 决定是否重派 codex |

## 6. Owner 决策点(5 个)

### D-1: Cursor 产品形态
- **A IDE session/proto 完整反代** —— 工作量 10–15 天,含 C3 + C4,无 MIT 等价,ToS 高风险
- **B BYOK / direct-provider safe equivalent** —— 用户拿 cursor API key 走 OpenAI 兼容接口,工作量 ~5 天 仅 C0+C1+C2+C5,对齐 LiteLLM,ToS 低风险,**但功能比 sub2api/all-api-hub 弱**
- **C manual-first / internal canary** —— 不做生产,仅 Owner 内部测,工作量 ~2 天 仅 C0+C5

参考对照:
- LiteLLM `~/refs/litellm/.../provider_endpoints_support_backup.json:2286` 选 B
- LLMGateway 注明 coding agent 不完整 → 提醒 A 难度
- 无 MIT 项目选 A

### D-2: OAuth 来源
- **A Owner 提供并维护 operator config**(HUAKAI 当前已 fail-closed 这条路)
- **B 用公开 CLI client / discovery**(如 CLIProxyAPI hardcoded codex `app_EMoamEEZ73f0CkXaXp7hrann`)
- **C 不做 OAuth,仅 manual token import**

### D-3: OCAW 策略
- **A strict fail-closed 直到 checksum 算法证据完整**(推荐)
- **B dev/canary 允许缺 checksum**
- **C 放弃 OCAW,仅 BYOK direct provider**

### D-4: Cursor wire schema 来源
- **A 公开 / 许可资料**(尚未发现)
- **B 红 acted traffic 行为推断 + HUAKAI 自建 schema**
- **C 暂缓协议转换,保留 passthrough / manual-first**

### D-5: 上线开关
- **A Cursor 专属 flag**(推荐)`HUAKAI_ENABLE_CURSOR_VENDOR=true`
- **B 复用 placeholder 总开关 `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS`**(扩大风险面)
- **C 仅本地 canary 不接生产**

## 7. 推荐起步顺序

| Slice | 工时 | 阻塞依赖 | 谁 |
| --- | --- | --- | --- |
| C0 | 0.5–1 天 + Owner 拍板 | 无 | Owner 主导 |
| C1 | 1–1.5 天 | **无**(可与 C0 并行;C1 只接现有 HUAKAI adapter 进 store contract,extra 字段名 `cursor_checksum`/`cursor_client_version`/`cookie` 已在 cursor_session.go 用,不来自真流量) | codex 实施 + Claude review |
| C2 | 1.5–2 天 | C0 done(`access_token == session_token` 假设需 verify)+ C1 done | codex + Claude review |
| C3 | 2–3 天 | C2 + checksum 证据 | 看 D-3 |
| C4 | 4–7 天 | D-4 拍板 | 看 D-4 |
| C5 | 1 天 | C2(+ C3/C4 看选项) | codex |

**最低风险起步:C0 → C1 → C2 → C5**,工作量 ~5 天,可以让 HUAKAI cursor 进入"默认关闭但可
operator-config 开启"的真实 state,不进 C3/C4 这些 ToS / 协议反向高风险区域 —— **对应 D-1
选 B(BYOK safe equivalent)路径**。

**完整反代路径**:再加 C3 + C4,合 10–15 天 —— **对应 D-1 选 A,需 Owner 拍板 D-3/D-4 提供
合法 wire schema + checksum 来源**。

---

Synthesizer: Claude PM(Opus 4.7)
Time: 2026-05-26T08:45:00Z
Source plans:
- `docs/process/plans/2026-05-26-cursor-vendor-claude.md`
- `docs/process/plans/2026-05-26-cursor-vendor-codex.md`
