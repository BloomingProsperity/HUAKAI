# 2026-05-08 Upgrade #1 — binding-aware filter (claude lane plan)

## 升级目标

让路由 / 健康 / quota 三个子系统都"binding-aware"：

- **路由**：选 upstream account 时优先看 binding（user→tenant→account 链），不再纯 family round-robin
- **健康**：circuit breaker 按 binding 维度统计，bind 到的账号 lease 失败不影响 unbind 账号
- **quota**：未来与 #5 联动，按 binding 切分

区别 sub2api: sub2api 全局 round-robin + 全局 health → 多租户场景下，bound account 与 unbound 的统计互相污染。

## Scope

**In**:
- `binding_index` (in-memory cache): user → []account_id 映射，启动 + 增量刷新
- 路由器加 `binding-first` selection 选项
- health FSM 加 binding-key 维度的 stats slot
- 单元测试 + 集成测试

**Out**:
- 不引入新 binding storage (用现有 user_account_binding 表)
- quota binding 集成放 #5 之后
- admin UI 不动

## Atomic 拆分

| atomic | 内容 | 估时 |
|---|---|---|
| **U1-A** | `binding_index` cache 类型 + reload + 单测 | 60-90 min |
| **U1-B** | router 接入 binding-first selection 选项 | 60-90 min |
| **U1-C** | health FSM 加 binding-key dim + 单测 | 90-120 min |
| **U1-D** | e2e 测试: 同 family 多 account, 一 bound 一 unbound, bound 失败不污染 unbound stats | 60-90 min |

总: 4-6 小时.

## Decision points

- binding-first vs binding-prefer 还是 hard binding？→ **binding-first 软选择**（bound 失败时仍可 fallback unbind 账号，由 policy 控制）
- 缓存策略：全量加载 vs lazy？→ **启动全量 + 60s 增量**（user 量级 < 10w 全量可接受）
- 双键统计 (binding + global) → 双键并行，不替换全局

## Success criteria

1. bound 账号失败 N 次 → 该 user 的 binding-key health degrade，但 unbound 用户的全局 stats 不变
2. binding cache 启动 < 1s 加载完毕（10w user 规模）
3. 不破坏既有 routing 测试

## Blast radius

中：动 routing 与 health 两个 hot path。错路由会让请求打错账号；错 health 会让账号被错误熔断。

## Failure modes

1. binding cache stale → 60s 增量未跟上 → fallback 全局 routing
2. binding key 命名冲突 → 用 `(user_id, account_id)` 元组键
3. health FSM 双键内存爆 → 限制 bound key 总数 < 1M

Lane: claude
Time: 2026-05-08T<UTC>
