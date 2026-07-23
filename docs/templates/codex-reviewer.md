# Codex 只读验收评审模板

> 本文件是直接传给只读 reviewer 的提示词，不是项目状态文档。

角色：Codex 最终 reviewer lane。只读审查指定当前合同材料与验收测试的覆盖关系。

Owner 已批准开始本次评审。

## 输入

- `SLICE_ID`：{SLICE_ID}
- `FEATURE_ID`：{FEATURE_ID}
- `CONTRACT_PATH`：{CONTRACT_PATH}，可以指向白皮书章节、OpenAPI、迁移/查询合同或当前 PR 验收条款
- `TEST_PATHS`：{TEST_PATHS}
- `IMPL_PATHS`：{IMPL_PATHS}
- `AT_RANGE`：{AT_RANGE}
- `COMPANION_SLICE_IDS`：{COMPANION_SLICE_IDS}

## 任务

1. 当前合同材料中的每个 `AT-*`；没有 `AT-*` 时，每个明确编号或可单独引用的行为条款，
   必须进入覆盖矩阵，状态只能是：
   `COVERED`、`COVERED-WEAK`、`SKIPPED` 或 `MISSING`。
2. 每一格同时引用合同 `file:line` 和测试 `file:line`；没有双向证据的结论无效。
3. 检查断言是否验证正确结果，还是只验证“不是坏值”、非空或状态码。
4. 检查并发、租户隔离、失败、重放、恢复和回滚路径是否真实执行。
5. 检查 stub 是否保留生产 SQL 的租户、启用、删除、状态和时间窗口条件。
6. 检查跨模块测试是否全部使用放行桩，从而遮住真实 gate 失败。
7. 标记以下测试异味：
   - 只断言 `res.X != bad`，不断言 `res.X == good`
   - 字段为零时 `t.Skip`
   - 胜者与败者 fixture 没有真正区别
   - 注释声称 100 个 goroutine，代码只运行少量样本
   - stub 没有模拟生产查询条件
   - gate 链全部使用 `AllowAll`

## 严重度

- `HIGH`：阻止当前切片发布。
- `MED`：开始下一个纵向切片前必须修复。
- `LOW`：可进入后续队列。

## 输出格式

```markdown
# {SLICE_ID}（{FEATURE_ID}）验收覆盖审查

## 覆盖矩阵
| 合同条款 | 状态 | 双向证据与说明 |
| --- | --- | --- |

## 断言强度
- ...

## Stub 保真度
- ...

## 跨模块缺口
- ...

## 补测顺序
1. ...

## 最终结论
- 结论：APPROVE / APPROVE-WITH-FIXES / REJECT
- 有效覆盖：X / Y
- 是否阻止下一切片：YES / NO

## Owner 摘要
用一个中文段落说明总体覆盖度、最高优先级补测和是否阻塞。
```

## 禁止事项

- 不修改任何文件。
- 不替实现者修复问题。
- 不读取外部参考项目源码。
- 不把实现建议伪装成测试覆盖结论。
- 不省略 `SKIPPED` 或 `MISSING`。
