# Anthropic CLI / Claude Code → AWS Bedrock 部署指南

> 适用 commit `27379bb` (A8 闭环) 之后. 本文档说明 ops 把 HUAKAI 配成
> "Anthropic CLI 直连 AWS Bedrock Claude" 所需的全部 seed/配置步骤。

## 闭环全景

```
Anthropic CLI / Claude Code
  → POST /v1/messages (HUAKAI)
  → MessagesHandler (gatewayhttp, EndpointFamily="messages")
  → Registry.ResolveModel(model_alias) → ProtocolFamily="bedrock_invoke"
  → Pool.Selector → 选 provider_account (account_type="aws_sigv4")
  → CredentialVault.Resolve → Credential{Type=AWSSigV4, Value=secret, Extra={...}}
  → Dispatcher → bedrock.PassthroughAdapter{AutoTranslateAnthropicAPIBody:true}
  → 翻译 body (剥离 model+stream, 注入 anthropic_version)
  → SigV4 sign
  → POST https://bedrock-runtime.{region}.amazonaws.com/model/{model_id}/invoke-with-response-stream
  → AWS Bedrock 二进制 EventStream
  → BedrockEventStreamScanner (gateway)
  → BedrockEventStreamAdapter (proto)
  → forwarder SSE 输出
  → CLI 收 Anthropic 事件 ✓
```

## 必须 seed 的 3 件事

### 1. provider_accounts: 录 AWS 凭据

```sql
INSERT INTO provider_accounts (
    tenant_id, account_type, status, credentials, ...
) VALUES (
    1,                                    -- 你的 tenant_id
    'aws_sigv4',                          -- account_type 必须是这个 (postgres_vault.go:194)
    'active',
    '{
        "aws_access_key_id":"AKIA...",
        "aws_secret_access_key":"wJal...",
        "aws_region":"us-east-1"
    }'::jsonb,
    ...
);
```

**注**: STS 临时凭据用 `"aws_session_token":"FQoD..."` 加进 JSONB；当前
HUAKAI **不自动刷新** STS（sonnet F5 SHOULD_FIX 已记案，phase 2 修）。
建议先用长期 IAM key + 严格 IAM policy 限定到 bedrock:InvokeModel*。

### 2. model_aliases: 把客户端 model 字符串映射到 bedrock_invoke

Anthropic CLI 会发 `"model":"claude-3-5-sonnet-20241022"`（Anthropic 命名）。
Bedrock 需要 `anthropic.claude-3-5-sonnet-20241022-v2:0`（Bedrock 命名）。

```sql
INSERT INTO model_aliases (
    tenant_id, public_alias, model_id, status, ...
) VALUES (
    1,
    'claude-3-5-sonnet-20241022',         -- 客户端发的
    <model_id>,                            -- FK to models.id
    'enabled',
    ...
);

-- models 行需声明 protocol_family + provider_model_id:
INSERT INTO models (
    canonical_id, protocol_family, provider_model_id, ...
) VALUES (
    'anthropic.claude-3-5-sonnet-20241022-v2:0',  -- Bedrock 的 model id
    'bedrock_invoke',                              -- 关键: 触发 Bedrock 路径
    'anthropic.claude-3-5-sonnet-20241022-v2:0',
    ...
);
```

具体字段见 `backend/sql/migrations/0008_model_registry.up.sql`。

### 3. 启用 PassthroughAdapter 的 AutoTranslate flag

**当前 gap (sonnet F4 SHOULD_FIX)**: `registrydefault/default.go:102` 注册的
是 `&bedrock.PassthroughAdapter{}` (AutoTranslate=false)。要让 Anthropic CLI
原始 body 自动翻译，需改为：

```go
// in backend/internal/provider/registrydefault/default.go
r.MustRegister(ProtocolBedrockInvoke, &bedrock.PassthroughAdapter{
    AutoTranslateAnthropicAPIBody: true,
})
```

或更精细：按 route binding 选择性启用（roadmap 未做）。

**临时 workaround**: 如果 registry 暂不改，部署时 fork registrydefault.Build()
本地版本，注入 AutoTranslate=true 的 PassthroughAdapter。或者上游 client
直接发 Bedrock 形态 body (含 anthropic_version, 不含 model/stream)。

## 已知限制 (post-A8)

| 项 | 描述 | 状态 |
|---|---|---|
| AutoTranslate registry 启用 | 见 §3 上方，默认未开 | SHOULD_FIX (admin 当前 fork) |
| STS 临时凭据自动续约 | `aws_session_token` 过期不自动刷新 | phase 2 改 |
| OpenAI client → Bedrock | 需要 OpenAI ClientAdapter 实现 | 未做 (Phase 4+) |
| /v1/responses → Bedrock | 同 OpenAI Responses adapter 缺失 | 未做 |

## 验证步骤

完成 §1-§3 seed 后:

```bash
# 1. 启动 HUAKAI gateway
cd backend && go run ./cmd/gateway/ --config config.yaml

# 2. 用 curl 模拟 Anthropic API 请求（用 HUAKAI API key）
curl -X POST http://localhost:8080/v1/messages \
  -H "Authorization: Bearer hk_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "messages": [{"role":"user","content":"Hello"}],
    "max_tokens": 256,
    "stream": true
  }'

# 期望: SSE 流, event: message_start ... event: message_stop

# 3. 用真 Anthropic CLI:
ANTHROPIC_BASE_URL=http://localhost:8080 ANTHROPIC_API_KEY=hk_your_api_key \
    anthropic-cli messages "Hello"
```

## Troubleshooting

| 错误 | 原因 | 解决 |
|---|---|---|
| 404 model_not_available | model_aliases / models 没 seed | 见 §2 |
| 503 gateway_not_configured | startup 依赖缺失 | 看 ChatHandlerDeps 检查 |
| Bedrock 400 ValidationException | body 形态错 | AutoTranslate 未启用，见 §3 |
| Bedrock 403 SignatureDoesNotMatch | sigv4 时间漂 / 凭据错 | 校 system clock + iam policy |
| Bedrock 403 (STS expired) | session_token 过期 | 用长期 IAM key 临时绕，phase 2 修 |

Lane: claude (synthesis)
Time: 2026-05-08T<UTC>
