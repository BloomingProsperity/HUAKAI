# /setup 首装向导 + 登录角色分流 + 单侧栏角色导航(sub2api 形态照抄)

Owner 指令:按成熟参考项目的结构与方法来。三镜对照(行为级结论,证据引用见下):
sub2api 具备完整形态——首装向导端点(状态探测公开只读、安装受"未装才放行"守卫保护、装完
fail-closed 关死)、登录按角色分流(管理员直落管理仪表盘、普通用户落个人面板)、单侧栏按角色
渲染(管理员=管理导航+个人功能收纳为一组,无双端切换门);new-api 无向导(首启种子管理员
账号);CLIProxyAPI 纯代理无用户面板,无等价物。默认跟 sub2api 的形态。

**证据引用(source-must-read;lane=specifier,本会话读源→行为摘要,实现全自写)**:
- 首装端点/守卫:`sub2api@12d811b:backend/internal/setup/handler.go:22-62`(状态只读+改动
  端点受守卫,已装 403);安装判定 `sub2api@12d811b:backend/internal/setup/setup.go:151-163`
- 向导前端与收尾:`sub2api@12d811b:frontend/src/views/setup/SetupWizardView.vue:619-660`
  (装完跳登录,不自动登录)
- 登录角色分流:`sub2api@12d811b:frontend/src/router/setupRedirect.ts:1-7` 与
  `frontend/src/router/index.ts:782`(按管理员身份分流首页)
- 单侧栏按角色:`sub2api@12d811b:frontend/src/components/layout/AppSidebar.vue:35-38,730-753`
  (管理导航+个人功能一组,无双端切换门)
- 无等价物:`new-api@246d62a` 全仓无 setup 向导目录(首启种子管理员);
  `CLIProxyAPI@26d45fd` 无用户面板模块
- clean-room:sub2api 为 LGPL——上列引用仅作行为证据,不复用任何代码/标识符/结构,
  HUAKAI 实现(`backend/internal/setuphttp`、`frontend/src/features/setup`)全部自写。
  Source files read: 上列 5 文件;lane=specifier;agent=Claude PM 本会话;2026-07-13 UTC。

**遗留口径(S2,记 DEFERRED)**:`HUAKAI_DEFAULT_WORKING_TENANT_ID` 配非 1 时,登录页
固定提交租户 1(单实例既定形态,注册/2FA 同受此限,非本切片引入)——首装建到 env 租户后
无法从登录页进入。统一口径需 site-config 暴露工作租户并贯通登录链,排后续切片。

## 范围

**后端 `internal/setuphttp`(新包)**
- `GET /setup/status` 公开只读 → `{needs_setup}`;判定=工作租户无 role='admin' 未软删用户
  (sub2 用配置文件+lock 文件,我们配置在 env,以 DB 事实为准更贴我们部署形态)
- `POST /setup/install` `{email,password,display_name?}` → 建 admin(argon2id 哈希复用
  userauth.HashPassword,email_verified=true,status=active,role=admin)
- fail-closed:NeedsSetup=false 一律 403;advisory lock+锁内二次判定防并发双装(TOCTOU)
- routes.go 挂载(无鉴权,guard 自守)+ openapi.yaml 补契约

**前端**
- `/setup` 向导页(壳外公开路由):步骤条 管理员→完成(我们无 DB/Redis 步,env 已配),
  成功后跳 /login(sub2 同款,不自动登录)
- 启动分流:LoginPage/RequireAuth 挂载时查 /setup/status(模块级缓存一次),needs_setup→强制
  /setup;已装则 /setup 访问→/login
- 登录落点:operatorEnabled → `/`(运营台首页),否则 `/overview`(sub2 分流形态)
- 单侧栏:PipelineNav 按角色渲染——admin=运营台分组+「我的账户」分组(原用户门户分组收纳);
  user=用户门户分组;**删「进入管理后台」浮动按钮**

## 成功标准
- 后端 httptest:status 两态/install 成功建 admin/已装 403/并发双装单胜者/弱口令 400
- 前端 vitest:分流纯逻辑(needsSetup×path→目标)判别测试;门禁 tsc+vitest+build 绿
- E2E:现有 smoke/buttons/dashboard 全绿(setup 流程需空库,后端测试覆盖,E2E 不造)
- codex 审查零未结 S0/S1

## 爆炸半径
- 登录后落点变化影响所有 admin 用户习惯(Owner 点名要的)
- 侧栏重组只动渲染层,路由/权限守卫不动;后端新包无既有面伤害
