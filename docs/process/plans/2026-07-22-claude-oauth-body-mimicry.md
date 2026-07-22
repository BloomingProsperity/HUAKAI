# Claude OAuth Body 伪装 · 分支说明

| 项 | 值 |
|---|---|
| 分支 | `feat/claude-oauth-body-mimicry` |
| 基底 | `origin/main` @ `52d7f658` |
| 目标 | 第三方客户端打 Anthropic **反转号**时，请求 body 改成接近真实 Claude Code 的 system 形态 |

## 架构（审查后结构）

```
clientgate.DecideWithBody
  真 Claude Code → OfficialDirect
  第三方 + BodyCloak 开 → Allow (ReasonAllowBodyCloak)  // 只读开关，不碰 body
  第三方 + BodyCloak 关 → Reject

OutboundDispatchBody(officialDirect, …)
  OfficialDirect → 任意协议族整段 clone，跳过 rewrite

identityRewrite → outboundbody.BuildPlan + Apply
  SkipAll | SystemCloak(claudecodecloak) | IdentityUserID(mimicryidentity)
```

| 包 | 职责 |
|---|---|
| `outboundbody` | **唯一**出站 body 策略 + 串接 |
| `claudecodecloak` | system 三块纯函数 + `Enabled()` |
| `mimicryidentity` | metadata.user_id |
| `clientgate` | 准入；只调 `ThirdPartyAdmissionAllowed()` |

`gateway.ApplyMimicryPlan` 的 SystemRewrite 仍是 binding 级前缀，**不**承担 OAuth 反转伪装。

## 行为矩阵

| 客户端 | 入站 | 出站 body |
|---|---|---|
| 真 Claude Code | OfficialDirect | **不改**（全协议族） |
| 第三方（cloak 默认开） | Allow | system 三块 + user_id |
| 第三方 + `HUAKAI_CLAUDE_OAUTH_BODY_CLOAK=false` | Reject 403 | — |
| api_key | Allow | 无 system cloak |

## 默认行为说明（Owner 可见）

**默认开启** body 兼容：`HUAKAI_CLAUDE_OAUTH_BODY_CLOAK` 未设或非 false 时，非官方客户端可进 Anthropic 反转号并由出站伪装。关开关恢复历史「仅官方」严格门。

## 测试

```bash
cd backend
go test ./internal/claudecodecloak/ ./internal/outboundbody/ ./internal/gatewayhttp/clientgate/ ./internal/gatewayhttp/chatpipe/ -count=1
go test ./internal/gatewayhttp/ -count=1 -run 'Identity|Official|BodyCloak'
```
