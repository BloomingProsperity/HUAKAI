# 生产部署与首启引导(自用 / API-only)

本文给出用 `backend/docker-compose.prod.yml` 把 HUAKAI 在生产模式真正起栈跑通的步骤。
当前运维控制台 UI 尚未实现,**自用上线 = API-only**(用 bootstrap 管理员令牌 + admin API 管理),
手动 admin 充值已可替代真支付,真支付 provider 是可选增强而非上线前置。

> 说明:本文是运维对照,涉及 deploy/prod 改动按仓库规则属 Owner-gated。

## 1. 前置

- 已装 Docker + Docker Compose。
- 在 `backend/` 目录操作(compose、Dockerfile、迁移、env 样例都在此)。

## 2. 准备密钥与私钥(切勿提交进仓库)

复制样例并按其中注释生成各值:

```bash
cd backend
cp .env.prod.example .env
# 按 .env 顶部注释生成:CREDENTIAL_KEY / SESSION_SIGNING_KEY / ADMIN_BOOTSTRAP_TOKEN(hk_admin_<24base32>)/ POSTGRES_PASSWORD
# 注意 HUAKAI_DATABASE_URL 里的密码要与 POSTGRES_PASSWORD 一致。
mkdir -p secrets
openssl genpkey -algorithm ed25519 -out secrets/audit_key.pem   # production 审计签名私钥
```

`.gitignore` 已忽略 `.env` 与 `secrets/`;务必确认它们不会被提交。

## 3. 三道生产启动门(为什么不能"起了就跑")

`HUAKAI_RELEASE_MODE=production` 下,gateway 启动期 fail-loud 自检,任一不过即拒启(这是有意的安全护栏):

1. **数据库迁移**:gateway 不自管迁移;prod compose 的 `migrate` 一次性服务会在 gateway 之前把
   `sql/migrations` 应用到空库。漏了迁移,gateway 会因缺表崩溃。
2. **审计**:要求 `HUAKAI_AUDIT_LEDGER_BACKEND=postgres` + `HUAKAI_AUDIT_PRIVATE_KEY_PATH` 指向有效
   ed25519 私钥(上面已生成并由 compose 挂载)。
3. **email 就绪门**:要求**每个正 id active 工作租户**都配齐 SMTP 且开启邮件验证。系统伪租户
   `id=0`(定价 scope 哨兵)已被排除,不需配。空库首启时只有默认工作租户 `id=1`,需给它配 email。

## 4. 首启引导序列(解 email 门的"鸡生蛋")

email 门要求工作租户配齐 SMTP,但配置只能经 admin API、而 admin API 需要 gateway 已在跑。
顺序如下:

```bash
# 4.1 先以非生产模式起一次,跳过 email 门、让默认工作租户(id=1)被创建
HUAKAI_RELEASE_MODE=dev docker compose -f docker-compose.prod.yml up -d

# 4.2 用 bootstrap 令牌给工作租户 id=1 配 SMTP(开启邮件验证)
#     <BOOTSTRAP> = .env 里的 HUAKAI_ADMIN_BOOTSTRAP_TOKEN
curl -sS -X PUT http://127.0.0.1:8080/v1/admin/email/settings \
  -H "Authorization: Bearer <BOOTSTRAP>" -H "Content-Type: application/json" \
  -d '{"tenant_id":1,"smtp_host":"smtp.your-provider.example","smtp_port":587,
       "smtp_username":"you@your-domain.example","smtp_password":"<SMTP-PASS>",
       "smtp_from":"no-reply@your-domain.example","smtp_from_name":"HUAKAI",
       "smtp_use_tls":true,"email_verify_enabled":true}'

# 4.3 切回 production 模式重启,三道门此时都满足 → gateway 进入 healthy
HUAKAI_RELEASE_MODE=production docker compose -f docker-compose.prod.yml up -d
```

> 若新增了别的正 id 工作租户,需同样为它们各自配齐 SMTP,否则 production email 门不过。

## 5. HTTPS / 反向代理(Caddy 自动 TLS,已内置)

prod compose 内置 `caddy` 服务做 TLS 终结 + 反代,**gateway 不再对宿主暴露明文端口**,外部流量一律经
Caddy 走 HTTPS。上线只需:

1. 在 `.env` 设 `HUAKAI_PUBLIC_DOMAIN=你的域名`(见 `.env.prod.example`)。
2. 把该域名的 A/AAAA 记录解析到本机公网 IP;放行入站 **80 与 443**(Caddy 经 80 完成 ACME 验证、443 提供服务)。
3. `docker compose -f docker-compose.prod.yml up -d` —— Caddy 首次启动自动签发并续期 Let's Encrypt 证书,
   证书持久化在 caddy-data 卷(重启不重签,避免触发速率限制)。

`per-IP` 限流按真实客户端 IP 计算:gateway 经 `HUAKAI_TRUSTED_PROXY_CIDRS`(默认本 compose 固定子网)
信任 Caddy 转发的 `X-Forwarded-For`;对非可信来源的 XFF 一律忽略(fail-closed 防伪造)。

> 本地无公网域名联调:`HUAKAI_PUBLIC_DOMAIN=localhost`(Caddy 发本地自签证书,浏览器提示不受信,仅联调)。
> 多级代理上线后若要每个代理自带白标域名,Caddyfile 末尾已预留 on-demand TLS 模板(需配 ask 授权端点,默认不启用)。

## 6. 验证

```bash
docker compose -f docker-compose.prod.yml ps     # gateway/caddy 应 healthy、migrate 应 Exited(0)
# 容器内 gateway 健康(它不再对宿主暴露端口):
docker compose -f docker-compose.prod.yml exec gateway wget -qO- http://127.0.0.1:8080/healthz
# 经 Caddy 的对外 HTTPS(证书签好后):
curl -s https://<你的域名>/healthz               # {"status":"ok"}
```

接着接上游账号池、建 API key、手动 admin 充值,即可跑通 relay。

## 7. 尚未覆盖(部署侧待办,Owner-gated)

- **运维控制台 UI**:尚未实现;当前 API-only。
- **真支付 provider**:可选;手动 admin 充值已可替代。
