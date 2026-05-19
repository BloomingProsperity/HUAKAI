# 2026-05-08 HUAKAI 自有调度算法 — 4 选项 (Claude lane)

| 字段 | 值 |
| ---- | ---- |
| Owner directive | "我们优化的放行也只是在同架构上进行优化，没有跳出框架之外！用我们自己的东西！" + "我是说核心调度算法框架以及逻辑" |
| 决策规模 | **L0 战略** — 决定未来 6 个月所有实现走向 |
| Lane | Claude planner，独立草案，待 codex 平行 + Owner 拍板 |
| 当前 baseline | Track B sticky binding (flat hash → 同 prompt 同账号) + 标准 round-robin/fill-first selector + cooldown |

## 0. 大白话：这 4 个选项在干啥

把"客户发请求 → HUAKAI 选哪个供应商账号去打"这个动作的"算法"换掉。

现在的算法（基本和 sub2api / one-api / new-api 一样）：
- 简单点：轮流来（round-robin）/ 先把第一个账号填满（fill-first）/ 加权随机
- 加 Track B：相同 prompt 尝试给同一个账号
- 加 cooldown：账号刚出错过就先躲一下

Owner 说的"我们自己的东西" = 不再这样选账号，从根上改算法。

下面 4 个就是 4 种"换法"。

---

## 选项 1 — PASR：Cache-Locality 先行

### 一句话
**账号不再是"资源"，是"prompt prefix 的管家"**。 谁第一个 cache 了这段 prefix，谁就是这段 prefix 的管家；下次相同 prefix 必须找回这个管家，不许换。

### 真实场景类比
图书馆阅览座位。每张桌子上摆了不同的书。新读者进来要本《红楼梦》→ 系统直接领他到那张已经摊开《红楼梦》的桌子（"管家"）→ 而不是按"哪桌人少"分配。结果：读者来 100 次都坐同一桌，《红楼梦》就一直在那桌摊开，下次就不用从书架找。

把"《红楼梦》" 换成 "8000-byte system prompt"，把"桌子" 换成 "vendor 账号"，把"摊开"换成"vendor 端 cache_control 已生效"。就是 PASR。

### 关键数据结构
**Affinity Trie（亲和力前缀树）**：
- 节点 = prompt prefix 的前 N bytes 的 hash
- 每个节点记录："steward 账号 ID" + "上次激活时间" + "命中次数"
- 越深的节点 prefix 越长、越具体
- 5 分钟自动老化（对齐 Anthropic 默认 cache TTL）

### 算法
```
schedule(req):
  prefix = compute_prefix_signature(req.system, req.tools, req.messages[0..k])
  for depth in trie.walk(prefix, deepest_first):
    steward = depth.steward
    if steward exists AND steward.health > 0.5:
      return steward            # 回到原管家
  # 找不到：分配新管家
  acc = healthy_accounts.min_by(load)
  trie.insert(prefix, steward=acc)
  return acc

after_response:
  if cache_creation_input_tokens > 0:  confirm_steward(prefix, acc)
  if cache_read_input_tokens > 0:      reinforce(prefix, acc, +decay_resistance)
  if neither:                          weak_steward(prefix, acc)
```

### 收益
- **缓存命中率最大化**：同 prompt 永远找回原账号 → vendor cache 持续累计
- **解决 sub2api scaling 痛点**：客户多时不再随机打散，cache locality 不被破坏
- **Track B/C/P 都成它的子模块**：Track B = trie 的 hash 实现 / Track C = injection 时机就是 trie 路过 / Track P = trie steward 维度的 cache hit metrics

### 代价
- 新增内存开销：trie 节点 ~100 字节 × 万级 prefix = ~MB 级，可控
- 新增复杂度：trie 维护 + 老化 + steward 健康跟踪。~1500-2000 LoC scheduler 包
- 不解决 vendor 出问题：管家挂了得 fallback 到祖先 prefix 的管家或重新分配

### HUAKAI 独有性
**最强**。现有所有项目（sub2api / new-api / litellm / portkey / CPA）都是"账号公平 round-robin"，HUAKAI 改为"prompt prefix 优先"是真分叉。

---

## 选项 2 — MAB：多臂赌机自适应

### 一句话
**账号是赌机的"臂"**，调度器不依赖任何 operator 配置 routing policy，**自己学**哪个臂在什么条件下成功率高。

### 真实场景类比
你去 4 家小卖店买可乐：
- 第一次随机选一家 → 偶尔会选不熟的家试试（explore）
- 老板看你常去那家 → 主要去性价比最高的（exploit）
- 算法自动平衡这两个动作

### 算法（Upper Confidence Bound 一种实现）
```
select_account(req, cohort):
  arms = healthy_accounts(cohort)
  if random_uniform() < epsilon(t):
    return uniform_random(arms)        # explore
  return argmax over arms of:
    arms[a].success_rate +
    sqrt(2 * ln(t) / arms[a].pull_count)  # UCB upper bound
  # arm 越少被试，UCB 加分越大，鼓励试

after_response:
  if outcome.ok:        arm.success++
  else:                 arm.failure++
  decay_observations(half_life=7d)     # 7 天前的观测半衰
```

### 收益
- **零 operator 调参**：扔进生产，自动收敛
- **抗坏 vendor 周期**：vendor A 偷偷限流，accuracy 一掉，UCB 自动转 B
- **可量化 - regret bound**：理论保证收敛速度

### 代价
- 收敛要时间：冷启动要 100+ 次试错
- 不直接优化 cache locality（success rate 是粗粒度信号）
- 需 per-cohort 维护状态：(tenant, model_alias) cohort × N accounts → 状态空间大

### HUAKAI 独有性
**中等**。Bandit 思路在 ML serving 领域有先例（OpenAI 内部、Anyscale 等），但 AI gateway 类项目没人这么用。HUAKAI 引入是新的，不算革命。

---

## 选项 3 — Speculative：并行推演 + 最快赢

### 一句话
**不选账号，全选**。同一个请求扔给 top-3 账号并发跑，谁先回来用谁的，其它 cancel。

### 真实场景类比
重要快递急用 → 同时叫 3 家快递公司上门取件 → 谁第一个到家就给谁的取件单 → 其它 2 家空跑（白做工不收钱）。

### 算法
```
schedule(req):
  candidates = top_3_by_recent_success(pool)
  futures = parallel_send(candidates, req)
  winner = first_completed(futures, timeout=200ms)
  cancel(others)            # 用 ctx cancel + connection close
  return winner.response

# Cost: 3x token 调用费
# Gain: 单 vendor outage / 30% slow-tail 的 P95 直接抹平
```

### 收益
- **极抗 outage**：3 家同时挂的概率 < 单家挂的立方
- **P95 latency 直接向 P50 收敛**
- **零调参**

### 代价
- **3x token 成本**（重磅）：万人级流量直接乘 3
- 不解决 cache locality：3 家都各自冷打 → 每家都重新建 cache，反向破坏 cache hit
- vendor 反作弊检测可能联动（短时间 3 个账号 IP 命中同 prompt 像 abuse）

### HUAKAI 独有性
**高（理论）但低（实施成本）**。Hedged request 思路在 Google search infra 经典论文有，但 AI gateway 类项目没人做（成本太高）。HUAKAI 做了就独家，但商业化层 Owner 要算账。

---

## 选项 4 — 三者融合（PASR 主 / MAB 抓手 / Spec 兜底）

### 一句话
**三层夹心**：表面是 PASR（cache locality），同 prefix 多账号怎么分由 MAB 学，所有都打不通时 Spec 抢救。

### 算法
```
schedule(req):
  prefix = compute_prefix_signature(req)
  cohort = trie.candidates_at_prefix(prefix)   # 该 prefix 已注册的所有 steward + 兄弟
  
  if cohort.size > 0:
    # 第二层：在 cohort 内部用 MAB
    arm = mab_select(cohort, req)
    if arm.health > 0.3:
      return arm
  
  # 第三层兜底：高优先级 / 之前都失败 → Spec
  if req.priority == HIGH or len(failed_history) >= 2:
    return speculative_send(top_3_candidates, req)
  
  # 兜底兜底：随机健康账号
  return random(healthy_accounts)
```

### 收益
- **完整覆盖三种场景**：cache-friendly common path + 自适应 vendor 漂移 + 灾难兜底
- **成本可控**：Spec 只在异常 path 触发，常态 0 浪费
- **三层各自可独立测试**

### 代价
- **复杂度最高**：~3000-4000 LoC scheduler
- 三层之间状态协调的 bug surface 大
- 调试困难（log 要标清楚走的哪一层）

### HUAKAI 独有性
**最强**：没有任何参考项目是这种夹心结构。

---

## Claude 推荐

**选 1 (PASR) 单 axis 起步，跑 3 个月有数据后再考虑加 MAB/Spec 子模块成为选项 4 形态**。

理由：
1. **PASR 是 HUAKAI 现有 Track A-P 的自然延伸**：Track B 已经做 hash → account，PASR 就是把它升级成 trie + 健康 + 老化。重新利用率最高。
2. **PASR 能直接证明给 Owner 看效益**：cache hit ratio 从 0% → 80%+，运维 dashboard 一眼可见。MAB 要"100 次试错收敛"，没有这种立竿见影信号。
3. **PASR 的实施风险最低**：trie + radix 操作是教科书算法，单元测试容易写。MAB 牵涉随机性 + 半衰 + cohort 状态空间，flaky test 高。
4. **选项 4 是终态，但当前一步到位风险大**：直接上三层夹心 = 一次性引入 3 个新概念 + 3 套故障模式。先跑 PASR 1-2 月，data 来了知道哪些 prefix 真的稳定有 steward → 再决定 MAB 在 cohort 内部值不值得 → 最后判断 Spec 兜底要不要。
5. **Owner 之前 push 的"稳定 = 比 sub2api 强"** ：sub2api 弱在大流量下 cache 不命中（memory 已记录 7 个 scaling bottleneck）。PASR 直接打这个痛点，MAB / Spec 不是。

如果 Owner 想"一步到位 + 不怕复杂度"：选项 4。
如果 Owner 想"激进省钱"：选项 3。
如果 Owner 想"完全自学习"：选项 2。
如果 Owner 想"务实 + 缓存最大化"：**选项 1（推荐）**。

## 建议流程

无论 Owner 选哪个，下一步：
1. 派 codex 独立写 codex-lane 同名 plan（不看本 claude lane）
2. 双 lane 对比 → synthesis
3. 实现 atomic 拆分（PASR：trie 数据结构 → 集成 selector → 老化策略 → 健康跟踪 → metrics → cutover）

如 Owner 选 PASR，原子粒度建议：
- A1: trie 数据结构 + insert/walk/age (~300 LoC)
- A2: PromptPrefixSignature 计算（已有 Track B prompt_hash，复用）
- A3: PASR Selector 接口实现（~400 LoC）
- A4: 老化 worker（goroutine + ticker）
- A5: 健康跟踪 (continuous score)
- A6: 集成 selector.go cutover + feature flag (default=false 先共存)
- A7: cache-creation 反馈循环
- A8: per-account-per-prefix metrics 透出 /debug/vars

每 atom < 500 LoC，~2 天可以拿到能跑的 PASR feature flag。Owner 拍板后立即开 A1 + 派 codex 平行设计。

---

**当前等 Owner 拍板：选 1 / 2 / 3 / 4 的哪一个。**
