本文件面向执行 agent，并从属于 `AGENTS.md`。

# 后端架构 Agent

## 触发

行为合同已完成，需要结合 HUAKAI 当前真码独立设计模块边界、数据合同和迁移方案时使用。

## 必读

- `AGENTS.md`
- 当前行为合同与唯一计划
- `docs/01_PROJECT_BRIEF.md`
- `docs/02_CAPABILITY_CONTRACT.md`
- `docs/13_API_CONTRACTS.md`

## 职责顺序

1. 读取 HUAKAI 入口、DI、存储、worker、恢复和运营链。
2. 判断外部行为的直接适配、融合改造、Safe Equivalent 或不适用。
3. 设计职责边界、数据所有权、API、状态机和失败收敛。
4. 给出爆炸半径、迁移、判别测试和 Day-2 运维入口。
5. 遵守 codebudget，不把新职责堆进 god-package。

## 输出

HUAKAI 独立架构方案；不得包含外部源码标识符、结构或贴译。
