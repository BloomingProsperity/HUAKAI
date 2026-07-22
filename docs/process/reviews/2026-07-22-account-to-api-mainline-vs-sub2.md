# 账号转 API · HUAKAI 主线 ↔ sub2api 对照报告（重写版）

| 项 | 值 |
|---|---|
| 日期 | 2026-07-22 (UTC) |
| **HUAKAI 唯一基准** | **`origin/main` @ `52d7f658`**（只读 worktree：`/home/ubuntu/HUAKAI-wt-mainline-read`） |
| sub2api 基准 | `/home/ubuntu/refs/sub2api` @ `5a8d6c4e` |
| 方法 | 主线 `.go`/`.sql`/主线 SSOT **真读**；grep 只定位 |
| 废止 | 此前基于 `feat/ui-density-overview` 的旧稿 **已删除**，不得再引用 |

> **强制口径**：出口 TLS / Go uTLS 是否还在、socket 默认值等，**只认 main**。  
> 主线出口 SSOT：`docs/architecture/egress-tls-mimicry-SSOT.md`（main 上已写明 **Rust sidecar 唯一 mimicry**）。  
> 路径无前缀时：HUAKAI 相对 `backend/`，sub2 相对 `backend/`。

---

## 0. 一句话结论（主线）

两边都是：

> 客户用平台 API Key 入站 → 选上游账号 → 取凭证打官方上游 → 回传 → 计费。

| 维度 | 主线 HUAKAI | sub2 | 谁强 |
|---|---|---|---|
| 凭证 at-rest | AES-256-GCM + AAD | JSONB **明文** | **我们** |
| 计费 | Tx1 预扣 + Tx2 Settle/Abort | 资格预检 + **后扣** | **我们** |
| 出口 TLS 伪装 | **仅 Rust BoringSSL sidecar**（Go uTLS **已删**） | 进程内 uTLS + 可配 profile | **我们（指纹栈）** |
| 上游配额进选号 | SQL 投影 + `upstreamQuotaGate` + `adaptive_score.quota_headroom` | OpenAI 双窗口 headroom 等 | **约平**（我们有通用信号；sub2 OpenAI 旋钮更贴厂商） |
| Body 装成 Claude Code | 库有 6 步；热路径**只点亮 metadata.user_id**；**无 billing 三块** | OAuth 非 CC 客户端 **全套 system 三块+工具+cache** | **sub2 更深** |
| 请求前 token 续命 | 后台 worker + 401 异步热刷；Antigravity 请求侧较完整 | 多厂商 TokenProvider 请求前 RefreshIfNeeded | **sub2 覆盖更全** |
| 运营批量/重置券 | 加密 bundle、voucher 批；账号 reset-quota/批改凭证弱 | 批量改凭证、reset-quota、导出含 secrets | **sub2 运营更全；我们更安全** |

---

## 1. 端到端骨架（主线）

```
HTTP /v1/chat|messages|responses
  → Auth.Resolve (租户 API key, Bearer)
  → 模型 allowlist + validate
  → prepareRoute: Registry + Router.Plan (pool_group 尝试序列)
  → ClaimGate.Reserve (Tx1 hold)                    ★预扣
  → QuotaReserver (可 fail-open)
  → Selector.Select:
        ListEligibleAccountsByPoolGroup
        + gates (含 upstreamQuota / health / model cooldown)
        + sticky / rankFresh(adaptiveScore) / PASR
        + DB slot + claim WriteAcquisition
  → CredentialVault.Resolve (AES-GCM 解密)
  → enforceOfficialClient / OfficialDirect
  → Adapter.BuildRequest
        + DispatchBodyControls (binding 级)
        + identityRewrite (仅 step5 user_id, 条件满足时)
  → TransportFactory:
        mimicry_* → **仅** Rust sidecar (/run/huakai/tls-sidecar.sock)
        standard  → Go net/http（官方 key）
  → 成功 Settle / 失败 Abort + 健康/冷却 + 换号
```

**合流**：选号与计费不看「客户 key 是不是 OAuth」。  
**分叉**：取凭证形态 + transport(mimicry|standard) + body 身份改写范围。

---

## 2. 出口 TLS（主线必读更正）

### 2.1 主线事实

| 项 | 主线代码/文档 |
|---|---|
| Go uTLS | **已删除**（main 无 `utls` 依赖、无 `utls_dialer.go`） |
| mimicry 出口 | **只允许** `sidecarRoundTripper` |
| 默认 socket | `DefaultSidecarSocketPath = "/run/huakai/tls-sidecar.sock"`（`transport/factory.go:97-98`） |
| config | `envDefault("HUAKAI_TRANSPORT_SIDECAR_SOCKET", DefaultTransportSidecarSocket)`（`config.go:272`） |
| socket 空 | mimicry **fail-closed** `sidecar_unavailable`，**禁止**降级 standard（`factory.go:155-164`） |
| 官方 API key | `TransportModeStandard`，Go `http.Transport`，`Proxy=nil` |
| 八类 profile | Rust sidecar 内置；缺 profile 拒启动/拒请求 |
| SSOT | main：`docs/architecture/egress-tls-mimicry-SSOT.md` §0–§1 |

### 2.2 「socket」在主线上的正确理解

- **不配 env 也有默认门牌**：`/run/huakai/tls-sidecar.sock`  
- 运维要保证：**sidecar 进程在该路径监听**（单镜像双进程 / 共享卷）  
- 路径上没人听 → 启动 preflight / 请求失败，**不会**回 Go uTLS（已无此路）  
- Body 伪装 **永远在 Go**；Rust **只做 TLS ClientHello/握手**

### 2.3 与 sub2

sub2 用进程内 uTLS（`internal/pkg/tlsfingerprint`）+ 可 CRUD 模板。部署更简单，指纹控制力弱于 BoringSSL sidecar。

---

## 3. 链路分节对照

### 3.1 入站鉴权

| | 主线 HUAKAI | sub2 |
|---|---|---|
| Key | Bearer；前缀查库 + bcrypt | Bearer / x-api-key / x-goog-api-key |
| 身份 | **Tenant** + User + APIKey | 用户 + Group（**无多租户**） |
| 管线 | chat/messages/responses **共用** chatExecution | Anthropic Gateway vs OpenAI Gateway **两套** |

### 3.2 选号（合流点）

#### 主线

1. **SQL** `ListEligibleAccountsByPoolGroup`（`sql/queries/pool_accounts.sql`）  
   - 池/启用/过期/健康/model_allow/协议/capability  
   - **凭据真相门**：EXISTS 可服务 `account_credentials`  
   - **JOIN 上游配额 facts**（state / remaining_percent / resets_at / observed_at，约 2h 窗）  
2. **Gate**：含 `upstreamQuotaGate`（exhausted 且新鲜 → 挡）、health、model_rate_limits 冷却…  
3. **rankFresh**：Priority / LoadRate / LastUsed + **`adaptiveScore`**（`adaptive_score.go`）  
   - `capacity_headroom`、`quota_headroom`（剩余%）、`near_reset_recovery`、reliability…  
4. **PASR** 可选（前缀缓存感知）  
5. **槽**：Postgres Serializable + lease + claim 绑定  

`AccountSnapshot` 含 `UpstreamQuota*` 字段（`pool/router/types.go:239+`），**不是**「探了不用」。

#### sub2

- L1 模型路由 / L1.5 sticky / L2 负载 / L3 排队  
- Redis 账号并发槽  
- OpenAI 专用调度：`openAIQuotaHeadroomFactor` 双窗口权重  

#### 差异（补缺用）

| ID | 项 | 主线 | sub2 | 建议 |
|---|---|---|---|---|
| S1 | 通用配额进选号 | **有** adaptive + gate | 有（平台散落） | 保持；核对各厂商 facts 是否写满 |
| S2 | OpenAI 5h/7d 双窗口语义 | 视 `UpstreamQuota*` 投影是否拆窗 | 专用 factor 很细 | **P2** 若 Codex 池要逐字节对齐 |
| S3 | 429 无 reset 兜底 | channelhealth 默认 **5min** | 默认 **5s** | **P0** 秒级可配（默认行为翻转=Owner-gated） |
| S4 | 多租户 SQL 绑死 | 有 | 无 | 保持 |

### 3.3 凭证

| | 主线 HUAKAI | sub2 |
|---|---|---|
| 存储 | `account_credentials` AES-GCM + AAD | `accounts.credentials` **明文 JSONB** |
| Resolve | 双侧 tenant + 解密 + RuntimeMaterial | map 直读 |
| 热路径刷新 | 401 → 异步 `RefreshHotPath`（去抖）；Antigravity 请求侧较完整 | Claude 等 **请求前** RefreshIfNeeded(skew) |
| 后台 | credentialworker + storm 三作用域 | TokenRefreshService 多平台 |

**补缺**：C1 **P1**——Claude/OpenAI/Gemini 统一请求前 skew 刷新，减首包 401（不要学明文存储）。

### 3.4 出站伪装（TLS + Body）

#### TLS（主线）

| 账号 | Mode | 实现 |
|---|---|---|
| 反转 anthropic oauth/session/… | `mimicry_claude_code` | **Rust sidecar only** |
| 反转 codex/chatgpt/gemini/… | 各 `mimicry_*` | 同上，8 profile |
| 官方 api_key | `standard` | Go net/http，不伪装 |

Header 层（Go adapter）：Claude 设备指纹、session 头、beta 白名单等仍在 OAuth adapter 上。

#### Body（主线 vs sub2）——最大产品差

| 步骤 | 主线热路径 | sub2 OAuth 非 CC 客户端 |
|---|---|---|
| system → **billing + 身份 + 扩充 三块** | **无** | **有** |
| 原 system 沉 messages | **无** | **有** |
| metadata.user_id | **有**（`mimicryidentity`，默认开；需 secret+external id+反转号+anthropic_messages） | 有 |
| 工具名混淆 | 库有，**热路径未串** | 有 |
| cache 策略 | 有 auto breakpoint（协议优化）；非 OAuth 全套 | 全套 + ≤4 |
| 真 Claude Code 少改 body | OfficialDirect 跳过 JSON 改写 | `shouldMimicClaudeCode=false` 跳过 |

证据（主线只开 step5）：

```
// mimicryidentity/identity.go — BuildPlan 注释与实现
// 仅启用 step5(metadata.user_id)；其余 5 步关闭
```

**sub2 方法摘要**（第三方 → 像 CLI）：  
OAuth 且非真 CC → 覆盖 system 为 3-block（billing 归因 / “You are Claude Code…” / expansion+cache）→ 原 system 作 messages 前缀对话 → normalize 字段 → tool/cache → 不透传冲突头 + 指纹头。  
**只 prefix 拼一句不够**——缺 billing 块会被当 third-party。

**我们怎么补（仍是 Go body，不碰 Rust）**：  
1. 自研 `ClaudeCodeCloak` 三块 + messages 下沉（clean-room）  
2. 门控：反转号 ∧ 非真 CC ∧ 兼容模式  
3. 真 CC / OfficialDirect：不改 body  
4. api_key：永不 body 伪装  
5. 再串工具名/cache；TLS 继续 sidecar  

### 3.5 Failover

| | 主线 | sub2 |
|---|---|---|
| 换号 | `ExcludedAccounts` + attempt budget + **auth 子预算 +1** | FailedAccountIDs + max switches |
| 同号重试 | 偏 classification | 一等（次数+间隔） |
| 401 | 异步热刷不阻塞换号 | temp_unsched + 清 cache |
| 粘性换号计费 | 未见 ForceCacheBilling 对等 | **ForceCacheBilling**（input→cache_read） |
| 流式后停 | 交付守卫 | writer size 硬守卫 |

**补缺**：F3 **P1** ForceCacheBilling 类粘性换号计费修正。

### 3.6 计费（合流点）

| | 主线 | sub2 |
|---|---|---|
| 模型 | **预扣** claim+hold → Settle/Abort | **后扣** Apply |
| 与 account_type | 无关 | 无关（账号倍率只影响账号侧统计） |
| 兜底 | intent + sweeper + lease + slot recovery | usage 后扣；批量图另有 hold |

**保持预扣**，勿为对齐 sub2 改后扣。

### 3.7 健康 / 429

| | 主线 | sub2 |
|---|---|---|
| 健康 | channelhealth FSM + 探针 | schedulable / rate_limited / temp_unsched |
| 401 | **auth 车道**与 health 分拆 | 常 temp_unsched / 永久 error |
| 429 默认冷却 | **5 分钟**（`channelhealth/types.go`） | **5 秒** |
| 配额 | facts 进 gate + adaptive | session window + OpenAI headroom |

### 3.8 采集 / 运营

| | 主线 | sub2 |
|---|---|---|
| OAuth 状态机 + PKCE 加密 | 有 | 各平台 OAuthService |
| 凭证导出 | 加密 accountbundle | 可导出明文 secrets |
| 批改凭证 / 账号 reset-quota | 弱/缺 | 强 |
| 重置券 | 弱/缺 | OpenAI reset credits |

---

## 4. 官方 key vs 反转号（主线内部）

| 维度 | 官方 API key | 反转 OAuth/session |
|---|---|---|
| 存储 | 加密 `api_key` 字段 | 加密 access/refresh |
| Transport | **standard**（Go HTTP） | **mimicry_*** → **Rust sidecar** |
| Body user_id | 不改 | 可改（条件满足） |
| Body system 三块 | 不改 | **主线仍未做** |
| 客户端门 | 默认宽 | 可 OfficialDirect / 强制官方 CLI |
| 刷新 | 通常无 | worker + 401 热刷 |

---

## 5. 建议补缺总表（按主线重算）

### 5.1 不要动的优势

- 凭证加密 + AAD  
- 多租户  
- 预扣 claim  
- **Rust-only TLS mimicry**（勿复活 Go uTLS）  
- auth/health 分车道  
- PASR + adaptive 选号骨架  
- 加密迁移包  

### 5.2 P0

| ID | 补什么 | 为何 |
|---|---|---|
| **O1** | Claude OAuth **system 三块**（billing + 身份 + expansion）+ 原 system 沉 messages | 第三方吃反转号时过上游检测的主干；**纯 Go** |
| **Q5** | 429 无 reset 默认冷却 → **秒级可配** | 5min 易饿死池；**Owner-gated 默认翻转** |

### 5.3 P1

| ID | 补什么 |
|---|---|
| O2 | 热路径串 tool 混淆 / OAuth cache 策略；真 CC 跳过 body |
| C1 | Claude/OpenAI/Gemini **请求前** skew 刷新 |
| F3 | 粘性换号 ForceCacheBilling 类修正 |
| A1/A2 | 批量改号/凭证 + 立即刷新（加密边界内） |
| Q6 | OpenAI 重置券（运营手动） |

### 5.4 P2

| ID | 补什么 |
|---|---|
| S2 | OpenAI 双窗口 headroom 细语义 |
| A3 | 克隆 / CRS / 富统计 UI |
| TLS 运营 UI | 动态 profile 管理体验（内核已 Rust） |

### 5.5 产品策略（Body 伪装，Owner 拍板）

| 策略 | 含义 |
|---|---|
| A | 只放官方 Claude Code，第三方 403 → **可不做 O1** |
| B | 开放第三方 → **必须做 O1** |
| C | 默认兼容伪装 + 可开「仅官方」 → **推荐** |

---

## 6. Anthropic 反转号 12 步验收（主线）

| 步 | 动作 | 主线现状 | vs sub2 |
|---|---|---|---|
| 1 | 客户 sk 入站 | 租户 API key | 对等+多租户更强 |
| 2 | 解模型/路由 | Router→pool | 对等 |
| 3 | 钱 | **先 hold** | 后扣 |
| 4 | 选号 | SQL+gate+adaptive 配额+DB 槽 | 配额约平；介质不同 |
| 5 | 取 token | 解密；401 异步刷 | 请求前刷更勤 |
| 6 | 客户端门 | OfficialDirect 等 | 策略不同 |
| 7 | body | **仅 user_id（有条件）** | **全套三块** |
| 8 | header | 设备指纹 | 指纹服务 |
| 9 | TLS | **Rust sidecar only** | uTLS |
| 10 | 上游错误 | health+auth+模型冷却 | ratelimit 状态机 |
| 11 | 换号 | 排除集+auth 预算 | 同号重试+ForceCacheBilling |
| 12 | 结算 | Settle/Abort | 后扣 |

---

## 7. 源码索引（只认 main 树）

| 域 | 主线路径 |
|---|---|
| 出口 SSOT | `docs/architecture/egress-tls-mimicry-SSOT.md` |
| Transport | `internal/transport/factory.go`；`mimicry/sidecar_*.go`；**无 utls** |
| 选号 | `pool/router/{default_selector,gates,adaptive_score,pasr}.go`；`sql/queries/pool_accounts.sql` |
| 凭证 | `credentialstore/{crypto,postgres_store}.go`；`provider/postgres_vault.go` |
| 出站 body | `mimicryidentity/*`；`gateway/mimicry_compose.go`；`gatewayhttp/chat_completions_stream.go` |
| 计费 | `billing/{claim_gate,settler,balancehold}.go` |
| Failover | `gatewayhttp/chat_completions_queue_wait.go`；`chat_completions_attempt.go` |
| sub2 body | `service/gateway_forward.go`；`gateway_claude_oauth_body.go`；`gateway_billing_block.go` |

---

## 8. 修订记录

| 日期 | 说明 |
|---|---|
| 2026-07-22 | **废止**基于 `feat/ui-density-overview` 的旧报告（含「默认 Go uTLS」等错误）。本文件按 **`origin/main@52d7f658`** 重读重写。 |

---

*只陈述主线源码事实与影响判断，不擅自开工。默认冷却翻转与 body 全伪装策略属 Owner-gated。*
