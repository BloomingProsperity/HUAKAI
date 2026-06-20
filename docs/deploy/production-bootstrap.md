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

## 5. 验证

```bash
curl -s http://127.0.0.1:8080/healthz          # {"status":"ok"}
docker compose -f docker-compose.prod.yml ps    # gateway 应 healthy、migrate 应 Exited(0)
```

接着接上游账号池、建 API key、手动 admin 充值,即可跑通 relay。

## 6. 尚未覆盖(部署侧待办,Owner-gated)

- **TLS / 反向代理**:gateway 仅裸 HTTP `:8080`。对外暴露需在前面放反代(Caddy/nginx/Traefik)终止 TLS;
  自用可先限定 localhost / 内网 / VPN。
- **运维控制台 UI**:尚未实现;当前 API-only。
- **真支付 provider**:可选;手动 admin 充值已可替代。
