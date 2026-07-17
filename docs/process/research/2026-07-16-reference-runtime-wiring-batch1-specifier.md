# 2026-07-16 参考项目生产运行接线研究 Batch 1（specifier）

## 元数据

| 项目 | 值 |
| --- | --- |
| Lane | specifier |
| 研究范围 | CLIProxyAPI、sub2api、new-api 的运行时接线 |
| Observed regions | 22 |
| Inferences | 4 |
| Open questions | 6 |
| 研究日期 | 2026-07-16 UTC |
| 修改范围 | 仅本报告 |

## 真实性与快照限制

本轮首先检查了三个本地镜像的远端、默认分支指针和远端跟踪提交。镜像目录为只读挂载，执行更新时无法写入 Git 的抓取状态文件；随后尝试直接查询 GitHub，运行环境又因 DNS 不可用而失败。因此，以下 SHA 是本地镜像已经保存的 `origin/main`，不是本轮在线重新确认的 GitHub HEAD：

- CLIProxyAPI：`09da52ad509e2c18e7b9540db3b98c2214c280aa`，提交时间为 2026-07-16。
- sub2api：`393a8fe56a0b606d162183cf8014f9381adcbf7e`，提交时间为 2026-07-16。
- new-api：`a63364d156cf2a64f1c3d1ee4923d73d5f3222a1`，提交时间为 2026-07-14。

因此，本报告能证明“这些本地远端跟踪快照中的行为”，但不能诚实声称三个 SHA 在报告完成时仍等于 GitHub 当前 HEAD。所有结论均限定在上述快照。

## 一、CLIProxyAPI 的 runtime wiring shape

### 1. 组合根与核心对象保留

**Observed。** 进程入口先创建一个长期存活的扩展宿主，并在解析命令行前让已加载的扩展参与启动参数注册。这意味着扩展不是请求到来时临时发现，而是在组合根阶段进入运行时对象图。随后入口按配置选择一种共享凭据持久层，并把它注册为全局唯一来源，再注册内建访问提供者；这种顺序使服务构造时拿到的是已经确定的共享后端，而不是各模块自行创建副本。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:cmd/server/main.go:139`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:cmd/server/main.go:559`

**Observed。** 核心代理服务持有扩展宿主、认证管理器、访问管理器、配置、文件观察器等长期对象；启动阶段把扩展执行器、认证解析器、前端认证提供者、用量消费者和模型目录依次接入这些共享管理器。该形态的关键不是“构造过对象”，而是“构造后立即注册到请求执行所依赖的共享注册中心”。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/service.go:132`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/service.go:190`

### 2. HTTP 主入口、管理入口和中间件

**Observed。** HTTP 服务构造允许调用方在默认中间件之前调整引擎、附加额外中间件，并在默认路由完成后追加路由。这形成明确的扩展时序，减少扩展入口因注册过早或过晚而失效。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/api/server.go:74`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/api/server.go:112`

**Observed。** 管理路由使用原子状态防止重复挂载；在管理能力满足配置条件后才接入，同时把最终已注册的管理路由集合通知扩展宿主。这个回传动作使扩展侧能看到真实运行时入口，而不是只相信静态声明。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/api/server.go:244`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/api/server.go:801`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/api/server.go:990`

### 3. 后台任务、停止与 producer/consumer 防漏接

**Observed。** 用量分发器具有幂等启动和幂等停止；发布记录时即使组合根遗漏显式启动，也会兜底启动后台消费者。停止时先取消工作上下文，再封闭队列并唤醒等待者。这是直接防止“producer 已经发布、consumer 从未启动”的机制。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/usage/manager.go:219`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/usage/manager.go:242`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/usage/manager.go:307`

**Observed。** 文件与凭据观察器保存配置快照、变更队列、待分发集合、取消句柄和停止状态；启动、停止、配置更新和队列注入都有显式入口。文件来源和外部运行时来源最终都进入同一增量更新通道，避免其中一种凭据来源绕过统一认证状态。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/watcher.go:32`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/watcher.go:122`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/watcher.go:151`

**Observed。** 服务关闭路径显式停止后台工作、HTTP 服务、性能诊断服务和扩展宿主，并清除扩展注入到全局管理器的状态。这表明生命周期所有权保留在核心服务，而不是散落给各请求处理器自行结束。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/service.go:1783`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/service.go:1843`

### 4. 配置进入真实消费者

**Observed。** 配置可以来自控制面、数据库型持久层、对象存储、Git 型存储或本地文件；入口在服务构造前决定来源，并把解析后的同一配置对象交给扩展同步、访问提供者和核心服务。控制面模式下，获取或解析失败会直接终止启动，而不是用空配置继续运行。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:cmd/server/main.go:269`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:cmd/server/main.go:287`

### 5. 请求链共享身份与状态

**Observed。** 用量记录同时保存提供者、实际执行器、模型、客户端别名、凭据身份、凭据类型、失败信息、延迟和 token 明细；请求上下文还携带客户端请求的模型别名、推理等级、服务等级和是否实际生成。这样执行选择、别名翻译和用量消费者可以围绕同一次请求共享同一组身份数据。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/usage/manager.go:21`，`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/usage/manager.go:74`

**Inferred。** 其主要防漏接策略是“共享注册中心 + 生命周期归核心服务 + 发布侧自启动兜底 + 热更新统一进入同一认证管理器”。这是从上述多个已观察区域归纳出的保证形式，不代表源码中存在同名设计声明。

## 二、sub2api 的 runtime wiring shape

### 1. 组合根与核心对象保留

**Observed。** 正常启动先加载引导配置，再由生成式依赖装配入口构造完整应用；返回值同时保留 HTTP 服务和统一清理闭包。进程入口延迟调用清理闭包，因此只要应用构造成功，后台服务与基础设施资源就有一个集中所有者。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:134`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:151`

**Observed。** 依赖装配把账户仓储、调度快照、计费缓存、额度、并发、价格、身份、渠道和设置等服务注入网关服务，再把网关服务注入 HTTP 处理器。核心运行对象不是仅被创建，而是沿构造链最终抵达路由所调用的处理器。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire_gen.go:61`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire_gen.go:140`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire_gen.go:258`

### 2. HTTP 主入口、alias、管理员入口和中间件

**Observed。** 路由组合根先挂载请求日志、会话绑定、通用日志、跨域、安全头和耗时统计，再统一注册公共、认证、用户、管理员、网关和支付路由。管理员路径显式取得管理员认证、审计和强化验证中间件；网关入口则取得 API key 认证及订阅、运维和设置消费者。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/router.go:55`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/router.go:94`

**Observed。** 主网关协议组在认证之前统一经过请求体限制、请求身份、错误记录和端点规范化。无版本前缀别名和专用客户端入口重复使用同一组前置中间件及同一处理器，而不是复制一条缩减链；原生 Gemini 入口也具备对应的请求身份、错误记录、端点规范化和专用认证。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/routes/gateway.go:110`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/routes/gateway.go:214`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/routes/gateway.go:229`

**Observed。** 同一公开协议入口依据已认证分组的平台属性选择实际处理器；不支持的能力会明确记录业务受限原因并返回不可用，而不是误落到另一协议实现。这减少“某协议存在路由但绕开平台能力约束”的风险。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/routes/gateway.go:120`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/routes/gateway.go:180`

### 3. 后台服务、停止和失败协作

**Observed。** 统一清理闭包显式接收运维采集、聚合、告警、审计、调度快照、token 刷新、账户过期、代理过期、订阅过期、用量清理、异步图像、邮件、计费缓存、用量写入池、定时测试、备份、支付过期、渠道监控、额度刷新等长期对象。这些对象被保留进清理根，构造后不会因局部变量丢失而失去停止路径。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire.go:72`

**Observed。** 应用层后台对象先并行停止，Redis 和数据库等基础设施后关闭；每个步骤独立返回错误，清理阶段具有总超时。这一顺序保护后台消费者在其依赖被关闭前先退出。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire.go:109`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire.go:118`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire.go:298`

**Observed。** HTTP 服务在独立协程运行，主协程等待终止信号，先进行带时限的 HTTP 优雅关闭，再由延迟清理闭包停止后台对象。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:155`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:166`

### 4. 配置和设置进入真实消费者

**Observed。** 运行模式在组合根加载，并会改变计费和额度检查是否启用；该差异在启动日志中显式告警。设置服务还把更新回调接到前端缓存失效和安全来源刷新，说明数据库设置变更不止被保存，还会驱动运行中消费者更新。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/main.go:134`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/router.go:69`

### 5. 请求链共享身份与状态

**Observed。** 网关路由在所有主要协议和别名入口前统一注入客户端请求身份、端点规范化结果、认证后的分组平台和运维错误记录器；同一处理器对象又持有调度、账户、计费、额度、并发、用量写入和设置服务。由此一次请求的身份与平台状态能继续传入选择、重试、结算和日志消费者。`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/internal/server/routes/gateway.go:110`，`Wei-Shaw/sub2api@393a8fe56a0b606d162183cf8014f9381adcbf7e:backend/cmd/server/wire_gen.go:140`

**Inferred。** 其最强的“防存在但未接”机制是静态依赖装配生成物：关键构造参数必须贯穿到应用返回值或清理闭包；若构造签名变化而装配未同步，通常会在编译或生成检查阶段暴露。该保证是从装配形态推断，不等同于已观察到所有 CI 门。

## 三、new-api 的 runtime wiring shape

### 1. 组合根与初始化顺序

**Observed。** 进程先完成环境、日志、数据库、授权策略、数据库设置、日志库、Redis、性能指标和国际化初始化；关键基础设施失败会阻止继续启动，非关键国际化失败则记录后继续。数据库设置初始化明确发生在数据库连接之后。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:296`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:318`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:331`

**Observed。** 通道缓存先初始化并带一次 panic 恢复路径，之后才预热定价；接着启动配置同步、授权策略同步、看板聚合、凭据刷新、订阅额度重置和实例心跳。这一顺序使依赖缓存的定价消费者不会在缓存之前预热。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:79`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:105`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:109`

### 2. HTTP 主入口、alias 和统一中间件

**Observed。** HTTP 引擎在路由挂载前统一接入 panic 恢复、请求 ID、版本、语言、日志和会话；之后由一个顶层路由装配入口挂载管理 API、转发 API 和前端资源。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:174`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:205`

**Observed。** 管理 API 组统一带路由分类、压缩、请求体清理和全局速率限制；匿名入口、用户入口和管理员入口再叠加各自认证与敏感操作限流。这把管理员能力放在显式认证链中，而不是靠处理器自行判断。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:router/api-router.go:14`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:router/api-router.go:67`

**Observed。** 主转发组统一经过系统负载保护、token 认证、模型级限流和通道分配，再按协议格式进入同一转发入口。WebSocket、OpenAI、Claude、Responses、图像、音频、重排和 Gemini 兼容入口共享这条链；任务型协议也显式挂接认证和分配。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:router/relay-router.go:69`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:router/relay-router.go:82`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:router/relay-router.go:168`

### 3. scheduler、worker 和 producer/consumer 接线

**Observed。** 异步任务轮询在调度器启动前先注入协议适配工厂；代码明确要求该接线先于任务运行器，否则任务消费者无法取得实际适配器。之后才注册周期任务并启动运行器。这是本轮最直接的“producer/consumer 启动顺序”防线。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:136`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:147`

**Observed。** 周期任务注册层声明采用数据库租约跨主节点去重并保留运行历史，运行器内部还执行主节点与启用开关判断。该机制针对多实例重复消费，而不仅是“起一个 goroutine”。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:147`

**Observed。** 关闭时 HTTP 服务执行带可配置长超时的优雅退出，并在退出前把内存看板数据刷新到持久层；数据库由延迟清理关闭。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:232`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:244`

### 4. 配置和数据库设置进入运行时

**Observed。** 数据库设置初始化后，运行时持续启动设置同步和授权策略同步；通道缓存也周期同步。该形态把数据库作为配置来源，并用后台同步传播到多实例运行时。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:99`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:main.go:109`

### 5. 请求链中的共同身份、路由、重试与计费状态

**Observed。** 通道分配中间件从认证上下文取得指定通道、模型权限、用户分组和亲和信息；选定通道后写回请求开始时间与通道上下文，下游成功后再记录亲和结果。不同协议的模型解析也在同一分配组件内归一化，减少协议入口绕开通道健康与选择逻辑。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:middleware/distributor.go:32`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:middleware/distributor.go:79`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:middleware/distributor.go:163`

**Observed。** 分配器在亲和通道不可用时可清除当前绑定并回到通用通道选择；选择参数携带请求上下文、模型、分组、路径和重试计数。这让 fallback 不只是换一个通道 ID，而是继续沿用同一次请求的身份和协议路径约束。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:middleware/distributor.go:104`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:middleware/distributor.go:134`

**Observed。** 计费采用请求级结算对象保存预扣状态，并在实际用量产生后按差额补扣或退回；预扣失败被标记为不可重试，防止业务重试把资金不足误当成上游瞬态故障。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/billing.go:20`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/billing.go:51`，`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/billing_session.go:186`

**Inferred。** new-api 的主要防绕链方式是把多协议入口尽量汇入同一认证、限流、通道分配和转发入口，再把计费状态保存在请求级共享对象中；其后台任务则更多依赖显式启动顺序和数据库协调。

## 四、三家共同成熟模式

1. **组合根必须同时拥有“启动”和“停止”。** 三家都不是只在入口创建服务：CLIProxyAPI 的核心服务负责关闭扩展和观察器；sub2api 把大量后台对象保留进统一清理闭包；new-api 至少集中控制 HTTP、数据库和退出前刷新。
2. **路由组是运行时政策边界。** 成熟做法是在路由组挂认证、请求身份、限流、端点规范化、错误记录，再让多个协议处理器复用；alias 不能只复用 handler 而漏掉中间件。
3. **配置必须有明确消费者。** 三家都存在“加载后立即构造消费者”或“更新回调/同步器推送到消费者”的链，而不是仅把设置保存进内存或数据库。
4. **请求状态需要一个共享载体。** 三家分别使用标准上下文或 HTTP 框架上下文，把请求 ID、认证身份、模型、分组、所选账户/通道、重试和计费状态传到日志与结算。
5. **后台工作需要可证明的注册链。** 典型保证包括发布侧自启动、调度器启动前先注入适配器、依赖装配把 worker 保留进清理根、数据库租约防多实例重复执行。
6. **失败分类影响协作。** 资金/权限类错误不应进入上游重试；通道失效可进入 fallback；关键配置加载失败应阻止启动；非关键能力失败可以降级但必须记录。

## 五、主要差异

| 维度 | CLIProxyAPI | sub2api | new-api |
| --- | --- | --- | --- |
| 组合方式 | 长期核心服务 + 多个共享注册中心 | 生成式依赖装配 + 应用返回值 + 统一清理闭包 | 顺序式进程组合根 + 全局服务/模型包 |
| 扩展能力 | 扩展宿主深入启动参数、执行器、模型、认证、用量和管理路由 | 主要是静态服务图，运行对象通过构造参数连接 | 主要依靠顶层初始化和协议适配工厂 |
| 防 producer 无 consumer | 发布时兜底启动用量消费者 | worker 被依赖图构造并保留进清理根 | 明确规定适配工厂先于任务运行器接线 |
| 多实例后台协调 | 本轮证据不足 | 本轮未深读具体租约机制 | 已观察到数据库租约去重与运行历史声明 |
| alias 一致性 | 默认路由后提供统一追加点 | alias 显式复用同一中间件与处理器 | 多协议集中在同一转发路由组 |
| 停止完整性 | 核心服务显式关闭多个运行组件 | 后台对象覆盖面最系统，且基础设施最后关闭 | HTTP 与部分持久化明确，其他 goroutine 的统一停止证据较弱 |

## 六、失败协作模式

- **启动失败要分关键与非关键。** 数据库、核心配置、控制面配置等失败直接阻止服务；国际化、可选扩展状态上报等可记录后继续或按模式退出。
- **业务失败不能污染重试。** 认证、权限、额度不足、请求格式错误应终止；上游瞬态错误、账户不可用、通道失效才进入重试或 fallback。
- **关闭顺序必须反向依赖。** 先停止生产/消费后台工作，再关闭缓存、消息通道和数据库；否则会出现停止期间仍写入已关闭资源。
- **多协议必须有显式一致性证据。** 不能因为 handler 名称相同就认为链一致；应逐个核对 alias、原生协议、WebSocket、异步任务入口是否包含共同身份、认证、限流、路由、计费和日志。
- **运行设置变更必须能到达活消费者。** 仅存在轮询器还不够，还需证明刷新结果替换了选择器、缓存、策略或客户端。

## 七、可转化为 HUAKAI 全局接线真实性审计的问题

### A. Composition root

- 每个核心 service 构造后，是否至少被某个 HTTP handler、worker、scheduler、consumer 或生命周期管理器持有？
- 是否存在“构造成功但返回值被丢弃”的 service？
- 应用根是否同时保留 HTTP 服务和统一 cleanup/shutdown？
- 依赖注入生成物或手工组合根变化后，是否有编译级或测试级门验证所有构造参数都进入消费者？

### B. HTTP 路由

- 每个主入口、无版本 alias、厂商原生入口、管理员入口和 WebSocket 入口是否都真实挂载？
- alias 是否复用同一认证、请求 ID、租户解析、额度、计费、错误记录和审计链？
- 是否存在某协议直接调用 provider，而绕过统一账户健康、路由和结算？
- 管理员写操作是否同时具备管理员认证、强化验证和审计，而不只是 handler 内部判断？
- 是否能从运行中的路由表反查所有应有入口，并与静态能力清单做差异测试？

### C. Worker / scheduler / consumer

- 每个 producer 是否有已启动 consumer？能否通过启动日志、健康指标或队列积压证明？
- worker 是显式启动、构造即启动，还是首次发布兜底启动？是否可能三者都未发生？
- scheduler 启动前所需适配器、handler、租约仓储是否已经注入？
- 多实例下定时任务如何防重复执行？租约失效、进程崩溃后如何恢复？
- shutdown 是否先停 producer，再 drain consumer，最后关闭 Redis/DB？
- worker panic、单任务失败和永久错误分别如何恢复，是否有退避、死信或可操作告警？

### D. 配置与数据库设置

- 每个运行设置是否有明确消费者清单，而不只是 DTO、表字段和管理 API？
- 设置变更后是重建客户端、原子替换快照、失效缓存，还是必须重启？
- 多节点设置传播是否有版本、水位、轮询或事件确认？
- 配置解析失败是否阻止启动，还是悄悄使用零值？
- 运行中热更新是否可能只更新 HTTP 层，而未更新 worker、router 或计费消费者？

### E. 请求共享身份与状态

- 请求 ID、租户、用户、API key、所选 provider、账户、模型 alias、尝试序号和计费事务是否贯穿同一上下文？
- fallback 后是否仍保留原请求身份，同时更新实际账户与尝试序号？
- 用量、审计和错误日志记录的是客户端模型、规范模型还是实际上游模型？三者是否可关联？
- 预扣、实际结算、退款和失败日志是否引用同一个请求/结算身份？
- WebSocket、流式、异步提交与异步查询是否能延续同一业务关联身份？

### F. 防“代码存在但没接”

- 是否有启动期 capability manifest，列出实际挂载路由、已启动 worker、已注册 provider 和已启用消费者？
- 是否有测试从真实 composition root 启动应用，而不是只测孤立 handler？
- 是否有契约测试逐个协议验证统一中间件产生的上下文键确实存在？
- 是否有测试在 producer 发布后等待 consumer 可观察副作用，防止只验证“入队成功”？
- 是否有 shutdown 测试验证队列 drain、计费落盘和后台协程退出？
- 是否有静态或运行时检查发现“配置项有写入入口但无读取消费者”？

## 八、Open Questions

1. 三个本地远端跟踪 SHA 是否仍为 GitHub 当前默认分支 HEAD，需要在有网络且镜像可写的环境重新 fetch 确认。
2. CLIProxyAPI 的所有协议 alias 是否都经过完全相同的认证、用量和扩展链；本轮只确认了服务器扩展点与共享运行服务，未穷举每条路由。
3. CLIProxyAPI 热更新失败后的重试、退避和最后已知良好配置策略，本轮未完成完整调用链阅读。
4. sub2api 各后台服务的启动位置、panic 恢复和多实例协调策略，需要逐个从 provider 构造函数继续追踪；本轮主要确认了构造与停止所有权。
5. sub2api 账户健康变化、重试尝试、结算和审计是否共享同一不可变请求身份，需要继续深读网关执行链。
6. new-api 若干直接启动的长期 goroutine 是否都有统一停止接口；本轮在主关闭路径只观察到 HTTP、数据库与看板刷新，不能断言其余任务均可优雅停止。

## 九、结论

**Observed。** 三家成熟实现都把“真实接线”落实为可追踪链：入口构造长期对象，路由组绑定共同政策，后台消费者有启动顺序或生命周期所有权，配置有运行时传播路径，请求身份与选择/计费/日志状态共享。

**Inferred。** 对 HUAKAI 最有价值的审计模型不是逐文件确认“代码存在”，而是为每项能力建立一条闭环证据：`构造 → 注册/挂载 → 启动 → 请求或事件触达 → 状态传播 → 可观察副作用 → 停止/恢复`。任何缺口都应判为“未证明已进入生产运行”，而不是按实现完成。

本报告共有 22 个实际阅读区域、4 项明确标注的归纳推断、6 个 Open Questions。真实观察集中在三个快照的组合根、路由装配、后台生命周期和请求状态传递；没有把搜索无命中当作能力缺失证据。最大限制是网络和镜像只读导致本轮无法在线确认 HEAD。

Source files read: CLIProxyAPI: `cmd/server/main.go`, `internal/api/server.go`, `sdk/cliproxy/service.go`, `sdk/cliproxy/usage/manager.go`, `internal/watcher/watcher.go`; sub2api: `backend/cmd/server/main.go`, `backend/cmd/server/wire.go`, `backend/cmd/server/wire_gen.go`, `backend/internal/server/router.go`, `backend/internal/server/routes/gateway.go`; new-api: `main.go`, `router/api-router.go`, `router/relay-router.go`, `middleware/distributor.go`, `service/billing.go`, `service/billing_session.go`, `service/channel_select.go`
Lane: specifier
Agent: GPT-5 Codex / root
UTC timestamp: 2026-07-16T09:08:14Z
