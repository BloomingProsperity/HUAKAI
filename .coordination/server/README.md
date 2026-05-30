# 跨机器协调服务 — 部署配置（1 本地 + 2 服务器)

三台机器物理隔离 → 本地文件互不可见。所以在**你提供的那台服务器**上跑一个极小协调服务 `coord_server.py`,另两台(本地 + 另一台服务器)通过**加密私网链路 + 共享 token** 访问它。SQLite `BEGIN IMMEDIATE` 串行化并发 claim → **真原子锁**(两个 AI 同抢一个文件,必有一个被拒),比本地文件版的"建议性广播"更强。

零三方依赖(Python3 stdlib)。下面三步:**①服务端起服务 → ②打通网络 → ③三台都设客户端环境变量**。

---

## ① 服务端(你提供的那台服务器)

```bash
# 1. 放代码
sudo mkdir -p /opt/huakai-coord && sudo useradd -r -s /usr/sbin/nologin coord 2>/dev/null || true
sudo cp .coordination/server/coord_server.py /opt/huakai-coord/
sudo chown -R coord:coord /opt/huakai-coord

# 2. token + 自签证书 + 环境文件
#    注意:EnvironmentFile 只认整行注释,值后面【绝不能】加行内 "# ...",否则会被当成值的一部分,
#    例如 COORD_BIND=127.0.0.1 # x 会让服务拿到非法绑定地址而起不来。
TOKEN=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
# 用绝对路径,不要 cd(否则下面 step 3 的 .coordination/... 相对路径会失效)
sudo openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout /opt/huakai-coord/coord.key -out /opt/huakai-coord/coord.crt -days 3650 \
  -subj "/CN=huakai-coord" -addext "subjectAltName=IP:<本机公网IP>"
sudo chown coord:coord /opt/huakai-coord/coord.crt /opt/huakai-coord/coord.key
sudo chmod 600 /opt/huakai-coord/coord.key && sudo chmod 644 /opt/huakai-coord/coord.crt
sudo tee /etc/huakai-coord.env >/dev/null <<EOF
COORD_TOKEN=$TOKEN
COORD_DB=/opt/huakai-coord/coord.db
COORD_BIND=0.0.0.0
COORD_PORT=8443
COORD_TTL=1800
COORD_TLS_CERT=/opt/huakai-coord/coord.crt
COORD_TLS_KEY=/opt/huakai-coord/coord.key
EOF
sudo chmod 600 /etc/huakai-coord.env
echo "TOKEN(安全发给另两台,别贴聊天): $TOKEN"

# 3. systemd 常驻 + 开防火墙端口
sudo cp .coordination/server/coord-server.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now coord-server
command -v ufw >/dev/null && sudo ufw status | grep -q "Status: active" && sudo ufw allow 8443/tcp || true
curl -sk https://localhost:8443/healthz   # {"ok":true,...}
```

> 上面是**已采用的方案**:公网端口 + TLS(自签证书)+ token。`COORD_BIND=0.0.0.0` `COORD_PORT=8443` `COORD_TLS_*` 三者一起 = 对外 HTTPS;不设 `COORD_TLS_*` 则为明文 HTTP(只适合 127.0.0.1 / 私网绑定)。客户端用 `COORD_URL=https://<公网IP>:8443` + `COORD_CACERT=<coord.crt 路径>` 固定该自签证书。

## ② 打通网络（本部署已用"公网 TLS 端口";以下是不想开公网端口时的替代)

上面 §① 已用"公网 `0.0.0.0:8443` + TLS + token + 证书固定",另两台直接 `COORD_URL=https://<公网IP>:8443` 即可。若你**不想开公网端口**,可改成把 `COORD_BIND` 设回 `127.0.0.1`、去掉 `COORD_TLS_*`,再用下面任一私网通道(此时客户端 URL 用对应的 http/私网地址):

- **A. Tailscale / WireGuard 私网(最省心,推荐)**:三台都装 Tailscale(`curl -fsSL https://tailscale.com/install.sh | sh && sudo tailscale up`)。把服务端 `COORD_BIND` 改成它的 tailscale IP(`tailscale ip -4`,形如 `100.x.x.x`),`systemctl restart coord-server`。流量被 WireGuard 端到端加密,无需 TLS 证书,不暴露公网。客户端 `COORD_URL=http://100.x.x.x:8787`。
- **B. Caddy 反代 + 域名(有域名时)**:服务保持绑 `127.0.0.1:8787`;Caddy 一行 `coord.你的域名 { reverse_proxy 127.0.0.1:8787 }` 自动签 Let's Encrypt。客户端 `COORD_URL=https://coord.你的域名`。
- **C. SSH 反向隧道(无 Tailscale 无域名)**:另两台各自 `ssh -N -L 8787:127.0.0.1:8787 user@服务器` 把服务器端口隧道到本机,客户端 `COORD_URL=http://127.0.0.1:8787`。

> ⚠️ 不要把服务明文 HTTP 直接绑 `0.0.0.0` 暴露公网。token 是应用层防护,传输层加密靠 A/B/C 之一。

## ③ 三台机器各自设客户端环境变量

在每台 AI 的 shell 环境(`~/.bashrc` 或启动 AI 的 wrapper)里:

```bash
export COORD_URL="http://100.x.x.x:8787"   # 按 ② 的方式填
export COORD_TOKEN="<服务端那个 TOKEN>"
export COORD_AGENT="local-claude"          # 每台不同标识,如 local-claude / server2-codex / server3-gemini
```

之后**所有 AI 用同一套命令**(脚本自动走远程):

```bash
bash .coordination/check.sh                                   # 看全局看板(三台共享)
bash .coordination/check.sh backend/internal/billing/x.go     # 查某文件冲突
bash .coordination/claim.sh "$COORD_AGENT" "fileA,fileB" "核心功能" "目的"   # 冲突→exit2 拒绝
bash .coordination/release.sh "$COORD_AGENT"                  # 改完释放
```

`COORD_URL` 没设 = 自动退回本地文件模式(单机/同工作树仍可用)。设了 = 三台共享一个实时看板。协议(改文件前必 check+claim)已写进仓库 `AGENTS.md` / `CLAUDE.md`,三家 AI 都遵守。

## 与本地版的关系（迁移)

- 客户端命令、协议、AGENTS.md/CLAUDE.md 全不变;只是多设 `COORD_URL`+`COORD_TOKEN` 就从"本地文件"升级成"跨机器真锁"。
- 心跳:长编辑期周期重跑 `claim.sh` 刷新(超 `COORD_TTL` 秒无心跳的锁自动过期,防死会话占锁)。
- 备份:`coord.db` 是 SQLite 单文件,丢了只是丢当前活锁(无历史价值),不影响代码。

## 验证（服务端起来后)

```bash
# A 抢文件
COORD_URL=$U COORD_TOKEN=$T bash .coordination/claim.sh A "x.go" feat purpose   # ✓
# B 抢同文件 → 必被拒(exit 2)
COORD_URL=$U COORD_TOKEN=$T bash .coordination/claim.sh B "x.go" feat purpose   # ⚠️ REFUSED
# 看板
COORD_URL=$U COORD_TOKEN=$T bash .coordination/check.sh
# 无 token → 401
curl -s -o /dev/null -w "%{http_code}\n" $U/board
```
