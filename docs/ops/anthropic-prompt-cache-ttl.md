# Anthropic 提示缓存 TTL

本页说明 HUAKAI 对 Anthropic Messages 自动缓存断点的启用条件、TTL 策略、成本影响与观测口径。客户端显式发送的 `cache_control` 始终优先，网关不会替客户端改写已有断点。

## 两层开关

### 自动断点总开关

启动环境变量 `HUAKAI_CACHE_ANTHROPIC_AUTO_BREAKPOINTS` 控制是否启用自动断点规划：

- 未设置或设为 `false`：默认关闭，请求体保持既有字节语义，运行时 TTL 设置不生效。
- 设为 `true`：仅对 `anthropic_messages` 协议族、且请求内完全没有 `cache_control` 的请求规划自动断点。
- 客户端在 `system`、消息内容或工具定义任一位置自带 `cache_control` 时，整个自动规划步骤跳过，原请求体不改写。

该环境变量在进程启动时读取；修改后需要重启对应网关实例。实现入口见 `backend/internal/config/config.go` 与 `backend/internal/gateway/upstream_dispatcher.go`。

### 自动断点 TTL 运行时设置

平台设置键 `cache.anthropic_ttl_1h_rewrite` 是全局 bool 开关，默认值为 `false`：

- `false`：自动注入 `{ "type": "ephemeral" }`，不写 `ttl`，采用默认 5 分钟 TTL。
- `true`：只把网关自动注入的断点改为 `{ "type": "ephemeral", "ttl": "1h" }`。
- 设置读取失败时按 `false` 处理，不让缓存优化阻断实时请求。
- 多实例部署通过共享 `platform_settings` 更新；其它实例最迟在本地设置缓存过期后读取新值，当前缓存周期为 30 秒。

运行时设置不会替代环境变量总开关，也不会改写客户端已有的 `cache_control`。实现与默认值见 `backend/internal/platformsettings/cache_ttl_settings.go`、`backend/internal/platformsettings/types.go`。

## TTL 顺序约束

同一请求混用 1 小时和默认 5 分钟断点时，所有 1 小时断点必须出现在 5 分钟断点之前。`ValidateTTLOrdering` 从前向后扫描，一旦见到短 TTL，后续再出现 `ttl: "1h"` 就拒绝该方案；规则见 `backend/internal/gateway/cache_control.go:88-111`。

HUAKAI 的自动 1 小时路径会先给规划出的断点写入 `ttl: "1h"`，再经带顺序校验的应用函数生成请求体。客户端自带断点时不进入此路径，因此不会为满足排序而重排客户端内容。

## 每模型最小可缓存 token 阈值

缓存模块定义的当前阈值表来自 `backend/internal/gateway/cache_control.go:56-85`：

| 模型 | 最小可缓存 token |
| --- | ---: |
| `claude-opus-4-5` | 4096 |
| `claude-opus-4-6` | 4096 |
| `claude-opus-4-7` | 4096 |
| `claude-opus-4-1` | 1024 |
| `claude-opus-4` | 1024 |
| `claude-sonnet-4-6` | 2048 |
| `claude-sonnet-4-5` | 1024 |
| `claude-sonnet-4` | 1024 |
| `claude-sonnet-3-7` | 1024 |
| `claude-haiku-4-5` | 4096 |
| `claude-haiku-3-5` | 2048 |
| 未知模型保守回退 | 4096 |

`SuggestBreakpoints` 只在调用方提供块 token 估算时应用这些阈值；当前实时 dispatcher 没有块级估算，调用时传入 `nil`，因此阈值表目前用于运维评估而不是自动注入硬门。不要把“已插入断点”误判为“上游一定会缓存”；仍需确认模型名和实际可缓存前缀长度。

## 成本与观测

提示缓存写入会改变 input token 成本。当前策略口径下，默认 5 分钟写入按普通 input 的 1.25 倍计价，显式 1 小时写入按 2 倍计价。因此开启 `cache.anthropic_ttl_1h_rewrite` 是运营成本决策，不是无成本的命中率优化；上线前应按请求重复周期、缓存命中率和模型价格测算。

已结算用量在 `usage_records` 中分列记录：

- `cache_creation_5m_tokens`：默认 5 分钟 TTL 的缓存写入 token。
- `cache_creation_1h_tokens`：显式 1 小时 TTL 的缓存写入 token。
- `cache_read_tokens`：缓存读取命中 token。
- `cache_creation_tokens`：兼容既有总量口径；精细成本分析应优先看 5m/1h 分列。

管理端 `GET /admin/v1/usage` 会透出上述 5m/1h 分列。账号健康诊断端点 `GET /admin/v1/provider-accounts/{id}/recent-requests` 刻意不返回钱字段，只用于观察状态、时延、TTFT 与 token 量。

## 上线与回退

建议先在单个非关键实例开启环境变量，保持运行时设置为 `false`，确认自动断点数量、错误率和 `cache_creation_5m_tokens`；再按成本评估把运行时设置切到 `true`，观察 `cache_creation_1h_tokens` 与命中收益。

需要快速回退 1 小时策略时，把 `cache.anthropic_ttl_1h_rewrite` 设回 `false`；无需重启。需要完全停止自动注入时，把 `HUAKAI_CACHE_ANTHROPIC_AUTO_BREAKPOINTS=false` 并滚动重启网关。客户端显式断点在两种回退操作下都保持原样。
