# 2026-05-15 F-AUTH-005 upstream credential management (Claude 独立 plan)

| Lane | SPECIFIER (Claude); 平行 codex |
| Source | F-AUTH-005 spec (Released) + Owner 2026-05-15 sub2api auth-mode 调研 |
| Agent | Claude Opus 4.7 (1M context) |
| UTC | 2026-05-15T13:57:00Z |

## scope

管理上游凭证 — 每 vendor 5 auth mode:
- **anthropic**: api_key / claude_ai_oauth / claude_code / bedrock / vertex_anthropic
- **openai**: api_key / chatgpt_oauth / codex_cli_oauth / azure / refresh_token
- **gemini**: aistudio_api_key / vertex_sa / code_assist / google_one / antigravity

每 mode lifecycle 不同:
- API key: 静态,无 expire (除非吊销)
- OAuth (claude_ai/chatgpt/codex_cli): 短期 access_token + refresh_token; auto-refresh
- Bedrock / Azure: AWS/Azure auth pipeline (IAM/Entra ID); 短期 STS token
- Vertex SA: GCP service account JSON; 短期 metadata token
- Google One / Antigravity: TBD,等 Owner 凭证

## file-by-file impact

- `backend/internal/auth/credential/` (新建): vendor + mode 抽象
- `backend/internal/auth/credential/anthropic.go` / `openai.go` / `gemini.go` — 各 5 mode 实现
- `backend/internal/db/migrations/`: `account_credentials` table (encrypted at rest)
- `backend/internal/auth/refresh.go`: OAuth refresh scheduler (cron + on-demand)
- `backend/internal/admin/credential_handler.go`: 操作员 API CRUD + rotate
- `backend/internal/observability/credential_metrics.go`: Prometheus `huakai_credential_{active,refreshing,expired}_total{vendor,mode}`

## data model

```sql
CREATE TABLE account_credentials (
  id           UUID PRIMARY KEY,
  account_id   UUID NOT NULL REFERENCES accounts(id),
  vendor       VARCHAR(32) NOT NULL,    -- anthropic / openai / gemini
  auth_mode    VARCHAR(64) NOT NULL,    -- api_key / claude_ai_oauth / ...
  encrypted_blob BYTEA NOT NULL,        -- 加密的 raw credential
  expires_at   TIMESTAMP,               -- NULL for static (api_key)
  refresh_token_blob BYTEA,             -- OAuth refresh token, 加密
  last_used_at TIMESTAMP,
  health_state SMALLINT NOT NULL DEFAULT 0,  -- 0=ok/1=refreshing/2=expired/3=revoked
  created_at   TIMESTAMP NOT NULL DEFAULT NOW(),
  UNIQUE(account_id, vendor, auth_mode)
);
```

加密: KMS-wrapped DEK + AES-GCM on `encrypted_blob` / `refresh_token_blob`。Owner 决定 KMS provider (D1)。

## per-vendor per-mode flow

### anthropic.claude_ai_oauth (例)
1. Owner 提供初始 access_token + refresh_token (放 ~/secrets/anthropic/claude_ai_oauth.json)
2. F-AUTH-005 load → 加密入库 → schedule refresh 任务 (access_token expire 前 5min)
3. Proxy 出站时取 active token → Authorization: Bearer <token>
4. refresh 失败 → health_state=2 → 触发 F-OBS-003 计费 attribution to fallback 账号

### bedrock
1. Owner 提供 AWS access_key_id + secret_access_key + region
2. F-AUTH-005 用 STS:AssumeRole 获取短期 STS token
3. STS token 自动 refresh (1h before expire)
4. Proxy 用 SigV4 签名出站 (bedrock-runtime endpoint)

### vertex_anthropic / vertex_sa
1. Owner 提供 GCP service account JSON
2. F-AUTH-005 metadata.google.com 获取 access_token
3. Proxy 用 Authorization: Bearer + x-goog-user-project header

(其他 mode 类似,文件 by 文件)

## test plan

- unit: 每 vendor 每 mode 的 token-acquisition / refresh / expiry detection
- unit: 加密/解密 round-trip
- integration: mock OAuth provider,验证 refresh chain
- integration: 模拟 token expire 中间发请求,验证 auto-refresh + retry
- E2E (Owner 凭证): 真 anthropic / openai / gemini 跑 5 mode 各一次 (R-D smoke 已 scaffold)

## time estimate

7-10 天 codex (5 mode × 3 vendor = 15 实现 × 0.5 天) + 3 天 review + 2 天 KMS wiring = 12-15 天

## blast radius

**HIGH** — auth core + 凭证存储。一旦上线不能回退 schema; 加密 key rotation 需要 dual-key 过渡期。

## decision points

(D1) KMS 选型: AWS KMS / GCP KMS / Vault / 自托管 age  
(D2) refresh window: expire 前多久触发 (5min / 15min / 30min)  
(D3) refresh 失败 fallback: 立刻切换备用 vs 报错给用户  
(D4) 凭证 rotation 操作员是否要 2FA / approval  
(D5) audit_events 是否记录 credential 使用 (F-TRUST 信任链卖点; 但隐私 vs 透明取舍)

## clean-room

参考 (按 CLAUDE.md #11 lane lock + Owner 2026-05-15 "可以参考sub2" 允许 survey):
- sub2api: 据闻有 provider account model 抽象,但 5 mode 颗粒度不详
- new-api: AGPL,不读
- LiteLLM: MIT,可读;有 multi-key rotation 但不区分 oauth/bedrock/vertex 模式

HUAKAI 升级点 (架构 + 算法 + 生态):
- **5 mode × 3 vendor 显式枚举** — 没有现有 gateway 把 5 个 mode 都做齐 (sub2api/new-api 一般 1-2 mode)
- **STS / Vertex metadata pipeline 内置** — 不依赖外部 SDK 长链路
- **F-TRUST 链路公开** — credential 使用 audit 透明 (项目核心差异化卖点)

## sources read

- F-AUTH-005 row in parity matrix
- spec docs/specs/upstream-credential-management.md (Released 状态)
- memory project_core_trust_chain_differentiator
- Owner 2026-05-15 sub2api auth-mode survey directives
- (未读) sub2api / new-api / portkey / helicone 源码 — 声誉级引用
