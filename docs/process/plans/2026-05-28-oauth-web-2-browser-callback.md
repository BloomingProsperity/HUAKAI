# OAUTH-WEB-2 (S1-002a) browser callback 无 Bearer 实施计划

Lane: claude (auth-sensitive 设计, 直接 Write per AGENTS clean-room 规则)
UTC: 2026-05-28
依据: `2026-05-28-dual-mode-oauth-callback-spec.md` §3.2 + §7 (Owner 已批准, D-A=方案1)

## 0. 关键现状结论 (读码确认, 不臆测)

- provider 把浏览器重定向到 redirect_uri = `https://gateway/admin/v1/credentials/oauth-callback?flow_id=X&state=Y&code=Z` (OAUTH-WEB-1 已让 https admin redirect 带 flow_id)
- 该路径正是已挂载的 `GET /admin/v1/credentials/oauth-callback` → `newCredentialAcqOAuthCallbackHelperHandler` (`admin_credential_acquisition_handler.go:92,289`)
- **bug (S1-002a)**: 该 handler `:291` 调 `resolveCredentialAcqAdmin` 强制 admin Bearer; 浏览器导航不带 Bearer → 永远 401, 端点对它本来的用途(收 OAuth 跳转)不可用
- helper 路由组 (`routes.go:370-384`) **无 route-level auth 中间件**; auth 全靠每个 handler 自己调 resolveCredentialAcqAdmin → 去掉这一句即可让浏览器进入, 无需改路由
- **安全闸已存在**: `CompleteOAuthCallback` (`oauth.go:134-178`) 已做全部核心校验:
  - flow_id → session 查 (`store.Get`)
  - code 一次性/重放 (`!ConsumedAt.IsZero() || Status==Finalized → ErrFlowReplay`)
  - session TTL (`now.After(ExpiresAt) → ErrFlowExpired`)
  - state CSRF (`OAuthStateMatches` = sha256 + `subtle.ConstantTimeCompare`, `:207-210`)
  - PKCE verifier decrypt
- session 在 start 时绑定发起 admin: `start.ActorID = fmt.Sprintf("%d", ident.TokenID)`, `ActorRole = ident.Role` (`:351`); `CreateFromStart` 写进 session row
- 程序化 admin 完成已有单独端点 `POST .../{flowID}/callback` (`:81`, 带 Bearer, 不动)
- 既有测试无任何"callback 必须带 Bearer/401"断言 (`:436` 的 401 测的是 START 端点); 成功测试都是 start(带 admin auth)→ GET callback(flow_id+state+code) → 200

## 1. 设计 (最小、复用安全闸、不重造)

把 `GET /oauth-callback` 改成它本该是的**无 Bearer 浏览器落地端点**:

1. `newCredentialAcqOAuthCallbackHelperHandler`: **删 `resolveCredentialAcqAdmin` 调用**。保留 deps-nil 守卫(d.Auth/Sessions/Credentials/AuditStore 任一 nil → 503)。读 flow_id/state/code query; flow_id 空 → 400 invalid_request。
2. 审计归属来自 session 而非 Bearer: 先 `d.Sessions.Get(ctx, flowID)` 拿 session(确认存在 + 取 ActorID); 查不到 → 404/400 清晰错误。然后用 `actorID = session.ActorID`(发起 admin)走完成。
3. 完成逻辑**复用** `CompleteOAuthCallbackWithRegistry`(已含 state/code/TTL/replay/PKCE 全部闸)→ finalizer.Finalize(actorID=session.ActorID, requestID)。
4. 审计事件(EventCompleted / EventFailed)actor 写 session.ActorID, role 写 session.ActorRole。
5. 程序化 admin POST `.../callback` 不动(继续 Bearer)。

冻结包合规: 仅**改既有文件** `gatewayhttp/admin_credential_acquisition_handler.go`(CLAUDE.md #13 允许既改); 不新增 gatewayhttp 文件; 安全逻辑全在非冻结 credentialacq 复用; 无需新 helper。

## 2. 为什么无 Bearer 安全 (spec §3.2 安全模型, Owner 已批)

- start 必须 admin Bearer(`POST /oauth-init` / `POST /credential-acquisitions` 仍 resolveCredentialAcqAdmin), session 绑定发起 admin + 目标 provider_account_id
- 完成需要 unguessable flow_id(server uuid) + 匹配 state(server 随机, ConstantTimeCompare) + provider 发的合法 code; 三者缺一不可
- code 一次性 + 短 TTL 限制泄露窗口(spec §4)
- 无提权: 完成只能把凭据绑到 start 时 admin 指定的账号槽; 审计记发起 admin identity

## 3. 测试矩阵 (discriminating, spec §7; 每条 mutation 必变红)

改既有 `admin_credential_acquisition_handler_test.go`:

1. `TestOAuthBrowserCallbackCompletesWithoutBearer`: start(admin)→ GET callback **不带任何 admin 上下文**(auth stub 设为 unauthorized/缺省)+ 合法 flow_id+state+code → 200 + 凭据创建。Mutation: 恢复 resolveCredentialAcqAdmin Bearer 闸 → 无 Bearer 请求 401 → red。
   - 注意: 现有 fixture 的 auth stub 若默认放行, 本测试要显式用 `adminPoolAuthStub{err: admin.ErrAdminUnauthorized}` 证明"即使 admin 认证失败, 浏览器路径仍靠 flow_id+state+code 完成"。
2. `TestOAuthBrowserCallbackRejectsStateMismatch`: 合法 flow_id+code 但 state 错 → 非 200(state_mismatch)。Mutation: 若 handler 绕过 CompleteOAuthCallback 的 state 校验 → 错 state 通过 → red。
3. `TestOAuthBrowserCallbackRejectsReplay`: 同一 session 完成后再 GET 一次 → 非 200(flow_replay)。Mutation: 不复用 CompleteOAuthCallback 的 ConsumedAt/Finalized 闸 → 重放成功 → red。
4. `TestOAuthBrowserCallbackAuditsStartingAdmin`: 完成后审计 actor = start 时的 admin ActorID(非空, 非 Bearer 派生)。Mutation: 把 actor 源从 session.ActorID 改成空/Bearer → 审计 actor 错 → red。
5. loopback 不回归: 现有 loopback 相关测试不受影响(loopback 不走该远程端点); 跑全包确认。

## 4. 必跑验证

```bash
cd /home/codex/HUAKAI/backend
GOCACHE=/tmp/go-build go test ./internal/gatewayhttp/ ./internal/credentialacq/ -count=1 -timeout 120s
GOCACHE=/tmp/go-build go build ./...
```

## 5. 不做 / 边界

- 不碰 wiring / operator config(OAUTH-WEB-3)
- 不改 OAUTH-WEB-1 的 flow_id/allowlist 逻辑
- 不动程序化 POST `.../callback`(Bearer 保留)
- 不新增 OAuth provider
- 不读外部 ref source(照 HUAKAI 内部模式)

## 6. 风险

- blast radius: 仅 `GET /admin/v1/credentials/oauth-callback` 认证方式 + 审计 actor 源
- 最坏情况: 去 Bearer 后端点公开, 但无配置 allowlist 时 https web 模式根本起不了 flow(start 拒 https redirect), 故无可完成的会话; 完成仍需 flow_id+state+code
- review: per-commit codex R1/R2 (≤2 轮), auth-core 严格归类 S0/S1
