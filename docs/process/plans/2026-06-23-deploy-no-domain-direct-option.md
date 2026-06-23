# 计划:无域名 / IP 直连部署形态(作为选项,不强制域名)

- 日期:2026-06-23
- 作者:Claude(PM-Orchestrator)
- 切片:deploy/no-domain-direct-option
- 分支基线:origin/feat/frontend-portal @0720625e

## 背景与 Owner 指令

Owner 明确:「我们不强制有域名。只是设置里面可以增加这个选项。像 sub2 一样的,有使用者决定。我们不做决定,只是提供选项。」

现状(已亲核真码):`docker-compose.prod.yml` 把 Caddy + 域名(`HUAKAI_PUBLIC_DOMAIN`)做成了**唯一**生产形态,等于替运维焊死了"必须有域名"。这与 Owner 的"不强制、给选项"相悖。

`HUAKAI_PUBLIC_DOMAIN` 仅 Caddy 那一层消费,**gateway 本体全程不读它**(backend 内零 Go 引用,grep 证实)。gateway 默认绑 `:8080`(`backend/internal/config/config.go:236`,`HUAKAI_ADDR` 默认 `:8080`),不做任何域名/Host 校验。所以"无域名直连"在应用层本就成立,缺的只是一个不含 Caddy 的部署编排 + 文档。

## 三镜对照(§16 必读,已核 file:line)

| 项目 | 默认对外形态 | 内置 TLS | 给运维的"选项"姿态 |
|---|---|---|---|
| sub2api @e34ad2b | `0.0.0.0:8080` 明文 HTTP,装完直接给 `http://IP:8080`(`deploy/install.sh:787`、`README.md:194`) | 无;`cmd/server/main.go:129` 用 `ListenAndServe` 非 TLS | TLS 是**可选**外部反代(`deploy/Caddyfile` 范例),默认不编进编排 |
| new-api @1ac0f58 | `:3000` 明文 HTTP,README 全程引导 `localhost:3000` | 无;程序内无 HTTPS 开关 | 必须外置 nginx/宝塔/Caddy 终结 TLS |
| CLIProxyAPI @2a050dc | host 默认空绑全网卡 `:8317` 明文 | 内置**可选** TLS(`tls.enable` 默认关,需自备 cert/key,无 ACME) | TLS 完全由用户开关决定 |

**结论**:三镜默认全是"IP + 明文 HTTP",TLS 一律是运维可选项(外部反代或内置开关),**没有一个把域名焊成唯一形态**。HUAKAI 当前 prod compose 比三镜都"硬",需补回"无域名直连"选项以对齐"运维自决"。

**默认 tiebreaker = sub2api**:其做法 = 应用裸暴露端口 + TLS 留给可选外部反代。HUAKAI 直连形态采同款:gateway 直接发布到宿主端口,TLS 由运维自行决定(自带反代 / 或纯内网明文)。

**HUAKAI 升级 delta(生态升级)**:① `HUAKAI_RELEASE_MODE` 必填、拒静默降级(`config.go:66-72`),运维显式声明 dev/production,而非默认裸跑;② per-IP 限流 + `HUAKAI_TRUSTED_PROXY_CIDRS` fail-closed XFF 解析对"直连公网"与"同机反代回源"两路都正确;③ 迁移 one-shot 外置受控。

## 范围(scope)

纯新增 + 文档,**不改任何 gateway 逻辑、不改 prod/Caddy 那套**:

1. **新增 `backend/docker-compose.direct.yml`**:postgres + migrate(one-shot)+ gateway,**不含 Caddy**。gateway 直接发布到宿主端口:
   - `ports: ["${HUAKAI_HTTP_BIND:-0.0.0.0}:${HUAKAI_HTTP_PORT:-8080}:8080"]`(网卡/端口可调)。
   - `HUAKAI_RELEASE_MODE: ${HUAKAI_RELEASE_MODE}`(必填,运维选 dev / production)。
   - 审计卷挂载 + 审计 env **默认注释**:dev 模式无需私钥即可启动(`config.go:425/437`);production 运维取消注释并填 `HUAKAI_AUDIT_PRIVATE_KEY_HOST` 即硬化(`config.go:415/435`)。
   - 独立网络名 `huakai-direct-net` + 独立卷 `huakai-direct-pgdata`,与 prod 互不撞。
2. **新增 `backend/.env.direct.example`**:直连形态的 env 样例,两种 `RELEASE_MODE` 选择都给注释说明,**不含 `HUAKAI_PUBLIC_DOMAIN`**。
3. **更新 `docs/deploy/production-bootstrap.md`**:加"部署形态选择"小节,把两形态(域名+Caddy 自动 HTTPS / 无域名 IP 直连)平级列出 + 安全权衡讲清,运维自行决定。

## 成功标准

- `docker compose -f docker-compose.direct.yml config` 校验通过(语法 + 变量插值正确)。
- **真起栈实测**(隔离 project + 非冲突宿主端口 + 临时密钥用后即焚):dev 模式直连起栈 → `curl http://127.0.0.1:<port>/healthz` 得 **200**,全程无域名、无 Caddy。证"无域名 IP 直连"真能跑通。
- 对抗审查零 S0/S1。
- 仓库零残留(实测后 `down -v` + 删临时密钥)。

## blast radius

极小:纯新增文件 + 一处文档增量。不碰 gateway 代码、不碰 prod compose、不碰任何碰撞包。最坏情况 = 新 compose 写错,但不影响既有 prod 路径;真起栈实测兜底。

## 可能出错点

1. 审计卷挂载若不注释,dev 运维缺 `./secrets/audit_key.pem` 会被 docker 当目录创建 → 用"默认注释 + production 才取消注释"规避。
2. XFF 信任:直连公网时若错误信任 XFF 会被伪造源 IP。已核 clientip 对非可信来源 fail-closed,公网客户端 socket 源 IP 非 RFC1918 → 其 XFF 被忽略,限流按真实 socket IP;同机反代回源属 RFC1918 → XFF 受信,正确。默认 RFC1918 对两路都对。
3. 端口冲突:实测用非冲突高端口,绑 `127.0.0.1` 避免实测期对外暴露。

## 决策点(Owner)

- 本切片是 deploy 改动(Owner-gated),但 Owner 已就"加无域名选项"直接下指令,等于绿灯;按 cadence 落地后 surface。
- 不替运维设 `RELEASE_MODE` 默认 = 贯彻"我们不做决定,只提供选项"。
- codex 当前不可用,#10 双轨计划改为单轨 + #8 对抗审查工作流替代门禁(见 account-api-remediation 授权)。

## 安全权衡(写入文档,运维自决)

无域名 = 纯 HTTP = `hk_key` 明文过网,可被同网段/运营商/公共 Wi-Fi 嗅探盗用。
- 自测 / 内网(可信网络):可直接用。
- 对公网卖额度:务必前置一层 TLS(运维自己的 nginx / Caddy / LB / 宝塔 + 证书),或改用 prod 域名+Caddy 形态。
我们提供"无域名"选项,**是否加 TLS、用哪种形态由运维决定**。
