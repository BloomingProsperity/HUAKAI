# Wave2 — 公告 announcements admin CRUD 前端（计划留痕）

- 日期：2026-06-17
- 切片：Slice 6（Wave2 admin 后台补全）
- 分支：`feat/admin-announcements`（base `origin/feat/frontend-portal` @ 15d5129b）
- 协调锁：`claude-announcements`
- 选刀依据：proxies 分支仍活跃 → 避让 provider/channel/代理；复查 adminhttp 零覆盖面，公告为最小/低风险/非避让缺口。

## 后端权威契约（读真码，禁止凭记忆）

- `backend/internal/announcementhttp/handlers.go` + `backend/internal/announcement/service.go` + `routes.go:1038`
- 端点（前缀 `/v1/admin/announcements`，注意 `/v1/admin` 非 `/admin/v1`）：
  - GET `/v1/admin/announcements?tenant_id&limit&offset` → `{object,items,limit,offset}`
  - POST `/v1/admin/announcements`（tenant_id 在 **body**）→ 201 item
  - PUT `/v1/admin/announcements/{id}?tenant_id`（部分合并；tenant 在 query）→ item
  - DELETE `/v1/admin/announcements/{id}?tenant_id` → `{id,deleted}`
- 鉴权：platform_admin 或 tenant_operator（handlers.go:355）；platform_admin 必带 tenant_id，tenant_operator 用 scope。
- 校验（service.go validateAnnouncement:166）：title/body trim 非空；severity ∈ {info,warning,critical}（create 空默认 info）；
  published_at 非零（create 默认 now）；**expires_at 若存在必须严格晚于 published_at**（:178 `ExpiresAt.After`）；active create 默认 true。
- 请求体 **DisallowUnknownFields**（handlers.go:423）→ 前端只能发已知字段，禁多余键。
- 分页 limit 1-100 默认 50；body MaxBytes 64KB（无单字段长度上限）。
- 错误：invalid_json / tenant_id_required / announcement_id_required / invalid_limit / invalid_offset / 服务 invalid→400 / not_found→404。

## 借鉴对照（CLEAN-ROOM §11/§12/§16，仅功能形态，未抄码；源经 reviewer-lane 核实）

| 维度 | new-api@1ac0f58(AGPL) | sub2api@e34ad2b(LGPL) | CLIProxyAPI@2a050dc | HUAKAI delta · 维度 |
|---|---|---|---|---|
| 公告模型 | **两套**：①单条全局 Notice 串（`model/option.go:67` OptionMap["Notice"]、`controller/misc.go:172` GetNotice）；②**独立结构化 Announcements 模块**（`console_setting/config.go:8` JSON 数组串 + AnnouncementsEnabled，`controller/misc.go:129` GetAnnouncements、`validation.go:141-185`：数组≤100、per-record content、publishDate(RFC3339)、type 枚举{default,ongoing,success,warning,error}、按 publishDate 倒序，经通用 options 端点编辑）。但**无** per-row REST CRUD / 无 DB 表 / 无已读追踪 / 无租户 / 仅单 publishDate（无双时间窗） | **结构化实体** `backend/ent/schema/announcement.go`（title/content/status/notify_mode/targeting/starts_at/ends_at）+ 按用户**已读追踪** `backend/ent/schema/announcement_read.go`（唯一索引 announcement_id+user_id）；最全 | 无（纯中继，无等价物） | DB 表 + per-row REST CRUD + 按**租户**隔离 + **severity 分级(info/warning/critical)** + active + published/expires **双时间窗** + 后端 DisallowUnknownFields（`announcement/types.go:12-14`、`service.go:166-182`）· 较 new-api 多 REST CRUD/表/租户/双窗，较 sub2api 多 severity 分级（read-tracking/targeting/notify_mode 见 roadmap）|

## Feature-Preservation roadmap（sub2api 有、HUAKAI 后端暂无 → 登记不伪造）

- 公告**已读追踪**（per-user read state，sub2api announcement_read）——后端无 → roadmap。
- **受众 targeting**（定向投递，sub2api targeting JSON）——后端无 → roadmap。
- **notify_mode**（通知方式）——后端无 → roadmap。
- 本切片只接线后端【已有】的 CRUD + severity/active/时间窗，不伪造上述缺口。

## 文件（每个标注落点）

1. `frontend/lib/api/announcement-form.ts`（新，零依赖）：SEVERITIES、validateAnnouncementForm（title/body 必填 + severity 白名单 + 跨字段 expires>published + 时间合法性）、buildCreateBody（tenant 在 body、精确键集、不带 id）、buildUpdateBody（不带 tenant_id/id、expires 空→显式 null 清除）。
2. `frontend/lib/api/adminAnnouncements.ts`（新，客户端）：4 端点（list/create 用 client.ts apiGet/apiPost + 自带 adminPut/adminDelete）。
3. `frontend/app/admin/announcements/page.tsx`（新）：列表+分页 / 增改弹窗（title/body/severity/active/published/expires）/ 删除。**不动 Sidebar.tsx**（避让 proxies 分支）。
4. `frontend/lib/api/announcement-form.test.ts`（新）：纯逻辑单测 + 接线源文断言，全部变异验证。
5. `frontend/package.json`：加 `test:announcement`。

## 成功标准 / 风险

- tsc exit 0；`test:announcement` 全绿；邻测不破。
- 测试判别性变异实测：severity 白名单逐档、title/body 必填、跨字段 expires>published（含严格相等）、create 精确键集（DisallowUnknownFields）、update 禁 tenant_id/id、expires 空→null、4 端点路径/动词/builder/tenant 位置 —— 每个变异转红、还原绿。
- 对抗审查无未结 S0/S1。
- 爆炸半径：纯前端新增文件，不改后端/共享文件/Sidebar；公告=内容运维，无 money/auth/quota/security。低风险。
