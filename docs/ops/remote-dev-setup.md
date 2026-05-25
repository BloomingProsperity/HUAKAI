# HUAKAI 远程开发环境（GCP Linux Server）接入指南

> 本文档面向：从一台**新 Windows 机子**接入 HUAKAI 已搭好的 GCP Linux 开发服务器，用 VSCode Remote-SSH 操作 Claude Code / Codex CLI 完成项目工作。
>
> **背景**：HUAKAI 主开发环境已迁到 GCP server（Ubuntu 26.04，Go 1.25 / PostgreSQL 16 / Claude Code / Codex CLI 全装齐），通过 SS 代理走 UK 出口规避地理风控。新接入只需在本机配 SSH key 即可。

---

## 一、服务器现状（不需要改动，仅供了解）

| 项 | 值 |
|---|---|
| 主机 | `34.121.113.77` (GCP us-central1) |
| OS | Ubuntu 26.04 LTS |
| 用户 | `codex` (~uid 1004) |
| 项目路径 | `~/HUAKAI` (= `/home/codex/HUAKAI`) |
| Git remote | `git@github.com:BloomingProsperity/HUAKAI.git`（已配 deploy key，可双向 push/pull）|
| 工具链 | Go 1.25.0 / PostgreSQL 16.13 / Node 22 / golang-migrate v4.18.1 / tmux / Claude Code 2.1.x / Codex CLI 0.128.x |
| DB | `huakai_dev`（user `huakai` / pass `dev_local_only`），12 个 migration 全应用 |
| 出站代理 | shadowsocks-libev (UK 节点) → privoxy(127.0.0.1:8118) HTTP 桥接 |
| 环境变量 | `~/.bashrc` 已 export `ALL_PROXY=http://127.0.0.1:8118` 等 |
| Claude / Codex auth | 已 OAuth 完成，credentials 落 `~/.claude/.credentials.json` + `~/.codex/auth.json` |
| Session 历史 | `~/.claude/projects/-home-codex-HUAKAI/04d37436-9b8b-4a8e-b2c4-24538cfd6f23.jsonl`（81MB，迁移期间从 Win 拷贝过来）+ `memory/` 目录（feedback / project / reference 全 MD 记忆）|

---

## 二、新 Win 机子接入（一次性，~10 分钟）

### Step 1：装 VSCode + Remote-SSH 扩展

1. 装 VSCode：https://code.visualstudio.com
2. VSCode 里 `Ctrl+Shift+X` → 搜 `Remote - SSH` → 装 **Microsoft** 出的那个

### Step 2：拷贝 SSH 私钥

把这两个文件从原机子拷到新机子相同位置（`C:\Users\<用户>\.ssh\`）：

- `codex_gcp`（私钥，**敏感**）
- `codex_gcp.pub`（公钥）

设私钥权限只本人可读：

```powershell
icacls C:\Users\<你>\.ssh\codex_gcp /inheritance:r /grant:r "%USERNAME%:R"
```

### Step 3：配 `~/.ssh/config`

文件 `C:\Users\<用户>\.ssh\config`（无后缀，不存在就新建）末尾加：

```sshconfig
Host gcp-codex
    HostName 34.121.113.77
    User codex
    IdentityFile C:\Users\<你>\.ssh\codex_gcp
    Port 22
```

### Step 4：验 SSH

任意 terminal（PowerShell / Git Bash）：

```
ssh gcp-codex
```

看到提示符 `codex@instance-...:~$` = 成功。`exit` 退出。

### Step 5：VSCode Remote-SSH 连入

1. VSCode 按 `F1` → 输 `Remote-SSH: Connect to Host` → 选 `gcp-codex`
2. **首次问 OS 类型**：选 **Linux**（记住后下次不再问）
3. 等 vscode-server 在 server 上自动检测/装（已装过的版本秒连；新版本约 30-60 秒下载 ~100MB）
4. 左下角变**绿色 `SSH: gcp-codex`** 表示连上

### Step 6：打开 HUAKAI 项目

`File → Open Folder` → 输入 `/home/codex/HUAKAI` → 回车

左侧文件树出现 `backend/` `docs/` `tools/` 等 → 在 server 端工作的 ready 状态

---

## 三、用 Claude / Codex

### 方式 A：VSCode 侧边栏（推荐 GUI 体验）

- 右上角点 **CLAUDE CODE** 标签 → 新对话
- 新会话第一句给上下文：
  ```
  读 docs/process/plans/2026-05-07-bedrock-eventstream-claude.md +
  ~/.claude/projects/-home-codex-HUAKAI/memory/MEMORY.md，
  按 HUAKAI 项目正在做的事接着干。
  ```
- Codex 同样在右边 **CODEX** 标签

### 方式 B：Terminal CLI（带历史接力）

- VSCode 里 `Ctrl+\`` 开 terminal（在 server bash）
- 跑：
  ```bash
  cd ~/HUAKAI && claude --resume 04d37436-9b8b-4a8e-b2c4-24538cfd6f23
  ```
- 这条命令载入完整 81MB 对话历史 + 所有 memory，对它说"继续"它知道接哪步

### 方式 C：tmux 持久化（断网/关 VSCode 不丢上下文）

```bash
# 第一次进
tmux new -s huakai
cd ~/HUAKAI && claude --resume 04d37436-9b8b-4a8e-b2c4-24538cfd6f23

# 离开（claude 仍在 server 上跑）
Ctrl+B 然后 D

# 重新接回
tmux attach -t huakai
```

---

## 四、关键设计：为什么这样搭

### 1. 为什么走 SS 代理而不是直连？

GCP server 出站 IP 是 us-central1（美国 Iowa），但 Owner 平时所有 Anthropic / OpenAI 操作都从 UK BT 家庭网络（`195.171.187.32`）发出。直连会让 Anthropic / OpenAI 看到"账号突然从美国 IP 操作"——大概率触发地理风控审查甚至封号。

走 shadowsocks-libev → UK 节点 → privoxy（HTTP 桥），所有 `api.anthropic.com` / `chatgpt.com` 请求看到的 client IP 仍是 `195.171.187.32`，与本机操作完全一致，零风控差异。

验证当前出口：

```bash
curl -s https://api.ipify.org      # 应输出 195.171.187.32
```

### 2. 为什么用 privoxy 中转？

Claude Code（Node.js fetch / undici）只支持 `http://` / `https://` 形态的 proxy env vars，不支持 `socks5h://`。privoxy 监听 `127.0.0.1:8118` 把 HTTP 请求转给 SS 的 SOCKS5 端口 `127.0.0.1:1080`，桥起来。

### 3. 为什么 SCP 凭据不够、必须重新 OAuth？

Win 上 Claude Code 的 `.credentials.json` 包含 device-bound token / Windows Credential Manager 引用，跨机器复制后 token 会被 Anthropic 端拒绝（OAuth error 400）。**SS 装好后从 server 端跑 `claude login`，OAuth 流程从 UK IP 出去，这是干净的"新 device 在常用地点 login"**，不触发风控。

Codex 的 `auth.json` 是 token-based 可跨机迁移（实测成功）。

### 4. 为什么 OAuth 回调 localhost 会失败？

Claude CLI 的 `Anthropic Console` 登录路径开 `localhost:33413` 等回调端口，但**那是 server 的 localhost**，Win 浏览器到不了。**必须选 "Claude.ai Subscription" 路径**——它走 `platform.claude.com` 显示 code 让你手动粘贴回 CLI，不需要回调。

---

## 五、卡点排查（Common Issues）

| 现象 | 原因 | 解 |
|---|---|---|
| `Initializing VS Code Server` 卡 >2 分钟 | vscode-server 自动装挂了（CDN 慢/网络问题）| 见下方"手动装 vscode-server" |
| OAuth `Failed to connect to localhost` | 选错登录路径 | 退回 `claude` 主菜单选 "Claude.ai Subscription"（粘贴 code 模式）|
| API 调用 `UnsupportedProxyProtocol` | env var 用了 `socks5h://` | 改 `http://127.0.0.1:8118`（已配进 ~/.bashrc）|
| `curl https://api.ipify.org` 返回 `34.121.113.77` 而不是 UK IP | privoxy / SS 没起 | `sudo systemctl restart shadowsocks-libev-local@uk privoxy` |
| Claude 启动报 OAuth 400 | 旧 credentials 文件污染 | `rm ~/.claude/.credentials.json && claude` 重 login |
| tmux session 断了 | 正常 | `tmux attach -t huakai` 接回；不存在就 `tmux new -s huakai` |

### 手动装 vscode-server（如果自动装挂）

```bash
# 在 server 上跑
COMMIT=$(curl -s "https://update.code.visualstudio.com/api/commit/stable/server-linux-x64/latest" | grep -oP '(?<="version":")[^"]+' | head -1)
DEST=$HOME/.vscode-server/cli/servers/Stable-$COMMIT/server
mkdir -p $DEST
curl -fsSL "https://update.code.visualstudio.com/commit:$COMMIT/server-linux-x64/stable" -o /tmp/vscode-server.tar.gz
tar xzf /tmp/vscode-server.tar.gz --strip-components=1 -C $DEST
$DEST/bin/code-server --version    # 验证
```

完成后 VSCode 重连即可，会自动检测到已装的 server 跳过下载。

---

## 六、日常工作流速记

```bash
# 一进 server 检查环境就绪
cd ~/HUAKAI
git status                     # 工作树干净
git log -1 --oneline           # 最新 commit 知道在哪
curl -s https://api.ipify.org  # 应是 UK IP
psql $HUAKAI_DATABASE_URL -c '\dt' | tail -3  # DB 通

# Bedrock 等下一步任务
# 上次离开点：见 docs/process/plans/2026-05-07-bedrock-eventstream-claude.md
# Owner 待回 R1 / R2 / R4 决策
```

---

## 七、安全提示

- **GitHub 公钥访问**：server 上的 ed25519 deploy key（`~/.ssh/id_ed25519.pub`）已加到 BloomingProsperity/HUAKAI deploy keys（write 权限）
- **SS 凭据**：`/etc/shadowsocks-libev/uk.json` 里有节点密码，文件权限 644（systemd DynamicUser 需读）。如果 server 多人用要换严
- **DB 密码** `dev_local_only` 是占位 dev 密码，仅本机环回（5432 仅 localhost），不暴露公网
- **codex / claude credentials** 在 `~/.codex/` `~/.claude/`，权限 600

---

## 八、相关文档索引

- 项目 brief：`docs/00_PM_OPERATING_SYSTEM.md`
- 上次工作进展：`docs/process/plans/2026-05-07-bedrock-eventstream-{claude,codex}.md`（Bedrock #2 决策点 R1/R2/R4 等 Owner 回）
- Memory（持久反馈/规则）：`~/.claude/projects/-home-codex-HUAKAI/memory/MEMORY.md`
- CLAUDE.md（agent 行为规约）：项目根 `CLAUDE.md`
