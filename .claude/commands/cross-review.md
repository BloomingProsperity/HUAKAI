---
description: 对指定行为合同和验收测试运行 Codex 只读覆盖审查
allowed-tools: Bash, Read, Glob, Grep
---

# /cross-review

参数格式：

`/cross-review <目标标识> <功能标识> <当前合同材料路径>`

参数来自 `$ARGUMENTS`：$ARGUMENTS

## 强制流程

1. 从参数提取目标标识、功能标识和当前合同材料路径。材料可以是白皮书对应章节、
   OpenAPI、迁移/查询合同或当前 PR 的验收条款，不得使用已经退役的历史规格状态。
2. 定位对应的 `*_test.go` 与生产实现文件。
3. 完整读取 `docs/templates/codex-reviewer.md`。
4. 替换模板中的输入占位符。
5. 在仓库根目录运行只读 reviewer：

   ```bash
   codex exec --full-auto --sandbox read-only -C "$REPO" -
   ```

6. 等待 reviewer 完成，不轮询、不允许它写文件。
7. 直接向 Owner 返回最终报告，不在仓库新建 review Markdown。
8. 将阻断问题修复证据留在当前 PR；后续项进入当前唯一计划或 GitHub Issue。

## 硬规则

- 缺少模板、当前合同材料或测试路径时必须失败关闭。
- 不得因为测试命令通过就自行宣布覆盖完成。
- `REJECT` 阻止当前切片完成。
- 每项结论必须同时引用当前合同材料和测试 `file:line`。
- reviewer 不读取外部参考项目，不修改任何文件。
