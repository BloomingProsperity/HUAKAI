# F-CLIENT-IDENTITY-001: Client Identity Detection, Persistence, and Sticky Cache Drift Protection

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-CLIENT-IDENTITY-001 |
| Specifier | Claude executor ae3eeb41fb8f000ac |
| Specifier date | 2026-05-06 |
| Reviewer | — |
| Review date | — |
| Released date | — |
| Lane mode | Option B (default) |
| Supersedes | — |
| Superseded by | — |

## Sources

> Reference material consulted by the specifier. Implementer lane MUST NOT open these.

- docs/plans/2026-05-02-huakai-algo-upgrade-synthesis.md — §2 A23/A24 决议 + §4 合并后清单 + §6 Owner Sign-off
- docs/decisions/DR-009-algorithm-upgrade-policy.md — Phase B/D 排序 + 客户透明度响应头清单

## Capability

Satisfies domain 11 "Client identity" in the synthesis coverage map (A23 + A24).

Local capability statement: HUAKAI 必须能从每个入站请求的多维信号中推断出客户端的稳定身份，以高置信度 score 标注该请求，并将结果写入 sticky 内存缓存；当缓存内容与当前请求信号不一致（漂移）时，系统必须检测到该漂移并以枚举原因码失效缓存条目，防止错误的 sticky 路由决策传播。

关联 Feature IDs：
- F-SESSION-001 — 会话持久化
- F-ROUTE-AFFINITY-001 — Sticky 路由亲和性（A04/A05）
- F-ACCAPI-BIND-001 — API key binding（A01/A09 spine）

## Actor

- **System** — 每个入站 API 请求触发 A23 检测器；A24 漂移检测器在 sticky_cache 查找时触发。
- **Operator** — 通过 identity_signal_config 配置信号权重和 TTL；通过日志观察漂移事件。
- **User（客户端）** — 间接受益人：身份探测结果影响 sticky 路由亲和性，从而影响 prompt cache 命中率。

## Preconditions

1. 入站请求已通过 API key 鉴权，`binding_id` 已确定（F-ACCAPI-BIND-001 前置）。
2. `identity_signal_config` 表存在且已加载到内存（version ≥ 1）。
3. `sticky_cache` 内存结构已初始化，key 空间为 `(identity_hash, binding_id)`。
4. `request_attempts` 表已存在基础列（A09 spine 前置，参见 DR-009 Phase B 依赖）。

## Normal Path

### A23 — Client Identity Priority Detector

1. **信号采集**：从入站请求提取以下 6 类信号，按 `identity_signal_config.weight` 降序处理：

   | signal_name | weight | spoof_class |
   |---|---|---|
   | `auth_key_binding` | 100 | `tamper_resistant` |
   | `vendor_session_metadata` | 80 | `medium` |
   | `stable_cli_header` | 70 | `low` |
   | `conversation_id` | 65 | `low` |
   | `client_request_id` | 40 | `negligible` |
   | `message_prefix_hash` | 25 | `negligible` |

   每个信号若存在则贡献其 `weight` 分；若缺失则贡献 0。

2. **基础 score 计算**：

   ```
   raw_score = Σ(weight_i × present_i)   for i in signals
   max_score = Σ(weight_i)               for all signals = 380
   base_confidence = raw_score / max_score
   ```

3. **spoof_penalty 扣分**：对每个 `spoof_class != tamper_resistant` 且信号值与历史记录不吻合的信号，按以下惩罚系数扣减：

   | spoof_class | penalty_factor |
   |---|---|
   | `medium` | 0.15 |
   | `low` | 0.08 |
   | `negligible` | 0.02 |

   ```
   spoof_total_penalty = Σ(weight_i × penalty_factor_i × mismatch_i)
   ```

4. **churn_penalty 扣分**：若该 `binding_id` 在过去 `ttl` 秒内 identity_hash 变化次数超过阈值（默认 3 次），追加 churn_penalty：

   ```
   churn_penalty = 0.10 × base_confidence × (churn_count / churn_threshold)
   ```

5. **最终置信度**：

   ```
   identity_confidence = max(0.0, base_confidence - spoof_total_penalty - churn_penalty)
   ```

   取值区间 [0.0, 1.0]，≥ 0.70 视为高置信，0.40–0.69 为中置信，< 0.40 为低置信。

6. **identity_hash 生成**：对所有 `present_i = true` 的信号值拼接后做 HMAC-SHA256（密钥为系统级 identity_hmac_secret），取十六进制摘要前 32 字符。**原始 user_id / session_id 明文不持久化。**

7. **写入 request_attempts**：将 `identity_signal_class`（最高权重且 present 的信号名）、`identity_confidence`、`identity_hash` 写入当前 attempt 行。

8. **写入 sticky_cache**：以 key `(identity_hash, binding_id)` 写入内存缓存，值包含：
   - `identity_confidence`
   - `identity_signal_class`
   - `capability_version`（当前 `identity_signal_config.version`）
   - `cached_at` 时间戳
   - TTL 取命中信号中最小的 `identity_signal_config.ttl`

### A24 — Identity Cache Drift Detector

1. **缓存查找**：以当前请求的 `(identity_hash, binding_id)` 查找 `sticky_cache`。

2. **HIT 路径 — 一致性校验**：若命中，比较缓存条目的 `capability_version` 与当前 `identity_signal_config.version`：
   - 若版本相同，直接使用缓存，不触发漂移检测。
   - 若版本不同，进入漂移处理（步骤 4）。

3. **MISS 路径**：缓存未命中，记录 MISS reason，转 A23 重新计算：

   | MISS reason | 触发条件 |
   |---|---|
   | `MISS_NOT_FOUND` | key 从未写入或已过期 |
   | `MISS_TTL_EXPIRED` | key 存在但 TTL 已过 |
   | `MISS_CAPABILITY_VERSION_CHANGED` | key 存在，版本不匹配 |

4. **漂移处理**：
   - 将缓存条目标记为 `drift_invalidated = true`，记录 drift 原因和时间戳。
   - 以当前请求重新运行 A23 得到新 `identity_hash` 和 `identity_confidence`。
   - 若新旧 `identity_hash` 不同，记录 `identity_drift_event`（包含旧 hash 前 8 字符、新 hash 前 8 字符、`binding_id`、reason）。
   - 以新计算结果写入缓存（覆盖旧条目）。

5. **sticky 路由影响**：`identity_confidence < 0.40` 时，通知路由层降级为无亲和性路由（不影响 billing，仅影响账号选择亲和性）。

## Failure Path

### Failure: identity_hmac_secret 未配置

- **Trigger**：`identity_hmac_secret` 环境变量或密钥服务返回空值。
- **Observable outcome**：A23 检测器拒绝生成 identity_hash，请求以 `identity_signal_class = none`、`identity_confidence = 0.0` 继续（降级，不中断请求）。
- **Operator-visible signal**：ERROR 级别日志 `identity_hmac_secret_missing`；告警计数器 `identity_hash_generation_failures_total` 累加。

### Failure: identity_signal_config 版本回退

- **Trigger**：`identity_signal_config.version` 读到比缓存条目低的值（配置回滚场景）。
- **Observable outcome**：A24 将该情况视为 `MISS_CAPABILITY_VERSION_CHANGED`，强制重新计算；不使用旧版本权重结果。
- **Operator-visible signal**：WARN 级别日志 `identity_config_version_regression`，含旧版本号和当前版本号。

### Failure: sticky_cache 内存压力

- **Trigger**：`sticky_cache` 条目数超过运行时上限（默认 100,000）。
- **Observable outcome**：LRU 淘汰最久未访问条目；被淘汰的 key 下次查找触发 `MISS_NOT_FOUND`，重新计算，无数据丢失（仅 sticky 亲和性短暂丢失）。
- **Operator-visible signal**：INFO 级别日志 `sticky_cache_eviction`；指标 `sticky_cache_size`、`sticky_cache_evictions_total`。

### Failure: 高 churn（身份频繁切换）

- **Trigger**：同一 `binding_id` 在 TTL 窗口内 identity_hash 变化次数 ≥ churn_threshold × 2。
- **Observable outcome**：`identity_confidence` 被 churn_penalty 压低至低置信区间；路由层降级为无亲和性路由。
- **Operator-visible signal**：WARN 级别日志 `identity_high_churn`，含 `binding_id`（脱敏）、churn_count、TTL 窗口。

## Operator Recovery

- **HMAC secret 缺失**：配置 `identity_hmac_secret` 并重启服务；历史请求不受影响（已记录 `identity_signal_class = none`，可事后 replay 分析）。
- **配置版本回退**：将 `identity_signal_config.version` 恢复到预期值；已漂移的缓存条目会在下次请求时自动重建。
- **内存压力**：调整运行时 `sticky_cache_max_entries` 配置项，或横向扩容实例（每实例独立内存缓存，无分布式一致性要求）。
- **高 churn 误报**：通过 Operator 控制台调整 `churn_threshold` 或对特定 `binding_id` 豁免 churn_penalty；需写入 `identity_signal_config` 覆盖记录。

## Audit / Usage / Log Evidence

### request_attempts 表新增列

| 列名 | 类型 | 说明 |
|---|---|---|
| `identity_signal_class` | TEXT | 最高权重且 present 的信号名；若无信号则 `none` |
| `identity_confidence` | REAL | [0.0, 1.0]，A23 计算结果 |
| `identity_hash` | TEXT(32) | HMAC-SHA256 前 32 字符；原始身份明文不存储 |

### identity_signal_config 表

| 列名 | 类型 | 说明 |
|---|---|---|
| `version` | INTEGER | 配置版本号，单调递增 |
| `signal_name` | TEXT | 信号标识符（枚举值见 A23 表格） |
| `weight` | INTEGER | 信号权重 [0, 100] |
| `ttl` | INTEGER | 信号对应的缓存 TTL（秒） |
| `spoof_class` | TEXT | `tamper_resistant / medium / low / negligible` |

### sticky_cache（内存，不持久化）

| Key 字段 | 说明 |
|---|---|
| `identity_hash` | A23 生成的 HMAC hash（32 字符） |
| `binding_id` | 当前请求的 API key binding ID |

| Value 字段 | 说明 |
|---|---|
| `identity_confidence` | REAL |
| `identity_signal_class` | TEXT |
| `capability_version` | INTEGER，与 identity_signal_config.version 对应 |
| `cached_at` | Unix timestamp（毫秒） |
| `ttl_ms` | 到期时长（毫秒） |
| `drift_invalidated` | BOOLEAN，A24 标记用 |

### 结构化日志事件

| 事件名 | 级别 | 触发条件 |
|---|---|---|
| `identity_detected` | DEBUG | 每次 A23 成功完成 |
| `identity_cache_hit` | DEBUG | A24 HIT 且版本一致 |
| `identity_cache_miss` | INFO | A24 MISS（含 reason） |
| `identity_drift_event` | INFO | A24 漂移检测，hash 变化 |
| `identity_high_churn` | WARN | churn_count ≥ 2×churn_threshold |
| `identity_hmac_secret_missing` | ERROR | HMAC 密钥缺失 |
| `identity_config_version_regression` | WARN | 配置版本回退 |
| `sticky_cache_eviction` | INFO | LRU 淘汰发生 |

## Acceptance Test Direction

测试 ID 区间 `AT-IDENTITY-001` 至 `AT-IDENTITY-008`，应在 [docs/11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md) 登记。

| AT-ID | 类型 | 场景描述 |
|---|---|---|
| AT-IDENTITY-001 | Normal | 全 6 信号均存在，验证 identity_confidence 接近 1.0，identity_hash 为 32 字符十六进制，identity_signal_class = `auth_key_binding` |
| AT-IDENTITY-002 | Normal | 仅 `auth_key_binding` + `conversation_id` 存在，验证 confidence = (100+65)/380 ≈ 0.434，归类中置信 |
| AT-IDENTITY-003 | Normal | A24 HIT 且 capability_version 一致，验证缓存直接命中，不重新计算，无漂移日志 |
| AT-IDENTITY-004 | Normal | A24 `MISS_CAPABILITY_VERSION_CHANGED`：config version +1 后触发，验证缓存重建、`identity_drift_event` 写入 |
| AT-IDENTITY-005 | Failure | HMAC secret 置空：验证请求不中断，`identity_signal_class = none`，`identity_confidence = 0.0`，ERROR 日志触发 |
| AT-IDENTITY-006 | Failure | spoof 信号注入：`vendor_session_metadata` 值与历史不一致，验证 spoof_penalty 扣减后 confidence 下降 ≥ 0.10 |
| AT-IDENTITY-007 | Failure | 高 churn：同一 binding_id 在 TTL 内触发 ≥ 2×churn_threshold 次 hash 变化，验证 WARN 日志 + confidence 下降 + 路由降级信号 |
| AT-IDENTITY-008 | Recovery | sticky_cache 超过 max_entries 上限，验证 LRU 淘汰后旧 key 触发 `MISS_NOT_FOUND`，重新计算后缓存正常恢复 |

## Open Questions

1. **identity_hmac_secret 轮换策略**：密钥轮换时，已缓存的 identity_hash 将全部失效（因 HMAC 输出变化）。是否需要双密钥过渡窗口（old_secret + new_secret 同时有效）？建议 Operator 评估。
2. **跨实例 sticky_cache 一致性**：当前设计为每实例独立内存缓存，水平扩容时同一客户端可能命中不同实例的不同缓存状态。是否需要引入分布式缓存层（如 Redis）？若引入，需与 F-ROUTE-AFFINITY-001 协同设计。
3. **`message_prefix_hash` 隐私边界**：该信号对消息体前 N 字节做 hash，是否符合 DR-009 §Decision-5 drain 隐私边界（只看 token usage 元数据，不读 prompt body）的精神？建议 Owner 确认 prefix_hash 是否属于"读 prompt body"范畴。
4. **churn_threshold 默认值**：当前默认 3 次/TTL，具体数值需 A/B 测试验证；建议在 `identity_signal_config` 中以独立配置行存储，不硬编码。

## Implementer Notes (added by implementer lane)

> This section is filled by the implementer after consuming the spec, NOT by the specifier.

- (空)
