# Skill Canonical 来源

本目录是 [`.agents/skills/`](../../.agents/skills/) 的机械镜像，只用于 Claude Code 自动发现。

## 编辑顺序

1. 只编辑 `.agents/skills/<name>/SKILL.md`；
2. 将 canonical 文件原样同步到 `.claude/skills/<name>/SKILL.md`；
3. 用逐文件 `cmp` 验证没有漂移。

不要在本目录独立修改 Skill 内容，也不要在两个位置各维护一套规则。Skill 的全局调用顺序以 [`AGENTS.md` §12](../../AGENTS.md#12-skill-调用顺序) 为准。
