# Antigravity 内置 OAuth client_id/secret 改 env 提供(消除公开仓 secret)

日期:2026-07-12  作者:BloomingProsperity

## 背景 / 触发

push 到公开仓 `BloomingProsperity/HUAKAI` 被 GitHub secret scanning 拦截:commit 4760ab65
的 `internal/provider/antigravity/bootstrap.go:19-20` 硬编码了 Antigravity CLI 的公开
native-app OAuth `client_id` / `client_secret`(`GOCSPX-` 前缀触发 Google OAuth Client
Secret 检测)。

事实核实:
- 这套值是 Antigravity CLI 的公开 native-app wire 值(RFC 8252 / Google installed-app
  惯例下 "secret" 本不保密),完全相同的值已明文公开在 CLIProxyAPI(`antigravity_executor.go`、
  `auth/antigravity/constants.go`、`api_tools.go`)与 sub2api(`pkg/antigravity/oauth.go`)
  两个公开仓——放进 HUAKAI 不泄露任何尚未公开的东西。
- 完整 secret 全仓仅 bootstrap.go:20 一处(两处 docs 是截断占位 `GOCSPX-K58FWR486...`,不触发)。

## Owner 决策(2 步)

1. 先选 Path B(改 env 提供),随后追加「两个都保留吧」→ 最终定为:**内置默认 + env override 两套都留**。
   内置默认保证开箱即用(turnkey);env override 让运营者可换自己的 OAuth app(避开所有 HUAKAI
   部署共用一个 client_id 的单点)。env 设了覆盖,未设回落内置默认。
2. **后果**:保留内置默认 = 内置 secret 仍在源码 → GitHub secret scanning 仍拦 → push 仍需 Owner
   点两个 unblock URL 放行(与 CLIProxyAPI/sub2api 公开仓同样带这套公开 wire 值,合理)。

## 改动范围

固定公开 profile(token 端点/scopes/redirect_uri)仍是常量。client_id/secret 由**访问函数**解析:
env 设了用 env,否则回落内置默认常量(内置值保留)。

1. **bootstrap.go**:内置 client_id/secret 常量**保留**(改为非导出 `antigravityBuiltinClientID/Secret`);
   新增 env 名常量 + 两个导出访问函数 `AntigravityOAuthClientID()/Secret()`:env 非空则用 env,否则
   回落内置。`DefaultOAuthConfig`/`RefreshAdapterFromOAuthConfig`/`validateRefreshOAuthConfig` 改用访问函数。
2. **bootstrap_test.go**:常量引用改函数调用;**新增判别测试** env override 生效(设 env → 解析出 env 值)。
3. **mode_refresh_test.go**:两处常量引用改函数调用(无需 setenv,内置默认在)。
4. **.env.prod.example**:文档化两个**可选** env override(占位值,不写真 secret;说明默认用内置)。

## 成功判据

- `go build ./...` + `go vet ./...` + `go test ./internal/provider/antigravity/ ./internal/credentialworker/` 全绿。
- 变异证:①访问函数删掉"env 非空则用 env"分支 → 新 override 测试红;②内置默认改空 → Pins 测试红。
- 门禁后重跑干净基线无残留。**push 仍需 Owner 点 unblock URL**(内置 secret 在源码是有意保留)。

## 爆炸半径

小。仅 antigravity 反转车道(默认 env-gate 关);不触 money/quota/schema/auth-core。lane 行为:
env 未设等价原内置(完全不变),env 设了用运营者自己的 client。
