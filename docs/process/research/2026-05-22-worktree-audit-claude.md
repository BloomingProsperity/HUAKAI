# 2026-05-22 HUAKAI 工作树结构审计 — Claude 独立稿

> Owner 2026-05-22 指示「你和 codex 先审计下我的工作树的优缺点」。
> 平行审计:Claude 本稿 + codex `-codex` 稿,各自独立后交叉。
> 范围:仓库目录/模块**结构**优缺点,不是代码缺陷(那是 56 条深度审计)。

## 总判定

**工作树架构根本上是健康的 —— 保留 + 增量整理,不需要结构性重做。**
核心(backend/ 标准 Go 布局、exploratory 隔离、internal 包颗粒度)健康;
所有缺点都是**杂物与组织卫生**,不是架构腐烂。

## 优点

1. **backend/ 标准 Go 布局** —— `cmd/ internal/ pkg/ sql/` + `go.mod` +
   `sqlc.yaml` + `Makefile` + `Dockerfile`,规范、可预测。
2. **exploratory/ 真隔离** —— grep 确认生产 `backend/` 对 `exploratory/` **零
   import**。Rust 实验无法污染生产链路。边界干净。
3. **internal 包颗粒度合理** —— 41 个包,多数单一职责,对这个体量的网关合适。
4. **docs/ 有编号主轴** —— `00_`–`24_` 的 PM 规范文档成体系。
5. **构建产物正确 gitignore** —— `cargo-targets/ huakai-rust-target/ tmp/`
   均在 `.gitignore`,tracked=0,非提交垃圾。
6. **集成测试正确隔离** —— `integration_pg` build tag 分离单元/集成。

## 缺点

| # | 严重度 | 缺点 | 证据 | 修向 |
|---|---|---|---|---|
| W-1 | MED | 顶层杂乱 | `reference_deep_dive/`(41 tracked 文件,参照资料堆在仓库根)、`ROUND_7_REPORT.md` 顶层 + `backend/ROUND_8_REPORT.md` 散落、`tools/`+`scripts/`+`backend/scripts/` 三处脚本 | 参照资料移 `docs/` 或移除;round 报告归 `docs/process/reviews/` |
| W-2 | MED | docs/ 目录结构重复 | `docs/plans/` vs `docs/process/plans/`、`docs/research/` vs `docs/process/research/` —— plans 与 research 各自存在两处 | 收敛到单一位置,新文档去向唯一 |
| W-3 | MED | plans 目录爆量 | `docs/process/plans/` **334 个文件** | 按月/季归档,保留近期 |
| W-4 | MED | internal 疑似重复职责包 | `obs` vs `observability`(两个可观测包)、`cache`/`cache_routing`/`cachemetrics`(三个缓存包) | 查 obs/observability 是否应合并 |
| W-5 | LOW | 命名不一致 | `cache_routing`(snake_case)vs `cachemetrics`/`gatewayhttp`/`auditledger`(连写);API 前缀 `/admin/v1` vs `/v1/admin` | 统一 Go 连写包名;API 前缀收敛(已入 backlog) |
| W-6 | LOW | docs 编号撞号 | 两个 `01_*`、两个 `02_*`;缺 `23_` | 编号唯一化或接受为非键 |
| W-7 | LOW | 6 份 README 翻译 | `README` + CN/ES/JA/KO/VI;`docs_zh/` 仅 13 文件部分翻译(docs/ 约 28 编号文档) | 预发布期翻译必漂移,建议收窄或显式标 stale |
| W-8 | LOW | cmd 状态不明 | `cmd/_smoke-codex-tls` 前导下划线(禁用/隐藏?) | 明确启用或删除 |

## 关键观察

- exploratory/ 1693 文件里 99.9% 是 `rust-core-gateway`;`python-offline-analytics`
  只有 1 文件(空壳)。"exploratory" 实质=Rust 网关。它隔离得好,但体量已接近
  正式模块 —— 是否升格/改名是战略问题(与 `project_two_data_planes` 关联),
  不是结构 bug。
- 缺点全是**卫生问题**,逐条都 ≤ 1 天可整理;没有一条需要动 backend 架构。

## 建议

不立即做大整理(会打断 12 波主线)。把 W-1/W-2/W-3 收进 §状态树的一个
"仓库卫生" section,与 W-5 的 API namespace 收敛一起做;W-4 obs/observability
合并评估随 W3(errors 波,本就动 observability)顺手查。

---
Lane:Claude 独立审计(HUAKAI 内部仓库,无 clean-room)。
UTC:2026-05-22
