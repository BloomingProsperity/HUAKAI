# 生产部署与首启引导(自用 / API-only)

本文给出用 `backend/docker-compose.prod.yml` 把 HUAKAI 在生产模式真正起栈跑通的步骤。
当前运维控制台 UI 尚未实现,**自用上线 = API-only**(用 bootstrap 管理员令牌 + admin API 管理),
手动 admin 充值已可替代真支付,真支付 provider 是可选增强而非上线前置。

> 说明:本文是运维对照,涉及 deploy/prod 改动按仓库规则属 Owner-gated。

## 0. 部署形态选择(域名 / 无域名,两选一,运维自决)

HUAKAI **不强制有域名**。我们提供两种平级形态,你自己选——我们不替你做决定:

| 形态 | compose / env | 对外协议 | 适用 | TLS |
|---|---|---|---|---|
| **A. 域名 + 自动 HTTPS** | `docker-compose.prod.yml` + `.env.prod.example` | HTTPS(Caddy 自动签发 Let's Encrypt) | 对公网卖额度给陌生人,零证书运维 | 内置 Caddy 自动签发/续期 |
| **B. 无域名 / IP 直连** | `docker-compose.direct.yml` + `.env.direct.example` | 默认明文 HTTP | 自测 / 内网,或运维自带反代(nginx/LB/宝塔)在前面终结 TLS | 由运维自理(自带反代,或纯内网明文) |

> ⚠ **安全权衡(选 B 必读)**:无域名 = 纯 HTTP,意味着 `hk_key` 在网络上**明文传输**,可被同网段 /
> 运营商 / 公共 Wi-Fi 嗅探盗用。
> - 自测 / 内网(可信网络):形态 B 直接用。
> - **对公网卖额度**:要么用形态 A(域名 + 自动 HTTPS),要么形态 B 前面**自己加一层 TLS**
>   (你的 nginx / Caddy / LB / 宝塔 + 证书)。**不要"无域名 + 纯 HTTP + 对公网卖额度"裸跑。**
>
> 本文 §1–§6 讲形态 A(域名)的完整首启;形态 B(无域名)见 §7。两者首启的密钥 / 迁移 / email 门逻辑一致,
> 区别只在"前面有没有 Caddy / 要不要域名"。

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

1. **数据库迁移**:gateway 默认不自管迁移;prod compose 的 `migrate` 一次性服务会在 gateway 之前把
   `sql/migrations` 应用到空库。漏了迁移,gateway 会因缺表崩溃。
   > 裸二进制单实例(不走 compose)可设 `HUAKAI_AUTO_MIGRATE=true`,让 gateway 启动时进程内自迁移,
   > 省去手动跑迁移这一步(与 compose one-shot 共用同一张 `schema_migrations`、幂等、advisory lock 防竞态);
   > 多副本部署仍建议保持默认关、由 one-shot 受控迁移。
2. **审计**:要求 `HUAKAI_AUDIT_LEDGER_BACKEND=postgres` + `HUAKAI_AUDIT_PRIVATE_KEY_PATH` 指向有效
   ed25519 私钥(上面已生成并由 compose 挂载)。
3. **email 就绪门(默认已软化,不再拦启动)**:从 2026-06-23 起,production 默认**不再因租户未配 SMTP
   而拒启**——对齐成熟中转站的"请求时惰性"做法:未配 SMTP 的租户,其验证邮件功能在请求时惰性返回错误,
   注册按"验证关闭"放行(用户直接 active)。门校验仍会跑,但只 warn 提示。若想恢复"每个 active 租户必须
   配齐 SMTP 且开启邮件验证才放行启动"的旧严格行为,设 `HUAKAI_REQUIRE_EMAIL_GATE=true`。
   故首启只剩**迁移**与**审计**两道硬门。

## 4. (可选)配置 SMTP 以启用邮箱验证 / 重置邮件

email 门已软化,**production 可直接起,不必先配 SMTP**。仅当你要启用邮箱验证、密码重置邮件等功能,或
显式设了 `HUAKAI_REQUIRE_EMAIL_GATE=true` 时,才需给工作租户配 SMTP。配置经 admin API(需 gateway 已在跑):

```bash
# 用 bootstrap 令牌给工作租户 id=1 配 SMTP(开启邮件验证)
#   <BOOTSTRAP> = .env 里的 HUAKAI_ADMIN_BOOTSTRAP_TOKEN
curl -sS -X PUT http://127.0.0.1:8080/v1/admin/email/settings \
  -H "Authorization: Bearer <BOOTSTRAP>" -H "Content-Type: application/json" \
  -d '{"tenant_id":1,"smtp_host":"smtp.your-provider.example","smtp_port":587,
       "smtp_username":"you@your-domain.example","smtp_password":"<SMTP-PASS>",
       "smtp_from":"no-reply@your-domain.example","smtp_from_name":"HUAKAI",
       "smtp_use_tls":true,"email_verify_enabled":true}'
```

> 若设了 `HUAKAI_REQUIRE_EMAIL_GATE=true`,则需先以 `HUAKAI_RELEASE_MODE=dev` 起一次配齐上面的 SMTP,
> 再切回 production(否则严格门不过)——这正是软化前的旧"鸡生蛋"序列,现已非默认。

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

## 7. 无域名 / IP 直连形态(形态 B)

用 `docker-compose.direct.yml`,不含 Caddy、不需要域名,gateway 直接发布到宿主端口,用「服务器IP:端口」访问。

```bash
cd backend
cp .env.direct.example .env
# 按 .env 顶部注释生成:CREDENTIAL_KEY / SESSION_SIGNING_KEY / ADMIN_BOOTSTRAP_TOKEN / POSTGRES_PASSWORD
# 选 HUAKAI_RELEASE_MODE:dev(自测最省事)或 production(对外硬化,需补审计私钥,见 .env 注释)
docker compose -f docker-compose.direct.yml up -d
```

- **自测 / 内网(dev)**:设 `HUAKAI_RELEASE_MODE=dev` 即可,审计走内存 + 临时签名密钥,**无需任何私钥文件**,
  一条命令起栈。访问 `http://<服务器IP>:${HUAKAI_HTTP_PORT:-8080}`。
- **对外但自带反代(production)**:设 `HUAKAI_RELEASE_MODE=production`,按 `.env.direct.example` 的"对外硬化"段
  生成审计私钥、设齐三个 `HUAKAI_AUDIT_*`(BACKEND=postgres / PATH=容器内读取路径 / HOST=宿主源路径)、取消 compose 里
  gateway 的 volumes 审计挂载段注释;email 门首启序列同 §4(把命令里的 compose 文件换成 `docker-compose.direct.yml`)。
  TLS 由你前置的 nginx / LB / 宝塔 终结。
- **绑定网卡**:`HUAKAI_HTTP_BIND=127.0.0.1` 可让 gateway 只绑本机(同机另起反代回源时用);默认 `0.0.0.0` 对外可达。

验证:

```bash
docker compose -f docker-compose.direct.yml ps          # gateway 应 healthy、migrate 应 Exited(0)
curl -s http://127.0.0.1:${HUAKAI_HTTP_PORT:-8080}/healthz   # {"status":"ok"}
```

## 8. 尚未覆盖(部署侧待办,Owner-gated)

- **运维控制台 UI**:尚未实现;当前 API-only。
- **真支付 provider**:可选;手动 admin 充值已可替代。
