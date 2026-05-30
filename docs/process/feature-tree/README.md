# HUAKAI 功能树 · 推进 + 修复 状态地图

可视化仪表盘:一屏看清**整个项目建到哪（推进进度）+ 问题修到哪（修复轴）**,细到模块级,覆盖 ≥ 借鉴项目(sub2api / CLIProxyAPI / new-api)。

- **`feature-tree.html`** — 视图(交互式仪表盘 + 可折叠树 + 筛选)。纯 vanilla JS+CSS,零外部依赖。
- **`feature-tree.json`** — 数据(唯一要改的状态源)。
- 取代 `docs/process/research/2026-05-21-full-audit-tree.md`(codex 旧树,保留为历史基线)。

## 怎么看（要走 http,不能 file:// 双击)

浏览器在 `file://` 下禁止 `fetch` 本地 JSON,所以**必须用 http 预览**(两种都自动重载):

1. **VSCode**:装 **Live Preview** 扩展 → 右键 `feature-tree.html` → *Open with Live Preview*(改 JSON 存盘后自动刷新)。
2. **终端**:
   ```bash
   cd docs/process/feature-tree
   python3 -m http.server 8000
   # 浏览器开 http://localhost:8000/feature-tree.html
   ```

## 怎么改状态（只改 JSON)

改 `feature-tree.json` 里某节点的 `pct` / `stage` / `fix` / `verified` → 存盘 → 刷新页面即更新。**视图/数据分离**,不用动 HTML。各板块百分比、顶部加权、修复计数都是页面**实时算**的。

## 三标含义（每个叶子)

| 标 | 含义 |
|---|---|
| **阶段** `stage` | `📋spec` 仅文档 → `🟡partial` 有实现有缺口 → `🔵wired` 已接线 → `✅tested` 测试/生产;另有 `⚠️deadcode`(零调用/名实不符) `❌missing` `⛔nogo`(真码但未上线) |
| **完工 %** `pct` | 该能力建设完成度 |
| **修复** `fix` | 关联审计 finding:`✅landed` 已落地 / `⏸deferred` 延后(多为钱路需 Owner gate) / `○open` 待修 |
| **✓ verified** | Claude 已对**真实代码**深核(多为修复战役亲历);无 ✓ = 按 codex 2026-05-21 审计 + 文档综合(声称) |

顶部仪表盘:左=建设加权完工度(9 轴,来自 `02_HUAKAI_FUSION_ARCHITECTURE.md`);右上=9 轴明细;右下=195 条审计修复战役计数。

## 数据来源

codex `2026-05-21-full-audit-tree.md` + `audit-A~E`(16 板块骨架/状态/三参照对照) · `02`(加权 9 轴) · `03_FEATURE_PARITY_MATRIX`(F-* ID) · `/home/ubuntu/audit/MASTER-verification-2026-05-29.md`(195 finding 修复轴) · 修复战役亲历(verified✓ 节点)。

## 注意（活文档)

- 这是**活文档**:随开发/修复推进,改 JSON 保持新鲜。
- `pct` 含主观综合成分;`verified✓` 节点最可信,其余为声称——逐簇深核后补 ✓。
- 参考缺口(rerank / 后端 i18n / Realtime / 充值订单 等)**显式列在页面底部**,不隐藏,确保"覆盖 ≥ 借鉴项目"可被检验。
