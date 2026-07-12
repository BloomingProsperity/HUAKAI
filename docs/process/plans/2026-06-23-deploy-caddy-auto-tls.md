# 部署:Caddy 反代 + 自动 HTTPS(单租户 MVP 上线最后一道)

- 日期:2026-06-23
- 分支:`feat/deploy-caddy-tls`(off `feat/frontend-portal` @ 7edc9b07)
- 决策来源:Owner 选定 TLS/反代走 **Caddy 自动 TLS**(AskUserQuestion),且要"实际使用 + 升级方案,云上不合适";Owner 已绿灯按推荐升级版建
- 关联:[[go-live-readiness-2026-06-19]]、[[business-model-relay-saas-decision]]、[[multi-level-agent-reseller-direction]]

## 一、目标

单租户 MVP 上线的最后一道:把"gateway 裸 HTTP :8080"变成"一键起栈即 HTTPS"。

## 二、三镜对照(#16,clean-room,只引部署行为)

| | 反代/TLS 做法 | 证据 |
|---|---|---|
| sub2api | Caddy 反代 + 自动 Let's Encrypt(Caddyfile,reverse_proxy 到后端 :8080),CF 兼容头;**Caddyfile 模板让运维另起 Caddy**(不在主 compose) | `sub2api/deploy/Caddyfile`、`deploy/docker-compose.yml`(仅 app+pg+redis 无 caddy 服务) |
| new-api | app 裸 HTTP :3000;**TLS 外置**,文档用 nginx(常配宝塔面板)或 Cloudflare | `new-api/docker-compose.yml`(仅暴露 3000)、`new-api/docs/installation/BT.md`(宝塔配 nginx+SSL) |
| CLIProxyAPI | 纯 relay,**部署侧无反代/TLS 等价物**(个人 CLI 代理) | 前序核:无 web/TLS 模块 |

**共识** = app 裸 HTTP + 外层反代终结 TLS;sub2 选 Caddy(自动 TLS,最省心)。sub2 为默认 tiebreaker → 选 Caddy。

## 三、HUAKAI 升级 delta(融合 + 给代理未来铺路)

| 维度 | sub2/new-api | HUAKAI delta |
|---|---|---|
| 一键性 | sub2 让你另起 Caddy;new-api 让你外面配 nginx | **Caddy 打进 prod compose** = 一条命令起栈即 HTTPS(gateway 退到内网 expose,不再裸暴露 8080) → 生态升级 |
| 白标多域名 | nginx 每域名手动 certbot+reload | **Caddy on-demand TLS 预留**:代理白标自带域名首访自动签证;Caddyfile 末尾留模板(配 ask 授权端点,默认不启用)→ 架构升级,直接接 [[multi-level-agent-reseller-direction]] |
| 真实 IP | sub2 Caddyfile 转发 XFF | **接 HUAKAI clientip 信任链**:gateway `HUAKAI_TRUSTED_PROXY_CIDRS` 信任 compose 固定子网,per-IP 限流按真实客户端 IP(非 Caddy IP),对非可信 XFF fail-closed 防伪造 → 安全 |
| 加固 | — | HSTS/nosniff/referrer-policy/隐 Server 头 |

## 四、改动面(纯部署/文档,非碰撞,无 Go 码)

- 新增 `backend/Caddyfile`:`{$HUAKAI_PUBLIC_DOMAIN}` 站点,reverse_proxy gateway:8080 + 健康检查 + 安全头 + XFF;on-demand TLS 模板预留(注释)。
- 改 `backend/docker-compose.prod.yml`:加 `caddy` 服务(80/443/443udp + Caddyfile 挂载 + caddy-data/config 卷持久化证书 + depends_on gateway);gateway 由 `ports 8080:8080` 改 `expose 8080`(退内网)+ 加 `HUAKAI_TRUSTED_PROXY_CIDRS`;networks 固定子网 `172.28.0.0/16`;加 caddy 卷。
- 改 `backend/.env.prod.example`:加 `HUAKAI_PUBLIC_DOMAIN` + `HUAKAI_TRUSTED_PROXY_CIDRS`。
- 改 `docs/deploy/production-bootstrap.md`:HTTPS/Caddy 章节 + 验证口径(经 Caddi/HTTPS,gateway 内网)。

## 五、成功标准 / 验证

- `docker compose -f docker-compose.prod.yml config` 语法通过(已过)。
- `caddy validate` Caddyfile 语法通过(设 HUAKAI_PUBLIC_DOMAIN=localhost 校验)。
- gateway 不再对宿主暴露 8080(config 里为 expose);caddy 暴露 80/443。
- (运维侧真起栈验证需 Owner 机器 + 真域名,本切片到语法/拓扑校验为止)。

## 六、blast radius / 风险

- 纯部署/文档改动,无 Go 码、无 schema、非碰撞包;不影响应用逻辑。
- gateway 退内网后,若运维仍想本机直连 8080 调试 → 用 `docker compose exec` 或临时加回 ports(文档已说明)。
- Caddy 自动 TLS 依赖 80/443 公网可达 + 域名解析;未满足时 Caddy 起不来(预期门控,文档说明 localhost 联调法)。
- on-demand TLS 默认**不启用**(防任意域名触发签发),仅作代理阶段预留模板。
