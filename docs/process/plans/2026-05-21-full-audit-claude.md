# 2026-05-21 HUAKAI 全面自查计划（Claude 独立草案）

> 本文件按 CLAUDE.md #10 平行交叉规则,由 Claude 独立起草,未参考 codex 草案。配对文件:`2026-05-21-full-audit-codex.md`。

## 1. 缘起与目标

Owner 给出 16-section「HUAKAI 项目功能树」并问「是这样的状态树吗」。Owner 先前指令:洞③ 落地后做「全面自查 —— 差的就是和借鉴项目功能缺失模块」。

**目标**:以 Owner 的 16-section 树为骨架,逐叶子节点回答两个问题:
1. **真实状态** —— HUAKAI 这个节点现在是什么状态(不靠印象,靠代码证据)。
2. **parity gap** —— sub2api / CLIProxyAPI / new-api 有、HUAKAI 缺或弱的。

## 2. 成功标准

- 产出一棵「带状态标注的功能树」,每个叶子节点带:状态标 + 证据(`file:line` 或「未找到」)+ parity 备注。
- 产出「功能缺失/弱项总表」:维度 | HUAKAI 现状 | sub2api | CLIProxyAPI | new-api | gap 严重度 | 补救估时。
- 每条引用借鉴项目的论断带 `<repo>@<sha>:<file>:<line>`(CLAUDE.md #12)。
- 不靠训练记忆;每个状态判断有 HUAKAI 代码证据或明确「未找到」。

## 3. 状态分类法

| 标 | 含义 |
|---|---|
| ✅ 完整 | 有实现 + 接线 + 测试,生产可用 |
| 🟡 部分 | 有实现但有缺口(未接线 / 缺测试 / 仅 happy path) |
| 📋 仅 spec | 只有 spec 文档,impl 为 0 |
| ❌ 缺失 | 树列了、项目该有、但没有 |
| ⚠️ 名实不符 | 树这样写,但项目实际不是这个形态(需修正树) |

## 4. 已知的两处树-现状偏差（开工前先记)

- **§13 Juice** —— 树写的是旧「降智检测」框架(Benchmark Prompt / 能力探针 / 输出评分 / 降智趋势)。Owner 本人已把 juice 改为「透明版」(用户端显示 HUAKAI 自己有没有换/映射模型)。审计按**透明版**口径,标注树的 §13 标签为 ⚠️ 待修正。
- **§6 Rust 高性能网关** —— 树把它与 §5 Go 网关核心并列,易读成两个生产网关。已核实现状([[project_two_data_planes]]):Go gatewayhttp 是唯一生产数据面;Rust core_gateway 控制面未接通、功能薄。审计按「真实代码但未上线」口径。

## 5. 方法

每个 section:
1. **HUAKAI 状态** —— grep / Explore 定位代码,读关键文件确认状态标 + 证据。无 clean-room 问题(HUAKAI 自有代码)。
2. **parity** —— specifier lane 读 `~/refs/sub2api-latest`(`Wei-Shaw/sub2api@16793d3a`)、`~/refs/CLIProxyAPI-latest`(`router-for-me/CLIProxyAPI@21fad9db`)、new-api(若已 clone)的对应模块,行为级转述,标 gap。
3. 不写 HUAKAI 代码;不做 git;只产报告。

## 6. 切分（16 section → 5 lane,codex 并行 ≤ 3）

| Lane | sections | 主题 |
|---|---|---|
| A | §1 用户权限 / §3 账号凭证 / §4 账号池 | 身份与账号 |
| B | §2 模型接入 / §5 网关核心 / §7 路由调度 | 网关与路由 |
| C | §8 用量计费 / §9 审计信任链 | 钱与信任 |
| D | §10 可观测运维 / §11 安全隐私 / §12 反封禁 | 运维与安全 |
| E | §6 Rust 网关 / §13 Juice / §14 社区增长 / §15 前端 / §16 文档测试 | Rust/juice/增长/前端/文档 |

每 lane 一份子报告 `docs/process/research/2026-05-21-audit-<lane>.md`。Claude 汇总成总树 + 总表。

## 7. clean-room

读借鉴项目源码 = specifier lane,每个 lane 的 codex prompt 头部贴 AGENTS.md 规范 CLEAN-ROOM LANE GUARD 块。sub2api 非 MIT 只 paraphrase;CLIProxyAPI / new-api MIT。

## 8. 估时 / blast radius / 决策点

- 估时:5 lane,3 并行,每 lane ~30-50 min,总 ~2-3 轮。
- blast radius:低 —— 纯只读审计 + 报告,不改代码不改 schema。
- 决策点:审计若发现高危正确性 bug(如已发现的 tool-id / cache-TTL-billing),单独立项,不在审计内修。审计只产清单,修复另排。

## 9. 与 codex 草案的交叉

按 #10,codex 独立起草 `2026-05-21-full-audit-codex.md` + 独立评估这棵树(`feedback_owner_input_codex_eval`)。Claude 汇总两份草案的 agree/conflict/gap,surface Owner 后再开 lane。
