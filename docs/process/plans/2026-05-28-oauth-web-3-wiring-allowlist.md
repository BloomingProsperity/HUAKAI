# OAUTH-WEB-3 (S1-002b) production wiring 接 allowlist 构造函数 + operator config

Lane: claude (auth-adjacent wiring/config 设计)
UTC: 2026-05-28
依据: `2026-05-28-dual-mode-oauth-callback-spec.md` §5

## 0. 现状 (读码确认)

- `cmd/gateway/wiring.go:288` 用 `installGeminiPublicCLIOAuthExchangers(registry, client, secret)` → 内部 `NewGeminiPublicCLIOAuthExchangerWithClientAndSecret`(**无 allowlist**)
- `:291` 用 `installChatGPTOAuthExchanger(registry, client)` → 内部 `NewChatGPTOAuthExchangerWithClient`(**无 allowlist**)
- 无 allowlist → `validateXxxRedirectURIWithHTTPSAdminAllowlist` 对任何 https admin redirect 返 `ErrFeatureDisabled`(拒)→ 生产里远程 web 模式根本起不了 flow(S1-002b)
- allowlist 构造函数已存在: `NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist(mode, client, secret, allowlist)`(gemini_oauth.go:74)、`NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist(client, allowlist)`(chatgpt_oauth.go:66)
- 现有 env 读取风格: `strings.TrimSpace(os.Getenv(const))`(wiring.go:493/510/555);env const 风格 `geminiPublicCLIOAuthClientSecretEnv = "HUAKAI_..."`(:161)
- 现有 wiring 测试: `cmd/gateway/wiring_test.go` 有 `TestWiring_InstallGeminiPublicCLIOAuthExchangersReplacesDefault` / `...ChatGPT...` / `...SecretEnvFailFast`,模式是 install 后从 registry Lookup 校验

## 1. 设计 (只改 cmd/gateway/wiring.go + wiring_test.go;cmd/gateway 非冻结)

1. 新增 env const `adminOAuthCallbackAllowlistEnv = "HUAKAI_ADMIN_OAUTH_CALLBACK_ALLOWLIST"`
2. 新增 loader `loadAdminOAuthCallbackAllowlistFromEnv() []string`:读 env,逗号分割,逐项 TrimSpace,丢弃空项;**未设/全空 → 返回 nil/空切片**(退化为 loopback-only,不破现状)
3. `installGeminiPublicCLIOAuthExchangers` 增 `allowlist []string` 参数 → 改用 `NewGeminiPublicCLIOAuthExchangerWithClientSecretAndAdminCallbackAllowlist(mode, client, clientSecret, allowlist)`
4. `installChatGPTOAuthExchanger` 增 `allowlist []string` 参数 → 改用 `NewChatGPTOAuthExchangerWithClientAndAdminCallbackAllowlist(client, allowlist)`
5. wiring 调用点(:288/:291)先 `allowlist := loadAdminOAuthCallbackAllowlistFromEnv()`,传给两个 install
6. 现有 assert 函数(`assertGeminiPublicCLIOAuthExchangersHaveHTTPClient` / `assertChatGPTOAuthExchangerHasHTTPClient`)必须仍通过(allowlist 构造函数同样注入 httpClient — 确认 `IsGeminiPublicCLIOAuthExchangerWithExplicitClient` 等判定不破)

## 2. 默认安全 (空 allowlist = 现状)

未配 `HUAKAI_ADMIN_OAUTH_CALLBACK_ALLOWLIST` → allowlist 空 → https admin redirect 仍被拒(ErrFeatureDisabled)→ 仅 loopback 模式,完全等价当前生产行为,不破现状。配了才启用远程 web。

## 3. 测试 (改 wiring_test.go,discriminating)

1. `TestLoadAdminOAuthCallbackAllowlistFromEnv`:env=`" https://a/ , https://b/ ,, https://c/ "` → `["https://a/","https://b/","https://c/"]`;env 未设 → 空。Mutation:不 trim/不滤空 → 元素带空格或含空串 → red。(用 t.Setenv)
2. `TestWiring_InstallGeminiThreadsAdminCallbackAllowlist`:install 时传一个含某 https admin callback 的 allowlist;从 registry Lookup 到 Gemini exchanger,调 `exc.StartOAuthFlow(ctx, nil-store, StartInput{RedirectURI: 该 https admin callback, vendor=gemini, mode=code_assist, secret 注入})`;断言返回的 error **不是** `credentialacq.ErrFeatureDisabled`(即 allowlist 放行了该 redirect,卡在 nil store 而非被拒)。Mutation:install 退回非 allowlist 构造函数 → 该 https redirect 被拒 ErrFeatureDisabled → red。
   - 照 wiring_test.go:76 `StartOAuthFlow(ctx, nil, ...)` 的 nil-store 用法;注意 Gemini secret 注入(env-only),测试要满足 validateBuiltinProfile 的 secret 要求,否则会先因 secret 报错而非 allowlist
   - 若 nil store 在 allowlist 通过后报的不是 ErrFeatureDisabled 的某稳定 error,断言 `errors.Is(err, ErrFeatureDisabled) == false` 即可判别
3. `TestWiring_InstallChatGPTThreadsAdminCallbackAllowlist`:同理对 ChatGPT(PKCE-only 无 secret),allowlist 含 https admin callback → StartOAuthFlow 该 redirect 不被 ErrFeatureDisabled 拒。Mutation:非 allowlist 构造 → red。
4. 现有 3 个 install/env 测试仍绿(可能需按新签名补 allowlist 参数,传 nil 保持原断言)。

## 4. 必跑验证
```bash
cd /home/codex/HUAKAI/backend
GOCACHE=/tmp/go-build go test ./cmd/gateway/ ./internal/credentialacq/ -count=1 -timeout 180s
GOCACHE=/tmp/go-build go build ./...
```

## 5. 不做 / 边界
- 不改 OAUTH-WEB-1/2 已提交逻辑
- 不动 loopback 现状(空 allowlist 退化)
- 不引新依赖
- 不读外部 ref source

## 6. 风险
- blast radius: 仅 Gemini/ChatGPT OAuth exchanger 构造 + 新 env 读取
- 最坏: allowlist 没传进去 → 远程 web 仍不可用(回到 S1-002b),由测试 2/3 防;或传错导致 loopback 回归 → 由空 allowlist 默认 + 现有 install 测试防
- review: per-commit codex R1/R2,auth-adjacent 严格归类
