# Claude OAuth Body 伪装 · 分支说明

| 项 | 值 |
|---|---|
| 分支 | `feat/claude-oauth-body-mimicry` |
| 基底 | `origin/main` @ `52d7f658` |
| 目标 | 第三方客户端打 Anthropic **反转号**时，请求 body 改成接近真实 Claude Code 的 system 形态，便于后续提 PR |

## 行为

| 客户端 | 入站 clientgate | 出站 body |
|---|---|---|
| 真 Claude Code（严格形态） | `OfficialDirect` | **不改** system（`OutboundDispatchBody` 对 claude_session 跳过） |
| 第三方 / 半吊子 UA | 默认 **Allow**（兼容伪装） | **system 三块** + 原 system 沉 messages + 既有 user_id 改写 |
| 关兼容：`HUAKAI_CLAUDE_OAUTH_BODY_CLOAK=false` | 第三方 **Reject**（历史严格门） | 无 body 伪装 |

## 开关

- `HUAKAI_CLAUDE_OAUTH_BODY_CLOAK`：默认开；`false` 关 body 伪装 + 恢复第三方 403  
- `HUAKAI_MIMICRY_IDENTITY_REWRITE`：仍控制 metadata.user_id（默认开）  
- TLS 仍走主线 **Rust sidecar only**（本 PR **不碰**）

## 代码

| 路径 | 作用 |
|---|---|
| `internal/claudecodecloak/` | 纯函数：billing + 身份 + 扩充 三块；幂等 |
| `gatewayhttp/clientgate` | 第三方默认 Allow（cloak 开时） |
| `gatewayhttp/chat_completions_stream.go` `identityRewrite` | 出站串 cloak → user_id |

## 未做（可后续 PR）

- 工具名混淆 / tools 尾 cache 断点整串  
- 可运营配置的 system 模板  
- chat/completions 跨协议翻译后的二次伪装专项（当前走同一 identityRewrite 钩子）  

## 测试

```bash
cd backend
go test ./internal/claudecodecloak/ ./internal/gatewayhttp/clientgate/ -count=1
go test ./internal/gatewayhttp/ -count=1 -run 'Identity|BodyCloak|Official'
```
