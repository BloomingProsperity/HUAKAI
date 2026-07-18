---
name: frontend-ops-ui-review
description: 设计或审查账号、路由、健康、配额、计费、退款、日志、审计、插件和开关的集中运营 UI，确保直观且不丢后端能力。
---

# 前端运营 UI 审查

## 何时使用

- 设计或重构管理运营界面；
- 多个场景需要融合在一个页面/容器；
- 后端能力已存在但运营入口、状态或恢复动作缺失。

## 前置输入

- 当前可信行为合同和 API/OpenAPI；
- actor/权限矩阵；
- 状态机、错误分类、恢复动作和审计合同；
- Owner 明确的页面融合要求。

## 执行步骤

1. 从 operator 高频任务和资源关系组织页面，不照搬旧前端或参考项目 UI。
2. 能融合的场景用 tabs、filters、drawer/detail 和上下文动作融合，避免拆成大量页面；不能因融合隐藏能力。
3. 为资源提供搜索、筛选、排序、分页、详情和批量操作。
4. 展示 owner、状态、最后活动、限制、健康、累计值、pending、失败原因和恢复进度。
5. 危险动作必须权限、确认、原因和审计；秘密默认脱敏。
6. operator 可从请求追到用户、key、route、account、usage、billing、refund 和 audit。
7. 覆盖 loading/empty/error/stale/conflict/partial success/permission denied 状态。

## 输出

- 页面信息架构和组件/状态规范；
- API 缺口、运营动作和验收场景；
- UI、权限、可观测和 parity 缺口。

## 阻断项

旧前端已被 Owner 判定不可信时不得作为设计依据；不能复制外部独特布局、样式、文案或组件源码。
