---
name: dependency-license-auditor
description: 在选择外部领域项目、增加依赖、vendoring 或发布前，核实许可证、传递依赖、维护状态和 MIT 兼容边界。
---

# 依赖与许可证审计

## 何时使用

- 选择中转站或专业领域参考项目之前；
- 新增/升级 runtime dependency；
- 计划复用官方 SDK、vendoring 或隔离插件；
- 发布前复核依赖风险。

## 前置输入

- 候选仓库 URL、当前 HEAD 和用途；
- 现有依赖清单与 lockfile；
- `AGENTS.md` §5 clean-room/参考源规则；
- `docs/dependency-policy.md` 与当前 Issue/PR 的风险记录。

## 执行步骤

1. 核实仓库是否 archived/disabled、最近维护时间和默认分支 HEAD。
2. 读取根许可证、NOTICE、子目录许可证和依赖清单，不能只信 GitHub badge。
3. 识别 direct/transitive 的 MIT、Apache-2.0、BSD、GPL、AGPL、LGPL、SSPL、BUSL、未知或自定义条款。
4. 判定用途是“行为证据、独立实现、官方 SDK、隔离插件或 vendoring”。
5. AGPL/GPL/LGPL 默认只允许 clean-room 行为证据；不能因许可证风险删除能力。
6. 对允许复用的官方 SDK/隔离 vendoring，列 LICENSE/NOTICE、来源 SHA、升级和漏洞跟踪要求。
7. 把真实风险写入当前 Issue/PR；长期依赖规则只更新 `docs/dependency-policy.md`，不新建重复文档。

## 输出

- 候选项目与依赖的许可证/活跃度表；
- `行为证据 / 独立实现 / 可隔离复用 / 禁止复用` 结论；
- 风险、替代项和后续审计门。

## 阻断项

许可证未知、仓库来源不明、NOTICE 缺失或 copyleft 边界无法确认时，阻止代码复用；仍可在完整 clean-room guard 下研究用户可观察行为。
