# 2026-05-15 F-CRED-001 自动凭证获取流程 (Claude 独立 plan)

| Lane | SPECIFIER (Claude); 平行 codex (bj2ya2yww 在跑) — CLAUDE.md #10 |
| Source | Owner 2026-05-15 "对了，怎么获取这个功能你们也要做！看看sub2api" + "你要比他做的更简洁，更方便" |
| Principle | memory feedback_huakai_better_than_sub2api — HUAKAI 必须 strict better, 非 parity |
| Agent | Claude Opus 4.7 (1M context) |
| UTC | 2026-05-15T16:50:00Z |

## scope

F-AUTH-005 管理已有凭证 (encrypted, refresh, audit). F-CRED-001 是新 feature: **自动获取凭证流程** — 用户从零到有可用凭证。

HUAKAI 不只 parity sub2api 而是 strict better 在 2 维度:
1. **简洁**: 完成 acquisition 步数 ≤ sub2api 一半
2. **方便**: 失败率降 50%+ 或操作员所需知识降 1 级

## 15 mode acquisition flow + HUAKAI 简洁升级 (核心)

| Mode | sub2api 流程 (推测) | HUAKAI 简洁升级 |
|---|---|---|
| **anthropic.api_key** | 手动 paste sk-ant-... | **同**: paste box,但自动 detect prefix → 推断 vendor + mode,不要 dropdown |
| **anthropic.claude_ai_oauth** | OAuth callback 3-5 步 | **1-click "Login with Claude"** — 后端代发 OAuth state + popup callback → done. 失败自动 fallback "paste session cookie" |
| **anthropic.claude_code** | 手动 paste CLI session token | **auto-detect** `~/.config/claude-code/auth.json` (本机已有) — 一按 import |
| **anthropic.bedrock** | 手动配 AWS access_key + secret + region | **AWS CLI auto-detect** — 若 `aws configure` 已设, HUAKAI 一按 import (使用 `aws sts get-session-token` 拿临时凭证) |
| **anthropic.vertex_anthropic** | 手动 paste GCP SA JSON | **gcloud auto-detect** — 若 `gcloud auth application-default login` 已设, HUAKAI 一按 import |
| **openai.api_key** | paste sk-... | 自动 prefix detect (`sk-` → openai), batch paste 多 key 一次创建多账号 |
| **openai.chatgpt_oauth** | OAuth callback 多步 | **1-click "Login with ChatGPT"** popup |
| **openai.codex_cli_oauth** | 手动 paste | **auto-detect** `~/.codex/auth.json` (本机已有) — 一按 import |
| **openai.azure** | 手动配 Azure key + endpoint + deployment | **Azure CLI auto-detect** (`az ad signed-in-user show`) |
| **openai.refresh_token** | paste refresh token + endpoint | endpoint 默认 OpenAI 公开 endpoint, 只 paste refresh token |
| **gemini.aistudio_api_key** | paste AIza... | 自动 prefix detect |
| **gemini.vertex_sa** | paste SA JSON | gcloud auto-detect (同 vertex_anthropic) |
| **gemini.code_assist** | OAuth callback | **1-click "Login with Google for Code Assist"** popup |
| **gemini.google_one** | OAuth (复杂 — Google One 个人订阅) | 1-click "Login with Google One" popup + 自动判断 subscription state |
| **gemini.antigravity** | 未知 (Owner 提供) | 等 Owner 凭证格式描述 |
| **JSON 导入** (跨 vendor 通用,Owner 2026-05-15 加) | sub2api 无直接 JSON 通道 | **POST /admin/v1/credentials/json-import** — 一次 JSON 调用 import 任意 vendor + mode + payload, 支持单条和批量 array; HUAKAI strict better — 自动化集成场景 (其他系统通过 API 写凭证) |

## 必须 preserve 的 sub2api acquisition feature (Feature Preservation Rule, 2026-05-15 review 后补)

sub2api 已有的 acquisition 增强功能, HUAKAI **必须 preserve + 更简洁**:

| sub2api 行为参考点 (paraphrased, cite only) | HUAKAI 升级 |
|---|---|
| Antigravity OAuth 服务里负责 project ID 填充 + retry 的方法 (cite `backend/internal/service/antigravity_oauth_service.go`) — OAuth 后自动 retry 查 antigravity project ID | **HUAKAI 升级**: project ID 自动 fill 是 acquisition flow 内置, 失败 retry 3 次后 fallback 提示用户手动 paste, **不要求用户预先知道 project ID** |
| Gemini OAuth 服务里 Google One tier detect + refresh 的方法 (cite `backend/internal/service/gemini_oauth_service.go`) — Google One 订阅级别自动 detect | **HUAKAI 升级**: tier auto-detect + 在 admin UI 显示订阅状态 (Basic / Premium / AI Premium) + 自动按订阅 tier 配置 rate limit; sub2api 仅 detect tier 名, HUAKAI 用 tier 信息驱动业务逻辑 |
| per-vendor 后台 token refresh worker (cite `backend/internal/service/token_refresher.go` + `gemini_token_refresher.go`, OpenAI/Gemini/Claude 各一类) | **HUAKAI 升级**: F-AUTH-005 已有统一 credentialworker scheduler (commit 6262551), 但 acquisition stage 触发**首次 refresh in OAuth callback 内** (同步 wait token 真有效再返回 200, 防 user 跳转后凭证 not-yet-active); sub2api 异步 worker schedule 后才 refresh |
| OAuth 出站 HTTP client 工厂 (走 privacy proxy 防上游看 HUAKAI server IP, sub2api 各 OAuth service 用) | **HUAKAI 升级**: 与 R-3 transport mimicry 集成 (Rust 数据面 mimicry profile), acquisition OAuth 出站也走 fingerprint profile, sub2api 用 simple HTTP proxy |
| per-service 停服 lifecycle hook | **HUAKAI 升级**: 统一 lifecycle manager + graceful shutdown 时 drain in-flight OAuth callback (避免半完成凭证); sub2api 各 service 独立 Stop |
| 支持多 OAuth clientID 的 refresh 方法 (OpenAI 服务) | **HUAKAI 升级**: 多 OAuth app clientID 支持 (例 ChatGPT public + codex CLI 不同 client), acquisition flow 自动按 mode 选 clientID |
| Antigravity OAuth refresh 非 retryable 错误识别 + Gemini 同类 | **HUAKAI 升级**: 错误识别 + 自动建议下一动作 (non-retryable → 提示用户重新 OAuth; retryable → 后台 retry) |
| account credentials 构造 helper (各 OAuth service 各一) | **HUAKAI 升级**: 复用 F-AUTH-005 account_credentials encrypted blob schema, 不重复 |
| sub2api migration 122 半完成 OAuth state 清理 | **HUAKAI 升级**: credential_acquisition_flow_sessions 表本身带 `expires_at`, scheduled cleanup worker; sub2api 用单独 migration cleanup task |
| sub2api migration 135 多 OAuth provider type 容许 | **HUAKAI 升级**: 15 mode 显式 enum, unknown provider 拒绝 (sub2api 灵活但易混) |

## file-by-file impact

- `backend/internal/credentialacquisition/` (新): per-mode acquisition flow handlers
  - `cli_detector.go`: 扫本机 `~/.codex/auth.json` / `~/.config/claude-code/` / `~/.aws/credentials` / `~/.config/gcloud/` / `~/.azure/` 自动 detect
  - `oauth_flow.go`: 通用 OAuth callback 框架 (state + code exchange + token persist)
  - `paste_detector.go`: 自动 vendor + mode prefix detection (sk-..., sk-ant-..., AIza...)
  - `cli_import.go`: 一按 import 从本机 CLI auth file
- `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go` (新):
  - POST /admin/v1/credentials/oauth-init (start OAuth flow → 返回 redirect URL)
  - GET /admin/v1/credentials/oauth-callback (callback handler → exchange code → persist credential)
  - POST /admin/v1/credentials/cli-import (一按从本机 CLI auth file import)
  - POST /admin/v1/credentials/paste (paste credential → 自动 detect vendor/mode)
  - POST /admin/v1/credentials/csv-import (批量 CSV upload)
  - POST /admin/v1/credentials/json-import (JSON 调用导入 — Owner 2026-05-15 加)
    - Body schema: `{"credentials": [{"vendor", "auth_mode", "credential": "<payload, str|object>", "tenant_id", "alias", "metadata": {}}, ...]}`
    - 单条 OR 批量 array; 自动 detect 已存在 (vendor, mode, alias) 三元组 → upsert or reject (Owner OCAW)
    - Idempotency-Key header 防重复
    - Response: per-row success/fail + 落库后 account_credentials.id
- `frontend/`: 1-click button 集 + paste detect UI + CSV upload form
- `backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql + down.sql`: credential_acquisition_flow_sessions table (OAuth state + nonce + PKCE verifier encrypted + redirect URL + ttl) — actual table landed in commit afc93fb
- `docs/openapi/openapi.yaml`: 5 canonical lifecycle admin endpoints plus input helper triggers

## fusion-upgrade taxonomy (per CLAUDE.md #12)

| 维度 | sub2api 当前 | HUAKAI delta |
|---|---|---|
| **架构** (architecture) | 单点 OAuth handler per vendor | 通用 `credential_acquisition_flow_sessions` table + per-mode strategy registry; 一处加新 vendor 一处实现 |
| **算法** (algorithm) | 用户手选 vendor + mode | paste detector 自动从 prefix (sk-/sk-ant-/AIza/etc) 推断 vendor; CLI detector 自动扫本机 auth file |
| **生态** (ecosystem) | OAuth setup 文档 + 操作员手工 | 1-click button + auto-detect + batch CSV import + failure fallback chain (OAuth fail → paste session → paste manual mode) + F-TRUST acquisition_event audit log |

## data model

**Note (post-implementation sync)**: 实际 schema 已落在 commit afc93fb 的 `backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql`, 表名为 `credential_acquisition_flow_sessions` (plural per Owner naming convention). PKCE verifier 字段 encrypted at rest (AES-GCM via KeyProvider). 详见已合入的 migration 文件而非本 plan 草稿 schema.

## test plan

- unit: paste detector 各 vendor prefix 识别 (sk-/sk-ant-/AIza/etc 各 1 test)
- unit: CLI detector 模拟 ~/.codex/auth.json 文件 → 自动 import 成功
- integration: OAuth init → mock provider callback → exchange → credential 落库
- integration: failure fallback chain (mock OAuth 502 → 自动建议 manual paste mode)
- integration: CSV batch import 10 行 multi-vendor → 10 credentials 落库
- E2E: 操作员真实 1-click 各 mode (Owner 验证)

## time estimate

8-12 天 codex 实施 + 3 天 Claude review + 2 天 frontend Gemini wave 集成 = 13-17 天

实际比 sub2api 实现 (推测 20+ 天 OAuth + per-vendor + admin UI) **节省 1/3 时间** — 因为通用 `credential_acquisition_flow_sessions` table + strategy registry 一处加新 vendor 一处实现.

## blast radius

中. 新 admin endpoints + 新 schema. 但 acquisition 失败不影响现有 F-AUTH-005 凭证 (隔离).

## decision points (5 Owner OCAW)

(D1) frontend 谁做 — Gemini frontend wave 后 follow-up, 还是与 backend lane 同一 Codex impl 一起?  
(D2) OAuth callback domain — HUAKAI 应该用什么 domain 作 OAuth callback (Owner 服务器主域名? 或本地 localhost?)  
(D3) batch CSV upload schema 是否标准化 (vendor,mode,credential)? 还是支持 vendor-specific extra fields?  
(D4) auto-detect CLI auth file 是否需要 Owner approval per device (即操作员可以扫服务器本机 OR 必须先授权)?  
(D5) acquisition_event 是否进 F-TRUST audit_events (链路公开)?  

## clean-room

参考 sub2api 文件 (specifier lane MAY read per CLAUDE.md #11 + Owner 2026-05-15 "可以参考sub2"):
- /home/codex/refs/sub2api/backend/internal/repository/claude_oauth_service.go (推测含 Anthropic OAuth callback handler)
- /home/codex/refs/sub2api/backend/internal/repository/openai_oauth_service.go (OpenAI OAuth)
- /home/codex/refs/sub2api/backend/internal/repository/gemini_oauth_client.go (Gemini OAuth)
- /home/codex/refs/sub2api/backend/internal/service/auth_oauth_email_flow.go (email-based OAuth)
- /home/codex/refs/sub2api/frontend/src/utils/oauthAffiliate.ts (frontend OAuth flow)

(具体 file:line citation 等 codex specifier lane 读后落实; Claude 这版 plan 是 strategic, 不引具体行号)

No verbatim copy. HUAKAI 升级 (架构/算法/生态) 列在上 fusion-upgrade table.

## sources read

- Owner 2026-05-15 quotes "对了，怎么获取这个功能你们也要做！" + "你要比他做的更简洁，更方便"
- memory `feedback_huakai_better_than_sub2api` (本 plan 触发了它)
- memory `project_core_trust_chain_differentiator` F-TRUST 卖点
- docs/03_FEATURE_PARITY_MATRIX.md (查 F-CRED-001 是否已 row — 待 codex specifier 加)
- (未读) sub2api 源码 — Claude lane strategic, 不读源; codex lane 读源 (CLAUDE.md #10 平行)
- /home/codex/.codex/auth.json (4.3K) 文件**存在性**确认 (auto-detect 路径)

## 与 codex F-CRED-001 plan (bj2ya2yww) 平行

Codex lane 在跑 specifier read sub2api source. Claude lane (本) 强调"更简洁更方便" 维度. Synthesis doc 完成时合并:
- 共识 mechanism (codex 抽 sub2api 实际 flow)
- Claude "更简洁" 维度 (1-click / auto-detect / batch CSV / failure fallback)
- Owner OCAW 5 选项
