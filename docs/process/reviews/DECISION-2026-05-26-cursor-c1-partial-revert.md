# Decision: Cursor C1 Partial Revert (2026-05-26)

| Field | Value |
| --- | --- |
| Decision date | 2026-05-26 |
| Trigger | Owner Docker/Linux fresh checkout `63c7708` 跑 fresh PG migrations + full backend test, 抓 2 个真问题 |
| Status | Approved by Owner |
| Scope | `claude/hermes-phase-1` 分支, 本 session 内执行 |

## 1. Owner 发现

### 1.1 mode_refresh_test 红
`backend/internal/credentialworker/mode_refresh_test.go:28` 的 `t.Fatalf` 报 `mode adapter count=19 want 20` (test function 声明在 line 24)。

根因:
- C1 (commit b2ff6ac) 给 [credentialstore/types.go:268](../../../backend/internal/credentialstore/types.go) 加 cursor handlerSpec → store registry 19→20
- C1 给 [credentialacq/types.go:172](../../../backend/internal/credentialacq/types.go) 加 cursor ModePlan → acq plans 19→20
- **没给 [credentialworker/mode_refresh.go DefaultModeAdapterRegistry](../../../backend/internal/credentialworker/mode_refresh.go) 加 cursor** → worker registry 仍 19
- `mode_refresh_test.go:26` `wantCount := len(credentialstore.DefaultHandlerRegistry().Names())` → want 自动 = store 20, got = 19 → 红

更深层根因: Claude PM commit C1 时 **漏跑 credentialworker test**, 仅跑 credentialstore + credentialacq + provider/cursor + cmd/gateway。违反 [[feedback_no_fake_pass]] (PASS 必须真跑过) + [[feedback_full_suite_verification]] (收尾必跑 ./... 全量)。

### 1.2 fail-closed 测试是 helper 层, 真实入口不 fail-closed
C1 加的 `TestCursorOAuthConfigRejectsMissingEachOperatorField` 测的是 [cursor/bootstrap.go ValidateOAuthConfig](../../../backend/internal/provider/cursor/bootstrap.go#L58) 被直接调用时 fail-closed。

但真实 admin OAuth start 走的是通用 [credentialacq/oauth.go](../../../backend/internal/credentialacq/oauth.go), 不调 cursor 专属 Validate。`cursor/oauth` 在 [vendor_exchangers.go:52](../../../backend/internal/credentialacq/vendor_exchangers.go#L52) 仍是 `NewPKCEFakeExchanger(TokenShapeSession)`, 它不知道 cursor 专属 ValidateOAuthConfig 的存在。

结果:测试证明 helper fail-closed, **没证明真实 admin 入口 fail-closed**。属于 [[feedback_test_quality_discipline]] 判别性假阳性 — 测试通过 ≠ 真实入口被保护。

## 2. Owner 决策

**B:partial revert** — 撤 cursor ModePlan + handlerSpec + 相关 tests, 等 C2 + C5 一起重开。

不走 A "补 worker adapter":
- A 只解 test count, 不解 fail-closed 入口分歧
- Owner 战略 cursor 暂搁 ("我们目前制作 claude/gemini/codex 三个"), 补 worker adapter 等于给暂搁 vendor 加 plumbing, 不是退场

## 3. 撤回范围

### 撤
- [credentialstore/types.go:268](../../../backend/internal/credentialstore/types.go) cursor handlerSpec
- [credentialstore/types_test.go](../../../backend/internal/credentialstore/types_test.go) 中:
  - expected list line 30 `"cursor/oauth"`
  - `TestDefaultVendorHandlersIncludesCursorOAuth`
  - `TestCursorOAuthHandlerRuntimeMaterialAcceptsSessionTokenFirst`
  - `TestCursorRuntimeMaterialSurfacesCursorExtras`
  - `containsString` helper (仅前述 3 test 用)
- [credentialacq/types.go:172](../../../backend/internal/credentialacq/types.go) cursor ModePlan
- [credentialacq/types_test.go](../../../backend/internal/credentialacq/types_test.go) 中:
  - line 94 expected phaseAModePlans cursor 行
  - `TestDefaultModePlansIncludesCursorOAuth`
  - `containsFlowKind` helper (仅前述 test 用)

### 保留
- [credentialstore/types.go:20](../../../backend/internal/credentialstore/types.go) `VendorCursor = "cursor"` const — 无副作用, C5 重开时复用
- [credentialstore/types.go:203-204](../../../backend/internal/credentialstore/types.go) whitelist 4 keys (`user_agent` / `cursor_checksum` / `cursor_client_version` / `cookie`) — 无副作用, C5 重开复用
- [provider/cursor/bootstrap_test.go TestCursorOAuthConfigRejectsMissingEachOperatorField](../../../backend/internal/provider/cursor/bootstrap_test.go) — cursor 自己包 fail-closed 强化, 与 admin 入口暴露无关
- `docs/process/plans/2026-05-26-cursor-vendor-{claude,codex,synthesis}.md` — 3 份决策记录档 (2 份 lane plan + 1 份 synthesis)

## 4. 重开条件 (C5)

Slice C5 (默认关闭的 canary wiring + rollback) 起切片时, 需同时落:
1. cursor/oauth 真 OAuth exchanger (替换 fake) — 与 C2 合并或紧接
2. cursor handlerSpec + ModePlan 回加 (revert this revert)
3. credentialworker/mode_refresh.go DefaultModeAdapterRegistry 加 cursor/oauth (operatorOAuthModeAdapter 模式)
4. `HUAKAI_ENABLE_CURSOR_VENDOR=true` 专属 flag 控制 cursor adapter 注册
5. **测试加强**: 不仅测 helper, 也测**通过通用 oauth.go 路径**的 fail-closed — `TestCursorOAuthAdminFlowFailsClosedWithoutOperatorConfig` 通过模拟 admin StartOAuthFlow 验证真实入口不让 fake exchanger 出 token

## 5. PM 自检 — 本次为什么会漏

漏跑 credentialworker test 的根因:
- C1 commit 时手工跑 test 范围 = "C1 改的包 + 相邻 cmd/gateway", **未** 跑 ./... 全量
- 误以为 credentialworker 不在 C1 改动路径 ⇒ 不会受影响
- 但 credentialworker mode_refresh_test 反向引用 credentialstore.DefaultHandlerRegistry().Names() → C1 改了 store, worker test 自动撞

教训 (永久记忆):
- 即使 C1 没改 credentialworker 的代码, store 改了 ⇒ 所有引用 store registry 的下游 test 都必须再跑
- 收尾必跑 ./... 全量 (per [[feedback_full_suite_verification]] 2026-05-20 Owner directive)
- 任何"我只改了 X 包"的判断都不能 override 全套测试纪律

判别性测试假阳性的根因:
- 我只测了 cursor 包内 ValidateOAuthConfig, 没设计 "通过通用 oauth.go 入口" 的端到端 fail-closed test
- 类似问题可能存在于其他 vendor: 任何 provider 专属 Validate 都需要"经由通用 admin 入口验证 fail-closed"的 test
- 教训: spec 写 fail-closed 时, **必明确写"通过哪个入口"**, 不能模糊只写"调用 ValidateOAuthConfig"

## 6. follow-up

- C5 重开 cursor 时回滚本 partial revert (改 4 文件)
- 5 个 fake exchanger 修真 (anthropic/claude_ai_oauth, gemini/code_assist, gemini/google_one, gemini/antigravity, openai/chatgpt_oauth) 类似设计陷阱要避免 — 每个 fake 转真时需测真实 admin 入口 fail-closed

---

**报告者**: Claude PM (Opus 4.7) 自我 audit + Owner 2026-05-26 Docker fresh test 反馈
**记录时间**: 2026-05-26
