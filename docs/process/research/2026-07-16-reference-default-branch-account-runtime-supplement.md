# 2026-07-16 默认分支账号运行时补充证据（隔离 specifier）

## 元数据

| 项目 | 值 |
| --- | --- |
| Lane | specifier |
| Artifact | `2026-07-16-reference-default-branch-account-runtime-supplement` |
| CLIProxyAPI | `09da52ad509e2c18e7b9540db3b98c2214c280aa`，执行时已核实等于远端 `main` |
| New API | `a63364d156cf2a64f1c3d1ee4923d73d5f3222a1`，执行时已核实等于远端 `main` |
| Sub2API | 仅满足默认三镜护栏；本 artifact 未读取其源码、未形成新结论 |
| Observed regions | 21 |
| Inferences | 0 |
| Open questions | 0 |
| 事实纪律 | 以下内容均来自锁定 SHA 的实际源码观察；未复制源码、函数名、字段名或上游注释 |

## 1. CLIProxyAPI 凭据选择

**Observed：候选会先按请求模型排除整体禁用、目标模型禁用和仍在不可用窗口内的凭据，再只保留数值最高的可用优先级。** 同一优先级内使用稳定标识排序，避免输入集合顺序改变结果。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:199` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:219`

**Observed：最高优先级内支持两种分配方式。** 一种按“提供方 + 归一化模型”维护独立轮转进度；另一种持续选择稳定排序后的首个可用凭据，直到它退出候选集。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:256`

**Observed：逐模型冷却会计算实际恢复点。** 普通重试时间和额度恢复时间同时存在时使用更晚者；全部候选都因目标模型冷却时，返回最早可恢复候选对应的 HTTP 429 和 `Retry-After`。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:46` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:305`

## 2. CLIProxyAPI 自动续期与并发保护

**Observed：自动续期按每份凭据的下一检查时间调度。** 运行时使用时间有序队列、最近到期定时器和固定 worker；凭据状态变化会触发重新计算，不是固定周期全量扫描。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/auto_refresh_loop.go:13` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/auto_refresh_loop.go:62` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/auto_refresh_loop.go:122`

**Observed：任务入队前会重新读取最新凭据状态。** API key 和已进入未授权终态的凭据不再排期；其余凭据可按显式间隔、到期提前量和上次续期时间计算下一次检查。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/auto_refresh_loop.go:221` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/auto_refresh_loop.go:338`

**Observed：请求前凭据补全和刷新都按凭据隔离并发。** 等待锁的请求会重新读取最新状态；若另一请求已经刷新并替换访问令牌，等待者复用新状态，不重复消费刷新令牌。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/conductor.go:2956` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/conductor.go:5887` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/conductor.go:5926`

## 3. CLIProxyAPI 文件热加载

**Observed：观察器同时监听配置文件和凭据目录。** 配置接受写入、创建和重命名事件；凭据目录只处理直属 JSON 文件的创建、写入、删除和重命名，并在启动时先完成一次初始加载。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/events.go:29` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/events.go:67`

**Observed：凭据变化按新增、修改、删除增量传播。** 内容哈希相同会跳过；删除或重命名会留出原子替换窗口，路径重新出现时按修改处理。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/events.go:91` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/events.go:120` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/dispatcher.go:145`

**Observed：配置重载和凭据删除都有 debounce。** 配置内容哈希未变化或文件为空时不会重载；新配置成功解析后替换内存配置并触发客户端重载。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/config_reload.go:20` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/config_reload.go:51` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/events.go:172`

## 4. New API 自动分组

**Observed：自动分组按当前用户可访问的配置顺序搜索。** 请求上下文保存当前分组位置；组内用请求重试进度推进优先级，当前组没有可用渠道或重试额度耗尽后，下一轮从后续分组继续。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/group.go:44` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_select.go:84` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_select.go:107`

**Observed：外层 relay 重试复用同一请求上下文和可变重试进度。** 失败允许重试时再次选号，并在重试前复位请求体，因此分组位置和组内优先级只在当前请求生命周期内流转。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/relay.go:181`

## 5. New API 亲和路由

**Observed：亲和键可按规则依次从请求上下文整数、请求上下文字符串、请求头或 JSON 正文路径提取。** 规则还可约束模型、请求路径、User-Agent 和亲和值。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:289` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:550`

**Observed：亲和映射使用本地 TTL/LRU 与可选 Redis 的混合缓存。** 支持全量清空、按规则清空和当前请求精确键删除；统计包含总量、未知键、按规则数量、容量和淘汰算法。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:81` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:111` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:198` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel_affinity_cache.go:20`

## 6. New API 渠道测试

**Observed：渠道测试复用正常渠道上下文装配和 relay 信息构建。** 测试路径会装配渠道配置、参数与请求头覆盖、模型映射和多密钥状态，并把测试标志写入 relay 上下文。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:95` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:middleware/distributor.go:443` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:235`

**Observed：测试协议由显式端点类别或模型、模型后缀和渠道类型共同决定。** 不同端点构造不同请求形态，最终仍通过正常渠道适配器完成转换、发送和响应处理。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:98` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:185` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:305` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:411`

## 真实性摘要

本 artifact 在远端默认分支可达 SHA 上确认了六类行为：CLIProxyAPI 的最高优先级凭据选择、模型冷却恢复、按到期时间续期、单凭据并发保护和文件增量热加载；New API 的请求级跨组重试、混合亲和缓存和真实 relay 链复用测试。全部内容为实际观察，Inferences 为 0，Open questions 为 0。

Source files read: CLIProxyAPI:sdk/cliproxy/auth/selector.go, sdk/cliproxy/auth/conductor.go, sdk/cliproxy/auth/auto_refresh_loop.go, internal/watcher/watcher.go, internal/watcher/events.go, internal/watcher/config_reload.go, internal/watcher/dispatcher.go; new-api:setting/auto_group.go, service/group.go, service/channel_select.go, service/channel_affinity.go, controller/channel_affinity_cache.go, controller/channel-test.go, controller/relay.go, middleware/distributor.go, model/ability.go, model/channel_cache.go
Lane: specifier
Agent: OpenAI Codex GPT-5 /root
UTC timestamp: 2026-07-16T11:16:49Z
