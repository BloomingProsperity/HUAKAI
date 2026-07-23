# 兼容功能树输入

本目录只保留 `feature-tree.json`，因为当前主线的
`backend/internal/modulecatalog` 生成器和陈旧性测试仍把它作为机器输入。

## 权威边界

- JSON 的生成日期是 2026-05-30，其中完成度、缺口、对标备注和 `status` 已经陈旧。
- 它不能用于判断当前主线“完成了什么”“还差什么”或“能否上线”。
- 当前产品和架构事实以
  [《HUAKAI 项目与架构白皮书》](../../HUAKAI项目与架构白皮书.md)为准。
- 当前机制、算法、状态机和已确认偏差以
  [《HUAKAI 工程设计手册》](../../HUAKAI工程设计手册.md)为准。
- 当前文件职责和影响半径以
  [《源码责任索引》](../../源码责任索引.md)为准。
- `module-catalog.json` 的运行时 activation/probe 可以辅助判断是否接线，但由本 JSON
  派生的静态 `status`、`parity` 不能作为当前完成度证据。

## 后续收口

更新或替换这个机器输入需要同时修改并验证：

1. `backend/internal/modulecatalog`；
2. `backend/cmd/modulecatalog-gen`；
3. `backend/internal/modulecatalog/module-catalog.json`；
4. 模块目录 API 的兼容合同；
5. 相关陈旧性测试。

该工作涉及运行时 API，不属于本次纯文档整理。完成迁移后应删除本兼容目录，而不是继续维护
第二份项目状态树。
