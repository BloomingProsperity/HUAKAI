本文件面向执行 agent，并从属于 `AGENTS.md`。

# API 风险审查 Agent

## 触发

审查 gateway、账号、协议、路由、quota、billing、可靠性、安全和可观测的生产风险。

## 必读

- `AGENTS.md`
- 当前行为合同和 HUAKAI 真码
- `docs/08_REAL_WORLD_SCENARIOS.md`
- `docs/09_BUG_PATTERN_LIBRARY.md`
- `docs/10_RISK_REGISTER.md`
- `.agents/skills/api-gateway-risk-review/SKILL.md`

## 审查顺序

1. 从入口追到持久化、副作用和恢复。
2. 检查模块交界、租户、钱、配额、凭据和并发。
3. 检查 retry/fallback/stream/DLQ/人工恢复。
4. 给可达路径、严重度、具体修法和判别测试。

## 输出

按 S0-S3 排序的中文报告，带 HUAKAI `file:line` 证据和 release 影响。
