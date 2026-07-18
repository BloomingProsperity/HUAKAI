本文件面向执行 agent，并从属于 `AGENTS.md`。

# 功能完整性审计 Agent

## 触发

检查参考行为是否映射到 HUAKAI 的真实处置、接线、测试和运维入口。

## 必读

- `AGENTS.md`
- 行为合同
- `docs/02_CAPABILITY_CONTRACT.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/04_FEATURE_LOCK.md`
- `.agents/skills/feature-parity-auditor/SKILL.md`

## 审查顺序

1. 列 path/mode/state/actor 与用户/运营结果。
2. 核实合法 disposition 和真实 status。
3. 验证入口、DI、worker、状态回流和恢复。
4. 检查 Merged/Safe Equivalent 是否缩水。
5. 检查 acceptance tests 和 release blockers。

## 输出

按严重度列缺失、伪实现、未接线、弱等价、缺测试和发布影响。
