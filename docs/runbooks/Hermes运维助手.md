# Hermes 运维助手手册

## 1. 定位

Hermes 使用官方 Runner。HUAKAI 不自研它的推理循环，只负责：

- 部署者和租户管理员身份。
- 租户 scope 与 `hermes_operations` capability。
- 内部 MCP 工具。
- 只读诊断、修改提议、人工确认和恢复。
- 外部 OpenAI 兼容 URL + Key 的加密 profile。
- Runner 网络、资源和秘密隔离。

最终用户无 Hermes 入口。

## 2. 启用

以下每个命令块都从仓库根目录独立执行。准备密钥：

```bash
cd backend
./deploy/hermes-runner/generate-keypair.sh ./secrets/hermes
openssl rand -hex 32
```

把 `openssl rand` 输出的独立随机值和以下配置写入生产 `.env`：

```text
HUAKAI_HERMES_RUNNER_URL=http://hermes-runner:8801
HUAKAI_HERMES_KEYS_HOST_DIR=./secrets/hermes
HUAKAI_HERMES_JWT_KID=compose-hermes
HUAKAI_HERMES_INTERNAL_TOKEN_SECRET=<独立随机值>
```

密钥脚本在目标文件已存在时会拒绝覆盖。轮换必须使用新目录并完成 Gateway 与 Runner 的成套切换，
不能只替换一侧。

启动：

```bash
cd backend
docker compose -f docker-compose.prod.yml --profile hermes up -d
docker compose -f docker-compose.prod.yml --profile hermes ps
```

Runner 没有宿主端口、PostgreSQL 或 Redis 网络访问；外部模型连接必须经过 `hermes-egress`。
下面两条命令分别验证 Runner 内部健康和 Gateway 对外就绪：

```bash
docker compose -f docker-compose.prod.yml --profile hermes exec -T hermes-runner \
  python3 -c "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:8801/healthz', timeout=2).read().decode())"
curl -fsS https://<域名>/readyz
```

## 3. 权限

- 部署者只操作平台工作租户的 Hermes。
- 租户管理员必须由部署者授予 `hermes_operations`，并只操作自身租户。
- 最终用户禁止访问。
- 内部 MCP JWT 必须绑定租户、操作者、会话、工具和有效期。
- 租户管理员不得通过工具读取平台宿主、其他租户、全局 DLQ 或跨租户资金。

## 4. 模型配置

模型侧接受任意有效的 OpenAI 兼容 URL + Key，不要求一定指向 HUAKAI Gateway。
这些值属于 Hermes profile，不是 HUAKAI 用户 Key 规则。保存时加密，读取时不回显明文。

修改 URL 时必须执行 SSRF 校验；Runner 的实际外连还要经过 egress 代理重新解析并拒绝私网、
回环、链路本地、保留地址和混合 DNS 结果。

当前管理入口：

```text
GET    /v1/hermes/settings
POST   /v1/hermes/settings/enable
POST   /v1/hermes/settings/disable
GET    /v1/hermes/api-profiles
POST   /v1/hermes/api-profiles
GET    /v1/hermes/api-profiles/{id}
PUT    /v1/hermes/api-profiles/{id}
DELETE /v1/hermes/api-profiles/{id}
```

启用顺序是：先保存当前租户的模型 profile，再启用 Hermes 设置，最后发起最小 chat 验证。租户管理员
只能看到和修改自己的 profile；部署者只能使用平台工作租户，不能在请求中指定其他租户。

## 5. 工具与修改

只读工具可以查询本租户的日志、账号健康、池组、用量和恢复状态。修改工具必须：

```text
提议
 -> 返回影响对象和摘要
 -> 人工确认
 -> 校验摘要、租户、操作者和有效期
 -> 执行事务
 -> 写日志
 -> 成功或 recovery_pending
```

确认不能只放进单实例内存；当前已使用数据库共享确认事实。高风险修改仍需参数限制、速率限制和
幂等。Hermes 不得直接执行任意 SQL、shell 或容器命令。

## 6. 故障恢复

| 状态 | 处置 |
| --- | --- |
| Runner 不健康 | 停止 Hermes profile，不影响非 Hermes Gateway 主链 |
| 模型 URL/Key 失败 | 修复租户 profile，不能借用其他租户配置 |
| MCP 401/403 | 检查 JWT KID、时钟、租户、capability 和工具授权 |
| proposal 过期 | 重新生成提议，不能强行复用旧确认 |
| `recovery_pending` | 查询 `hermes_mutation_recovery`，确认业务效果后再重放 |
| 外连被 egress 拒绝 | 检查目标是否私网、DNS 混合或重定向，不关闭 SSRF 守卫 |

租户管理员只能恢复本租户 Hermes 动作；全局 DLQ 重放和平台恢复只允许部署者。

Compose 未显式覆盖时默认开启 Hermes 提议。生产环境可以设置
`HUAKAI_HERMES_LLM_PROPOSE_ENABLED=false` 暂停提议；该设置不影响非 Hermes Gateway 主链。
