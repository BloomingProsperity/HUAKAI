# 2026-05-08 Upgrade #6 — client identity detector (claude lane plan)

## 升级目标

每个进 HUAKAI 的请求识别"真实客户端身份"：是 Cursor / Claude Code / Cody / 自定义 curl 脚本 / 还是某 SDK。识别结果用于：
- per-client quota 切分
- abuse detection
- 客户端兼容协议适配（如 Cursor 期望 OpenAI shape，Claude Code 期望 Anthropic shape）
- 强伪装层准备（execution_boundary_c 暂停，detector 先就位）

区别 sub2api: sub2api 不分客户端身份，所有 caller 一类。

## Scope

**In**:
- `client_detector` package：从 HTTP headers (User-Agent / X-Client-Name / Origin / 自定义) + 路径 + body fingerprint 推断 client identity
- 输出 enum: `cursor / claude_code / cody / chat_ui / curl_script / unknown`
- middleware 在请求入口注入 detected identity 到 ctx
- 不实施 fingerprint 强伪装（execution_boundary_c）

**Out**:
- 不实施反向行为模拟（强伪装层 R5/R7/R8 暂停）
- 不动 quota / billing — 只先做 detector + ctx 标签

## Atomic 拆分

| atomic | 内容 | 估时 |
|---|---|---|
| **U6-A** | client_detector package + identity enum + Detect(headers) → identity | 60-90 min |
| **U6-B** | middleware 注入 ctx + 单测 | 30-60 min |
| **U6-C** | per-client metrics (用于 abuse detection 基线) | 60-90 min |
| **U6-D** | identity → protocol adapter 映射策略（Cursor 默认 OpenAI shape） | 60-90 min |

总: 3-5 小时.

## Decision points

- 是否信任 user-agent? → **作为信号之一不独信**，多信号融合
- 多信号冲突时 fail-open 还是分级? → **降级为 unknown 标签**，让上层 policy 决定
- 信号融合策略: 累计权重 vs 决策树? → **决策树**（可读性更好，covered by tests）

## Success criteria

1. 测试 fixture (Cursor / Claude Code / Cody / curl) → 识别准确率 > 95%
2. 未知客户端 → 标 `unknown`，不阻塞请求
3. ctx 注入开销 < 100µs (hot path)

## 设计大纲

### detector

```go
package clientid

type Identity string
const (
    IdentityCursor      Identity = "cursor"
    IdentityClaudeCode  Identity = "claude_code"
    IdentityCody        Identity = "cody"
    IdentityChatUI      Identity = "chat_ui"
    IdentityCurlScript  Identity = "curl_script"
    IdentityUnknown     Identity = "unknown"
)

type Signal struct {
    UserAgent string
    Path      string
    Headers   http.Header
    BodyHints []string  // 已 sniff 的 body 关键字段名（不含值）
}

func Detect(s Signal) (Identity, float64) // identity + confidence 0-1
```

### middleware

```go
func ClientIdentityMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        s := signalFromRequest(r)
        id, conf := clientid.Detect(s)
        ctx := clientid.WithIdentity(r.Context(), id, conf)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

## 测试矩阵

1. User-Agent="Cursor/0.42 ..." → IdentityCursor confidence > 0.9
2. User-Agent="claude-cli/1.0 (Claude Code; ...)" → IdentityClaudeCode
3. 无 User-Agent + body 含 `tools_choice: "required"` → IdentityCurlScript fallback
4. 多信号冲突 (UA=Cursor 但 Origin 是其它) → 降级 unknown + log 警告
5. 大量请求并发 → 无 panic

## Blast radius

低：纯 read-only middleware，不影响请求路径；只加 ctx 标签和 metrics。

## Failure modes

1. UA spoof → 多信号融合提升鲁棒
2. 误识别 → fallback unknown，不拒绝请求
3. metrics cardinality 爆 (per-client per-tenant) → 限制 cardinality buckets

Lane: claude
Time: 2026-05-08T<UTC>
