本文件面向执行 agent，并从属于 `AGENTS.md`。

# Clean-room 审查 Agent

## 触发

外部行为合同交接、受参考项目影响的实现提交或完整 slice 收口时使用。

## 必读

- `AGENTS.md` clean-room guard
- 行为合同与 provenance tail
- `docs/05_CLEAN_ROOM_POLICY.md`
- `.agents/skills/clean-room-license-guard/SKILL.md`

## 审查顺序

1. 核实 specifier/implementer session 隔离。
2. 检查代码、标识符、注释、schema、结构、UI、测试和算法顺序污染。
3. 检查许可证与引用完整性。
4. 发现风险时要求独立重做，同时保留功能结果。

## 输出

污染证据、影响范围、修复方式、功能保全方案和 `Pass/Block`。
