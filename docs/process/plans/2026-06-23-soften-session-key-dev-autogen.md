# 计划:session 签名 key —— 非生产模式自动生成临时 key(Owner 拍:dev-only ephemeral)

- 日期:2026-06-23
- 切片:soften/session-key-dev-autogen(四条软化之④)
- 基线:origin/feat/frontend-portal @9bc18adb
- Owner 决策:AskUserQuestion 选"Dev/test 临时自动生成(推荐)"

## 背景与决策经过

四条软化之④原拟"缺省自动生成 + 持久化"(学 sub2)。亲核真码发现:
- session key 现状在**所有模式都强制**(`backend/cmd/gateway/config.go:252` required;`lifecycle.go:330` 无条件调用)。
- 计划设想的免 schema 落点 `platform_settings` 被其**明文不变量挡死**:迁移 0077 头部注释"Credential material is never stored here... only the explicit non-secret allow-list"——禁存密钥、且只收白名单项。
- credential storage 绑账号/vendor handler,系统密钥不合适。
- 故"持久化"只能新建专用密钥表 = 新 schema(Owner-gated)+ 密钥持久化风险,却只省一个已文档化的 env 变量(`.env` 已给 `openssl rand -base64 32`)。值/险比最差。

向 Owner surface 实况 + 三镜对照(sub2=持久化到 DB / new-api=随机不持久化重启登出 / CLIProxy=无此概念)。**Owner 选 dev-only 临时自动生成**:零 schema、零密钥持久化风险,只省本地开发摩擦,production 安全姿态不变。

## 设计(最小改动)

改 `loadSessionSigningKey`:显式 B64/HEX 配置路径完全不动(优先生效)。仅末尾"无配置"分支改为 release-mode 感知:
- production → 仍 `fail-loud` 拒启(行为零变化)。
- 非生产(dev/development/test)→ `crypto/rand` 生成 32 字节临时 key + `logger.Warn` 提示"重启即换、会话失效,如需稳定显式设 key"。

签名加 `logger *zap.Logger`(唯一调用点 lifecycle.go:330 传 logger)。

## 默认行为变化

- production:完全不变(显式 key 必填,缺则拒启)。
- 非生产:从"缺 key 拒启"变"缺 key 自动生成临时 key"——纯本地开发便利。
- 任何模式显式配 key 都优先原样采用(逃生舱 = 设 key 即旧行为)。

## blast radius / 风险

- 改动限 `loadSessionSigningKey` 末尾分支 + 一个调用点;不碰 session 验签/签发逻辑、无 schema、无新依赖。
- 安全:**production 绝不自动生成**(本切片核心护栏),已用判别测试钉死——删 production 分支则"production 缺 key"用例 RED。临时 key 用 crypto/rand(安全随机)。

## 测试

`session_key_test.go`:① production 缺 key → error(护栏);② dev 缺 key → 32 字节 key 无 error;③ 显式 key 在 production 下优先原样采用。变异(删 production 分支)→ ① RED,实测通过。
