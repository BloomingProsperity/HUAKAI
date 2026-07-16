# UI 质感整改波(Claude 独立稿,#10 与 codex 稿交叉)

Owner 批评:页面糙——大片空白、空态只有灰字、信息密度低、组件孤零。本波只修**质感**,
不动 Owner 已拍板的 IA(布局/导航/路由/设计 token 不变),不加新依赖。

## 现状盘点(真码)

- 公共组件层极薄:`frontend/src/ui/` 只有 ErrorFallback/StatusBadge——没有 EmptyState、
  StatCard、Skeleton、SectionCard 等基础件;各页各写内联样式,密度和层级全靠手感,这是
  "糙"的结构性根因。
- 空态全是死灰字:概览页 `OverviewPage.tsx:87`「当前账户未配置配额限制(无上限)」、
  `:209`「暂无可统计的用量」——没有下一步行动引导,占着一整张大白卡。
- 卡片纵向留白大、单卡信息一两行,一屏利用率低(Owner 截图即此页)。

## 方案(三层推进)

**P0 公共组件底座(先建,后续页全复用)** `frontend/src/ui/` 新增:
1. `EmptyState`:图标+一句说明+主行动按钮(可选次行动链接)。API:
   `{icon?, title, hint?, action?: {label, to|onClick}}`。
2. `StatCard`:label/value/hint/tone 统一密度(即 Dashboard 现有卡的抽象化,替换各页手写)。
3. `Skeleton`:块级加载占位(替代"…"文本闪烁)。
   每件带 vitest(渲染分支判别:有无 action/加载态)。

**P1 用户门户高频页逐页整改(Owner 骂的重灾区)**
按序:①/overview 我的概览(配额空态→「去看配额说明」引导+已配时进度条;用量空态→
「去接入指引」按钮;三卡改 StatCard 密度)②/keys(空 Key→「创建第一个 Key」主按钮)
③/usage、/wallet、/orders(空态+表格加载骨架)。
**P2 运营台高频页**:/accounts(空池→「接入第一个上游账号」)、/users、/admin/orders。

## 派工

实现按页切片派 codex(每次一页+精确文件清单,防大范围只读超时);公共组件底座由我先落
(接口设计定契约),后续页 codex 照契约铺。

## 不做

布局/导航/配色 token/新依赖/后端;不照搬任何参考项目外壳(反克隆红线)。

## 成功标准

- 整改页零死灰字空态:每个空态都有行动引导;加载不再是文本"…"
- vitest:EmptyState/StatCard 分支判别测试;E2E smoke/buttons 全绿(按钮数会增,断言随更)
- Owner 逐页过目截图(5176 预览栈)

## 爆炸半径

纯前端表现层;每页一 commit 可独立回滚;公共组件新增不影响未接入页。

## 交叉合成(2026-07-13,#10 双稿对照)

两稿收敛:P0 公共底座先行(EmptyState+Skeleton)→ 逐页整改;边界一致(不动布局/导航/
token/依赖)。分歧与吸收:
- 采纳 codex 稿:EmptyState 增 tone(neutral/positive/unavailable——"暂无告警"是好事该
  用 positive 而非灰);Skeleton 系列变体+aria-busy/减少动效偏好;密度原则(压无信息
  垂直空白,卡头带状态摘要);Dashboard 管线卡压紧凑行动网格(不动 PIPELINE_NAV 数据)。
- 采纳 Claude 稿:StatCard 一并入底座(替换各页手写指标卡);实现按页切片派 codex;
  P2 运营台页清单(/accounts /users /admin/orders)。
- 顺序合成:P0 底座(EmptyState/Skeleton/StatCard+判别测试)→ P1 ①/overview ②/keys
  ③/usage ④/wallet → P1.5 控制台总览管线卡紧凑化 → P2 运营台。P0+P1① 先落,5176 给
  Owner 过目后再铺后续页。
