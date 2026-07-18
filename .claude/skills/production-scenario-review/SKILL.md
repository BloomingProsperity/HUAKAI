---
name: production-scenario-review
description: 验证功能、API、UI 或发布在真实流量、故障、滥用、多副本和人工恢复压力下是否仍可运行和好运维。
---

# 生产场景审查

## 何时使用

- 计划从 happy path 进入实现；
- 跨模块能力或运营 UI 收口；
- 发布前验证 Day-2 可运维性。

## 前置输入

- 行为合同和 HUAKAI 运行链；
- 已知事故/issue；
- 风险 register 和 acceptance matrix。

## 执行步骤

1. 枚举正常、边界、上游不可用、账号失效、限流、余额不足、DB/Redis 故障和网络中断。
2. 加入并发打满、重复回调、跨节点重放、worker 重启和 leader 切换。
3. 检查 partial success 后钱、状态、审计和 UI 是否一致。
4. 检查危险操作的权限、确认、原因和审计。
5. 检查 operator 能否无数据库手术完成查询、重试、隔离、对账和人工补偿。
6. 将缺失场景转成 bug pattern 与 acceptance test。

## 输出

- 生产场景矩阵；
- 缺失恢复、可观测、权限和测试；
- 风险与 release blocker。

## 阻断项

仅能 happy path 演示、失败后必须手改数据库、或 operator 看不到真实状态的能力不能宣称完成。
