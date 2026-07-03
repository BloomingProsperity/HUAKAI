# HUAKAI 服务器迁移引导(2026-07-03)

旧服务器一切已推 GitHub(`BloomingProsperity/HUAKAI`),零丢失。新服务器按本文件一键接续。

## 一、新服务器起手(复制粘贴即可)

```bash
# 0. 依赖(若缺):Go 1.23+、git、postgresql-client、rustup(Rust sidecar 用)
#    Go: 装到 /usr/local/go,PATH 加 /usr/local/go/bin

# 1. 克隆仓库(主线分支)
cd /home/ubuntu
git clone https://github.com/BloomingProsperity/HUAKAI.git
cd HUAKAI
git checkout feat/fe-wire-users-mod    # ← 当前主线(非 main;main 落后 251)

# 2. 三镜(clean-room 调研用,§16)——重拉即可,不用从旧机搬
mkdir -p /home/ubuntu/refs
git clone --depth=1 https://github.com/Wei-Shaw/sub2api.git          /home/ubuntu/refs/sub2api
git clone --depth=1 https://github.com/QuantumNous/new-api.git       /home/ubuntu/refs/new-api
git clone --depth=1 https://github.com/router-for-me/CLIProxyAPI.git /home/ubuntu/refs/CLIProxyAPI

# 3. 恢复 Claude 记忆(关键!不在 git 里,必须从旧机 scp 过来)
#    旧机文件:/home/ubuntu/claude-memory-backup-20260703.tar.gz
scp <旧机>:/home/ubuntu/claude-memory-backup-20260703.tar.gz /home/ubuntu/
mkdir -p /home/ubuntu/.claude/projects/-home-ubuntu
tar -xzf /home/ubuntu/claude-memory-backup-20260703.tar.gz -C /home/ubuntu/.claude/projects/-home-ubuntu/
#    验证:ls /home/ubuntu/.claude/projects/-home-ubuntu/memory/MEMORY.md

# 4. 真实密钥/env(不在 git,Owner 手动带):
#    - Grok/DeepSeek 官key、其它上游 key
#    - 生产 backend/.env(DB 密码/audit 私钥/admin bootstrap token)
#    模板见 feat/prod-deploy-bundle 分支的 backend/.env.prod.example

# 5. 构建验证
cd /home/ubuntu/HUAKAI/backend
export PATH=/usr/local/go/bin:$PATH GOFLAGS=-buildvcs=false
go build ./... && echo BUILD_OK

# 6. 启动 Claude Code,第一句告诉它:
#    "读 CLAUDE.md + 记忆,继续 /loop 推进(当前:片D slog门面审查中→E/F/G/H)"
```

## 二、当前进度快照(新环境的 Claude 接手点)

主线 `feat/fe-wire-users-mod` @426fb3c0,已合并且推送:
- 官key(Grok/DeepSeek/Kimi + 9 国内厂 vendor)、F4 视觉修复
- billing Serializable 重试(L1)、quota reconciler 跨租户 sweep(②)
- **auth 黑洞车道(缺口①)**——重审 26 条全修、5 变异全红
- **日志片 A+B**——billing/quota worker 处理量/失败可观测
- §17 模块配合规则 + relay 运行逻辑文档 + 日志体系调研计划

进行中(代码已上 GitHub `feat/slog-facade` @8eff5637,未合并):
- **日志片 D**:slog 门面统一 + /loglevel 联动两栈(修 S1 双栈割裂)——对抗审查被换机中断,新环境**重跑审查**:worktree add 该分支 → 发 4 维对抗审查 workflow → 零 S0/S1 后 rebase 到主 HEAD + 重跑门禁 → ff 合并。

待办队列(日志计划 docs/process/plans/2026-07-02-logging-observability-plan-claude.md):
- 片 E channelhealth 状态转换镜像 slog(S3,小,internal/channelhealth/service.go emitTransitionEvents)
- 片 F settlementrecovery/DLQ 补偿可见(S2)
- 片 G 全链 request_id + trace_id 填充(跨模块)
- 片 H slog 采样 + 脱敏单一真相源 + CI 脱敏断言
- /loop 上游扩展:Gemini 换 AIza key(待 Owner 给)、切片 C Veo/Sora 视频 adaptor

## 三、GitHub 上的兜底(任何分支都能找回)

- `feat/fe-wire-users-mod` = 主线(**不是 main**)
- `feat/slog-facade` = 片 D 代码
- `rescue/maincheckout-wip-20260703` = 旧主检出未提交工作(landing 页+codex 文档,非本人工作,待 Owner 定夺)
- `feat/prod-deploy-bundle` = 部署文件(占位密钥,无真密钥)
- `backup/srv-20260703/*` = **全部 ~100 本地分支的远端镜像**(含 main/全历史切片/rescue-350files)

## 四、关键规则提醒(CLAUDE.md 全文,新环境自动加载)

- 全中文(回复/注释/commit/文档/subagent 指令)
- 每片:worktree(基底=最新主 HEAD)→ §16 三镜 → 计划 → build/vet → §14 变异测试 → 对抗审查零 S0/S1 → 干净基线 → commit → push
- Owner-gated:money/quota/schema/auth-core/deploy/默认翻转
- worktree 陈旧基底 footgun:派 worktree 子 agent 必令基于最新主 HEAD;合并前独立核基底
- shell wrapper 间歇吞输出:关键操作后用 python-to-file+Read 独立核实真实 git 状态
