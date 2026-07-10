# Antigravity 多厂商中转 spec(经 Owner Google AI Pro 号亲登实证 2026-07-10)

> 一个 Google AI Pro($19.99/月)经 Antigravity 可调 Gemini+Claude+GPT。以下 wire id/端点/body 均实测 200 验证。

## OAuth
- client_id `1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com` (机密客户端,secret `GOCSPX-K58FWR486...`)
- **redirect `https://antigravity.google/oauth-callback`(托管页显示 code,非 localhost)+ PKCE S256**
- scope: cloud-platform userinfo.email userinfo.profile **cclog experimentsandconfigs** openid;auth_method=`consumer`
- token 端点 oauth2.googleapis.com/token

## 服务端点(**daily**,非 prod)
- `https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent`(+ streamGenerateContent / loadCodeAssist / fetchAvailableModels)
- Body: `{model, project, request:{contents:[...]}, enabledCreditTypes:["GOOGLE_ONE_AI"]}`
- Header: Authorization Bearer / Content-Type json / Accept */* / User-Agent `antigravity/hub/<ver> darwin/arm64`
- fetchAvailableModels body = `{}`;每模型含 quotaInfo(remainingFraction+resetTime)

## 实测可用模型(fetchAvailableModels 真实 wire id)
| wire id | 显示名 | apiProvider |
|---|---|---|
| `gemini-2.5-flash` | Gemini 3.1 Flash Lite | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3.1-pro-low` | Gemini 3.1 Pro (Low) | API_PROVIDER_GOOGLE_GEMINI |
| `claude-sonnet-4-6` | Claude Sonnet 4.6 (Thinking) | API_PROVIDER_ANTHROPIC_VERTEX |
| `claude-opus-4-6-thinking` | Claude Opus 4.6 (Thinking) | API_PROVIDER_ANTHROPIC_VERTEX |
| `tab_flash_lite_preview` |  | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3-flash-agent` | Gemini 3.5 Flash (High) | API_PROVIDER_GOOGLE_GEMINI |
| `chat_20706` |  | API_PROVIDER_INTERNAL |
| `gemini-3.1-flash-lite` | Gemini 3.1 Flash Lite | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3.5-flash-low` | Gemini 3.5 Flash (Medium) | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3.5-flash-extra-low` | Gemini 3.5 Flash (Low) | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3.1-flash-image` | Gemini 3.1 Flash Image | API_PROVIDER_GOOGLE_GEMINI |
| `gpt-oss-120b-medium` | GPT-OSS 120B (Medium) | API_PROVIDER_OPENAI_VERTEX |
| `chat_23310` |  | API_PROVIDER_INTERNAL |
| `tab_jump_flash_lite_preview` |  | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-2.5-pro` | Gemini 2.5 Pro | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-pro-agent` | Gemini 3.1 Pro (High) | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3.1-pro-high` | Gemini 3.1 Pro (High) | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-3-flash` | Gemini 3 Flash | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-2.5-flash-lite` | Gemini 3.1 Flash Lite | API_PROVIDER_GOOGLE_GEMINI |
| `gemini-2.5-flash-thinking` | Gemini 3.1 Flash Lite | API_PROVIDER_GOOGLE_GEMINI |

## 已 200 验证
- `claude-sonnet-4-6` → 200 出内容
- `gemini-3-flash` → 200 出内容
- `gemini-2.5-flash` → 200(prod 端点也可)

## HUAKAI 现状差距
- `internal/provider/antigravity/antigravity_session.go` 端点写死 `api.antigravity.ai`(**错**),UA/头全占位 TODO。
- 按本 spec clean-room 重写:daily-cloudcode-pa 端点 + 上述 OAuth + body + 动态模型清单;一个账号 fan-out 三厂商模型。