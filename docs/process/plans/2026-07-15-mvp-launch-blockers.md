# MVP 整体测试上线 · blocker 清单(三路真码摸底汇总)

日期:2026-07-15。分支 feat/ui-density-overview @ 8fc97c39。三路调研(relay 闭环 / 历史缺陷+测试网 / 运维安全监测)全部 grep 真码 file:line 佐证。

## 总判
**S0 阻断项:零。** relay 核心链首尾闭合(hold 必 settle/release 同事务原子、断连 detachedAbort 不泄漏、换号状态干净、四个补偿 worker 全启动)、启动门干净、docker-compose.direct 无域名可直跑、前端真内嵌发货(Dockerfile 写死 -tags embed)。历史 6 类 S0/S1 中 5 类已真修且有判别测试守护(72 个 integration_pg 包真 PG 跑),KEK 轮换为已知 Owner-gated 部分项(数据模型+启动自检就绪,缺 re-wrap worker,非阻断)。

## S1(上线前必解,4 项)
1. **模型基础定价无后台入口 + 旗舰模型未种价 → fail-closed 拒服务**。billing_pricing_versions 全仓无应用层写路径;种子仅 gpt-4.1-mini+国内厂+MiniMax+gpt-5.6+图像;**Claude/GPT-4o/Gemini 文本全无价** → chat_completions_pricing.go:195-218 pricingUnavailable → 每请求 503。解法:补 admin 定价 upsert 路由(**触 money,Owner 点头后做**)或先手工 SQL 种价。
2. **main 分支保护丢失**(rulesets 空 + protection 404 双证)。2026-06-29 开过的保护已不在,「骑红合」风险回归。Claude 重开被权限闸拦(治理类操作),**需 Owner 授权或自行在 GitHub 设置开**(required = "Go test + race + vet" + "integration_pg (per-package isolated DB; money/quota/cross-tenant)",strict + enforce_admins)。
3. **全链路 smoke E2E 不在 CI**。cmd/gateway/smoke_test.go(建二进制→子进程→真 PG→mock 上游→5 项 PG 落账断言)是唯一 dispatch→forward→bill 全链守护,但 tag=smoke CI 不带 → 从不自动跑。解法:CI 加 smoke job(mock 上游无需真 key)。
4. **前端 vitest 不在 CI**(本机 1715/1715 绿;Docker build 只跑 tsc+vite,测试回归无自动守护)。解法:CI 加 frontend job(tsc+vitest)。

## S2(应处理,不阻断)
- internal/email TestAT_OBS_005_008 时序 flaky(重跑即绿),污染 integration_pg 稳定性。
- 限流默认值(RPM60/并发5)仅 env 可调,无 admin UI。
- platform_settings 60+ 键未做 100% 死项穷举(抽查全活)。
- chat_completions_pricing.go:37 TODO:工具调用次数未接上游真实 usage 计数(计费精度小项)。
- SettlementIntent sweeper 默认关(fail-open 设计,PendingReconciliationWorker 兜底,可接受)。

## 运维安全监测模块(Owner 主线需求,方案见 2026-07-15-security-monitoring-module-claude.md)
应用层骨架已相当完整(登录爆破封IP/审计三表/风控总览/告警/健康/运行日志,大多有 UI);缺=①security_events 归集表+落库钩子(封IP决策现纯内存,看不到"谁在攻击")②系统资源采集(CPU/内存/磁盘,激活死掉的 cpu_usage_percent 告警指标)③「安全监测台」聚合页。真主机层(端口扫描/SSH爆破)网关看不到,三镜全无等价:部署文档指引 fail2ban+云安全组,可选旁路 agent(Owner-gated)。

## 执行序(主线)
1. C③ 验收后 → 派 codex「上线加固切片」:CI 加 smoke job + frontend job + email flaky 修。
2. 安全监测模块切片①(表+钩子+聚合端点)→②资源采集→③UI页(页面级 Owner 点头)→④部署文档。
3. 定价 admin 路由:Owner 点头后做(money);未点头前上线手册写手工种价 SQL 步骤。
4. 分支保护:Owner 侧动作。
5. 全部闭环后:整体测试(真账号逐个链路 + 部署形态×出口×协议环境矩阵,per Owner 嘱托)→ 上线。
