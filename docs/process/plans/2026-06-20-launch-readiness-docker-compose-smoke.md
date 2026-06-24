# 上线就绪评估 — 真做 docker compose up 冒烟实测 + 可签字部署方案

日期:2026-06-20
作者:Claude(PM-Orchestrator)
状态:**评估完成,待 Owner 签字落 deploy 改动**(本文只读评估 + 方案;所有 prod/compose 改动 Owner-gated,未自主落)

---

## 0. 一句话结论

**relay 核心真能跑,production 模式也真能达全绿。** 在干净宿主上从零 `docker compose` 把整栈拉起来、真打通了端到端 relay 链路(入站鉴权→路由→池选择→executor→上游转发);**第二程进一步把 production 模式真 traverse 到 healthy + relay 跑通**(配齐 audit 私钥 + ledger=postgres + 每个正数 active tenant 的 email 即过)。

**但揪出一个真 blocker B0(见 §6):** 全新迁移库里系统伪租户 `tenant 0`('public-pricing')是 active,production email 门会检它,而唯一的 email 配置 API 拒绝 `tenant_id=0` → **production 模式在全新库上无受支持路径过 email 门 = 启动失败**。这是逻辑冲突,需 Owner 定修法(release-gate 行为变更)。除此之外离自用上线只差一批纯工程接线。

这正面回答 Owner 两次担心的"太严苛会不会上线跑不起来":**不会**。严苛的是启动期 fail-loud 自检(缺迁移/缺生产邮件配置/token 形状不对就拒启),它们恰恰是把"带病配置静默跑"挡在门外的护栏,而非阻止程序运行。

---

## 1. 实测做了什么(全部非 gated 只读/验证,已全部清理零残留)

在本机用 `sudo docker`(dockerd 在跑)起了一套**独立 project(`huakai-smoke`)、独立端口(gateway 18080 / postgres 15432)**的冒烟栈,基于仓库现有 `backend/docker-compose.prod.yml` + 仅改端口/release-mode 的临时 override(写在 `/tmp`,不进 repo)。临时密钥本地生成、用后即焚。实测完毕已 `down -v` + 删镜像 + 删密钥目录,`git status` 确认 repo 零残留。

### 实测时间线(每一步都有真实日志佐证)

| 步骤 | 结果 | 证据 |
|---|---|---|
| `docker build`(backend/Dockerfile) | ✅ **干净出镜像** | 96.3MB Go/alpine 镜像,exit 0 |
| `compose up` 起 postgres | ✅ healthy | pg_isready 通过 |
| `compose up` 起 gateway(prod 原样,**无迁移**) | ❌ **crash-loop** | `fatal: relation "tenants" does not exist (SQLSTATE 42P01)`,restarts=8 |
| 手动跑 148 个迁移(golang-migrate) | ✅ 全过 0→0151 | exit 0,无报错 |
| 重启 gateway(production 模式) | ❌ 进入下一道门 | `fatal: production email release gate: tenant 0: settings invalid` |
| 切 dev 模式重启(合法跳过 email 门) | ❌ 暴露第三道门 | `fatal: HUAKAI_ADMIN_BOOTSTRAP_TOKEN must be 'hk_admin_<24-char-base32>'` |
| 用合规 token 重启 | ✅ **healthy,restarts=0** | gateway 完整启动 |
| `curl /healthz` | ✅ **HTTP 200** | `{"status":"ok"}` |
| `mvp-seed` 播种路由目标 | ✅ | tenant+user+balance+api_key+provider→channel→account→pool→model→alias 全套 |
| relay 冒烟:播种 key 打 `/v1/chat/completions` | ✅ **管道通** | `upstream_oauth_invalid_grant`(打到了上游,只因上游凭据是假的才失败) |
| 对照:无效 key 打同端点 | ✅ **鉴权有判别力** | `unauthorized: invalid bearer`(坏 key 当门拒,错误码与上面完全不同) |

**两个 401 的对比是端到端铁证**:有效 key → `upstream_*`(走完全程到上游);无效 key → `unauthorized`(门口就拒)。证明 relay 整条链路真跑通,唯一缺的是**真上游账号凭据**(运维配置,非代码)。

---

## 2. 三道启动门(都是真实存在、有意为之的护栏)

gateway `main → run → buildGatewayRuntime` 启动期顺序自检,任一不过即 `log.Fatal` 拒启(`restart: unless-stopped` 会持续重启 = crash-loop):

1. **迁移门(隐式)**:gateway **不自管迁移**(`cmd/gateway` 无 migrate 接线、无 `go:embed`),首个查库点(production email gate 读 `tenants`)直接撞 `42P01`。→ **prod compose 缺迁移步骤 = #1 blocker。**
2. **production email release gate**(`internal/email/sender_factory.go:213` `ValidateProductionReleaseGate`,仅 `HUAKAI_RELEASE_MODE=production` 触发):要求至少一个 active tenant 且其 SMTP 设置有效 + `VerifyEmailEnabled=true`。空库的 tenant 0 没配 → 拒启。→ **生产模式硬依赖 SMTP 配置 = #2 blocker(dev/test 模式不触发)。**
3. **admin bootstrap token 形状门**(`cmd/gateway/wiring.go` admin bootstrap):`HUAKAI_ADMIN_BOOTSTRAP_TOKEN` 必须是 `hk_admin_<24位base32>`(共 33 字符)。→ **配置要求,非缺陷;需文档 + 生成助手。**

---

## 3. 离"自用上线"的纯工程接线缺口清单(无一是代码缺陷)

| # | 缺口 | 性质 | 影响 | 建议修法 | 风险 |
|---|---|---|---|---|---|
| G1 | **prod compose 缺迁移步骤** | deploy 接线 | 首启 crash-loop | prod compose 加一次性 `migrate` init service(golang-migrate 镜像,gateway `depends_on: migrate: service_completed_successfully`) | 低,纯 additive |
| G2 | **生产 email 门引导未文档化** | 文档 + 引导 | 生产模式拒启,易困惑 | 文档化引导序列:迁移→dev 模式起→`/v1/admin/email` 配 SMTP→切 production;或提供 SQL/CLI 播种 email_settings | 低 |
| G3 | **bootstrap token 形状无文档/无生成器** | 文档 + 工具 | 配错即拒启 | 文档写明形状 + 加一行生成命令(或 `cmd` 小工具) | 低 |
| G4 | **prod 无 `.env.example` / 密钥生成指引** | 文档 | 运维不知如何生成 4 个密钥 | 加 `backend/.env.prod.example`(占位)+ 密钥生成命令(`CREDENTIAL_KEY_B64`/`SESSION_SIGNING_KEY`/`BOOTSTRAP_TOKEN`/`POSTGRES_PASSWORD`) | 低 |
| G5 | **无 TLS 终止** | deploy 接线 | gateway 仅裸 HTTP :8080 | 文档 + compose 选配反代(Caddy/nginx/Traefik)终止 TLS;自用可先 localhost/VPN | 低-中 |
| G6 | **prod compose 无前端服务** | 已知范围 | 无 Web 控制台 | 自用上线 = **API-only**(bootstrap token + admin API);运维控制台(feat/frontend-portal)是独立轨,README 自述"尚未实现" | — |
| G7 | **README 陈旧** | 文档 | 误导 | 迁移自述"through 0093"实为 **0151**;前端"placeholder"现有 .next 构建产物;更正 | 低 |

> 注:G6 印证记忆 [产品核心是 relay 非支付] / [上线就绪审计 2026-06-19] —— **手动 admin 充值已可替代支付**,API-only 自用不被前端阻塞。

---

## 4. 可签字的部署方案(建议执行顺序;每项均 Owner-gated)

> 全部是 deploy/prod 改动,**等 Owner 签字后才落**,按硬规则一律走独立分支 + 对抗审查 + PR。

- **方案 A(最小可用,自用 API-only)**:G1(migrate sidecar)+ G3/G4(token 形状 + prod env 样例 + 密钥生成文档)+ G2(生产 email 引导文档)+ G7(README 更正)。落完后 `docker compose -f docker-compose.prod.yml up` 一把起栈、配密钥即跑通 relay。**估:0.5–1 人日纯接线。**
- **方案 B(对外/多用户)**:A + G5(TLS 反代)+ 生产 email 真配 SMTP + G6 前端控制台落地(独立大轨)。
- **不做**:支付 provider(手动充值替代,非 blocker);任何"为质量而质量"的后端硬化(本会话已证后端比功能树更完整,残留为细粒度/边界/碰撞面)。

---

## 5. 我没有做、留给 Owner 拍板的事

1. **未改任何 prod/compose 文件**(deploy 改动 Owner-gated)。本文只读评估 + 方案。
2. 未提交本 doc 之外的任何东西。
3. 是否落方案 A / 落到什么程度 / 是否动前端 —— 等 Owner 一句话。

---

## 6. [2026-06-20 第二程] production 模式全 traverse + 发现 B0 严重 blocker

第一程只在 **dev 模式**达 healthy(production 被 email 门挡住没走完)。第二程把 production 模式真 traverse 到底,把所有 production-only 启动门逐一摸清并满足。

### production 模式启动门全清单(都已读真码 + 实测验证)

| 门 | 触发位置 | 要求 | 满足方式 |
|---|---|---|---|
| release mode | `config.go:66` | env 是 production/dev/development/test | 设 `HUAKAI_RELEASE_MODE=production` |
| dev token flag | `config.go:185` | production 下 `HUAKAI_DEV_AUTH_RETURN_TOKEN` 不得 true | 不设即过 |
| audit ledger | `config.go:415` | production 要求 `HUAKAI_AUDIT_LEDGER_BACKEND=postgres` | 设之 |
| audit 私钥 | `config.go:435` | production 要求 `HUAKAI_AUDIT_PRIVATE_KEY_PATH`(ed25519,接受 PKCS8 PEM 或 64B 裸key) | `openssl genpkey -algorithm ed25519` 生成 + 挂载 |
| channelhealth signer | `wiring.go:862` | production 要求 audit signer 非 nil | 同上一把私钥 |
| **email 门** | `wiring.go:838` `sender_factory.go:213` | **每个** active tenant 配齐 SMTP(host/port/username/password/from)+ `email_verify_enabled=true` | admin API `PUT /v1/admin/email/settings` 逐租户配 |
| channelhealth store | `wiring.go:866` | production store mode | 自动,无额外要求 |

**实测结果:配齐以上后,production 模式 `restarts=0 health=healthy`,`/healthz` HTTP 200,relay 端到端跑通**(tenant-2 key 打到上游得 `upstream_oauth_invalid_grant`,与 dev 一致)。audit 私钥成功加载(日志 `audit private key loaded fingerprint=...`)。**即:production 模式没有"跑不起来"的代码缺陷,配齐即绿。**

### ⚠️ B0(真 blocker,需 Owner 定修法)— 系统租户 0 卡死 production email 门

- **现象**:全新迁移库里,迁移 `0030_pricing_versions_public_scope` 播种 `tenant id=0, name='public-pricing', status='active'`(公开定价 scope 的系统伪租户)。
- **冲突**:production email 门 `ValidateProductionReleaseGate` → `ListActiveTenantIDs`(`internal/email/settings_store.go:116`)SQL 是 `WHERE status='active' AND deleted_at IS NULL`,**不排除 tenant 0**;门要求 tenant 0 配齐 SMTP。但唯一配置入口 admin API `PUT /v1/admin/email/settings`(`internal/gatewayhttp/admin_email_settings_handler.go`)对 `tenant_id<=0` 返回 `400 tenant_id must be positive`。
- **后果**:**production 模式在全新库上无任何受支持路径让 tenant 0 过 email 门 → 启动 fail-loud 拒启**(实测确认:tenant 0 active 时报 `production email release gate: tenant 0: ...`;把 tenant 0 临时置 inactive 后 production 立即达 healthy,证 tenant 0 是 email 门唯一 blocker)。
- **严重度**:S1(挡核心功能=production 根本起不来;非安全/数据暴露)。无运维 workaround(裸 SQL 播种 tenant 0 的 email 也不行——password 须用运行时 key 正确 AES-GCM 加密)。
- **修法选项(Owner 定;改的是 production 安全门范围 = Owner-gated)**:
  1. **(推荐)email 门排除系统伪租户**:`ListActiveTenantIDs`(或门本身)加 `AND id > 0` / 按 system 标志排除 tenant 0。语义正确——pricing-scope 伪租户不发验证邮件。改动小、additive、可测(变异:不排除则 tenant 0 仍卡)。
  2. admin email API 放开 `tenant_id=0`:语义不对(伪租户不该有 email),不推荐。
  3. 迁移 0030 改 tenant 0 的 status:风险大(pricing scope 可能依赖它 active),不推荐。

### 附带观察(非 blocker,纳入 G2 文档)
email 门要求**每个**正数 active tenant 都先配 SMTP 才能起 production。单运维自用也要给 tenant 1('default')配 email。引导文档需写明"每租户配 email"这步。

## 附:复现命令(供 Owner 自验)

```bash
cd backend
# 1. 构建
sudo docker build -t huakai-gateway:smoke .
# 2. 起 postgres + 迁移(golang-migrate)+ gateway
#    (prod compose 当前缺第 2 步的迁移,这正是 G1)
# 3. 健康 + relay 冒烟
curl -s localhost:8080/healthz                       # {"status":"ok"}
curl -s -XPOST localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer <播种的 hk_test_key>" \
  -d '{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"ping"}]}'
#    有效 key → upstream_*(管道通);无效 key → unauthorized(门口拒)
```
