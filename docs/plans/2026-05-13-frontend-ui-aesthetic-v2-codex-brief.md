# HUAKAI 前端 UI 美学调研 v2 — Codex Brief（非蓝绿强约束）

## 触发

Owner 看 Lane A v1 报告（`docs/research/2026-05-13-frontend-ui-aesthetic-codex.md`）后判定：

> "Sub2 是蓝色的，不能一样。"

v1 推荐的方案 A（indigo `#4F46E5`）还是蓝家族。Owner 要 HUAKAI 在视觉上与 sub2api **完全分家**。

## 硬约束（v2 必守）

**严禁**作为 primary / brand 强调色：

- ❌ blue family（任何 hue 在 200°-260° 之间的中高饱和色）
- ❌ teal / cyan / mint / sky（hue 170°-210°，sub2api `#14b8a6` 本体）
- ❌ indigo / violet 偏蓝调（hue 240°-270°，方案 A 的 `#4F46E5` / `#5E6AD2` 都在这区间）
- ❌ grey-blue undertone 主色

**允许**的 primary 强调色家族：

- ✅ 纯紫 / 深紫（hue 270°-300°，明显紫 > 蓝，如 violet-600 `#7C3AED` / purple-600 `#9333EA`）
- ✅ 橙 / 琥珀（hue 25°-45°，amber `#F59E0B` / orange `#EA580C`）
- ✅ 翠绿 / 苔藓 / 草绿（hue 100°-140°，注意避开 teal/emerald，emerald `#10B981` 偏蓝绿不允许；推荐 lime `#65A30D` 或 grass `#16A34A`）
- ✅ 玫红 / 暖红（hue 340°-10°，rose `#E11D48` / red `#DC2626`）
- ✅ 黑白单色（hue 任意，饱和度 < 5%，luminance 极值）

底色（background / card / muted / border）可中性灰 / 暖白 / 偏紫白 / 偏粉白 / 偏暖白，**不要冷调白**（避免 sub2api 那种 `#F8FAFC` 偏蓝白）。

## 任务

参考 v1 brief 同样 6 节结构，**只换 5 套备选方案**。每套必须：

1. primary 强调色明确**非蓝绿**（按上面"允许"清单选）
2. light 底色避免冷调白
3. dark 底色避免 slate-teal 调
4. status 色（success/warning/danger）独立于 primary，**不要让 primary 和 success/danger 冲突**（比如 primary 用红时，danger 不能也是红）
5. 给完整 light + dark token map（hex + HSL 双标）
6. 给 1-2 句 HUAKAI 视角的适配度评估

## 强烈考虑（HUAKAI 调性参考）

- **HUAKAI 项目特征**：MIT 协议 AI gateway + 账号池 + 运营平台，给运营者用
- **中文品牌名 "华开 / 华凯"**：暖色（橙 / 红 / 紫）天然契合中文文化温度
- **避免**：开源 infra 工具感（teal / cyan 是这类的标准色），"硬核监控"感（深蓝 + 电蓝）
- **追求**：商业 SaaS 工作台、成熟、有差异化品牌记忆点

## v1 已涵盖、不要重做

- v1 方案 B（Vercel 黑白）可以保留 1 套（如 5 套之一）
- v1 方案 A / D / E 都是蓝家族，**全部废弃**
- v1 方案 C（石英白 + 深海蓝）也是蓝家族，废弃

## 推荐顺序提示（v2 必给）

每套含一行"为什么 HUAKAI 该选这套"+ 一行"风险 / 不适合的场景"。最后明确 **推荐 1 套** + 3-5 条理由 + 直接可粘贴 globals.css 字符串。

## 产出

`docs/research/2026-05-13-frontend-ui-aesthetic-v2-codex.md`（新文件，不覆盖 v1）

防死提示：

1. 第一件事 echo stub 到 `/tmp/codex-ui-aesthetic-v2.txt`
2. 每节完成 `>>` 追加进度
3. v1 stdout 已 65K 行，v2 不需要重读 ref 项目；直接基于色彩理论 + HUAKAI 调性出方案

## 不在范围

- 不动代码 / 不起 dev server
- 不读 sub2api decomp doc
- 不重新对比 market refs（v1 已对比，引用即可）
- 不研究 typography / spacing / radius

直接开始。
