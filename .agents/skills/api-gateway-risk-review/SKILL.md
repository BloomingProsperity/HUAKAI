---
name: api-gateway-risk-review
description: 从入口到恢复审查 gateway、账号、路由、协议、配额、计费、凭据、可靠性、安全和可观测的生产风险与跨模块联动。
---

# API Gateway 风险审查

## 何时使用

- 设计或修改 gateway、pool、provider、quota、billing、auth、credential、protocol 或 worker；
- 跨模块集成和完整 slice 收口；
- 线上故障、错账、串租户、重复副作用或恢复缺口。

## 前置输入

- 行为合同；
- HUAKAI 真实入口/DI/状态链；
- 风险 register、bug patterns 和 acceptance matrix。

## 执行步骤

1. 画清 actor 和完整链：身份、选号、gate、凭据、出站、retry/fallback、health、billing/quota、audit、DLQ/recovery。
2. 核对模块交界传递的 tenant/user/key/account/attempt/hold/claim/idempotency 状态。
3. 检查 4xx/5xx、流式中断、超时、取消、DB/Redis 故障、部分成功和多副本竞争。
4. 检查钱、额度、并发槽、凭据、账号健康和审计是否原子或可恢复。
5. 检查 secret redaction、SSRF、跨租户、权限和 abuse cases。
6. 检查 operator 是否能查询、筛选、诊断、重试、对账和人工解决。
7. 为每个风险给具体修法与判别测试，不只给通用建议。

## 输出

- 按 S0-S3 排序的风险；
- `file:line` 证据、可达路径、影响半径；
- 修复、测试和运维恢复方向；
- release 影响。

## 阻断项

money/auth/tenant/secret/data-loss 的可达问题、跨模块副作用不收敛或恢复只写日志，阻止落地。
