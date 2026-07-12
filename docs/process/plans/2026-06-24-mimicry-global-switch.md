# 计划:出站 TLS 指纹伪装 全局一键关运维开关(默认开)

- 日期:2026-06-24
- 切片:③ 伪装全局开关(本批尾债之一,Owner 已在选择题明确批"做·真接线全局开关")
- 分支:feat/mimicry-global-switch(基 origin/feat/frontend-portal @ 7f1ec926)

## 背景与三镜对照(§16)

- **sub2api**:有按账号真开关(`account.go` 的 `IsTLSFingerprintEnabled` → `ResolveTLSProfile`
  返回 nil → `DoWithTLS` 走普通 transport);号称的全局开关 `gateway.tls_fingerprint.enabled`
  是**死字段**(全仓无消费者)。默认关。
- **new-api**:完全不做 TLS 伪装,无此概念。
- **CLIProxyAPI**:对 anthropic/chatgpt **永远开、无任何开关**(`HelloChrome_Auto` 写死)。

→ 三家**没有一家有"全局一键关"的真开关**(sub2api 的全局开关是 inert)。HUAKAI 现状也是
伪装默认开、无关闭开关。本切片 = 补一个**真接线**的全局开关,**默认开不改现有行为**,
关闭即一键回退标准 net/http(排障 / 伪装本身故障时)。这是 HUAKAI 自有的生态升级
(运维可观测 / 可回退),不照搬任一参考外壳。

## Scope(改动面)

1. 新文件 `backend/internal/transport/mimicry_switch.go`:
   - `MimicryEnabled() bool` 读 `HUAKAI_TRANSPORT_MIMICRY`,`!= "false"` 默认开
     (与 `forceH1Enabled` 同款就地读 env 惯例,避免反向 import config)。
   - `(TransportMode).isMimicry()` 判 8 个 `mimicry_*` mode。
2. 落点 A `backend/internal/transport/factory.go` `For()`:`ValidateModeForProvider` 之后、
   `switch` 之前加 `if mode.isMimicry() && !MimicryEnabled() { mode = TransportModeStandard }`。
   覆盖 dispatcher×2 + OAuth 全部出站路径(三者都经 `For`)。
3. 落点 B `backend/internal/gateway/upstream_dispatcher.go` `applyTLSProfile` gate 追加
   `|| !transport.MimicryEnabled()`,堵 DB profile 旁路(它不经 `For` 单独构造 uTLS RT)。

## Success Criteria

- 默认(env 未设)`For(Anthropic, mimicry_claude_code)` 仍返回 uTLS RT(非 `*http.Transport`)——行为不变。
- 设 `HUAKAI_TRANSPORT_MIMICRY=false` 后,同调用返回 `*http.Transport`(标准回退);
  `applyTLSProfile` 即便有 resolver+accountID+mimicry mode 也返回原 rt 不套 profile。
- 全部判别性测试经变异验证:翻条件/删 guard 必转红。
- `go build ./... && go vet`、相关包单测、codebudget 守卫全绿。

## Blast Radius

- transport 包(非 §6 碰撞)+ gateway 包一行 gate(§6 碰撞包,已核 proxies 活跃分支
  真实改动 backend=0,无真碰撞)。
- 默认开 → 现有生产流量零行为变化。只有运维显式设 `=false` 才改变行为。

## 风险与对策

- **掩盖配置错误**?降级放 Validate 之后,校验仍按真实 (provider, mode);降级目标 standard
  对每个含 mimicry 的 provider 必然合法(矩阵已逐行核)。不掩盖任何合法性校验。
- **留死角**?三出站路径(dispatcher/HCSF/OAuth)全经 `For`(落点 A 覆盖);DB profile
  旁路单独由落点 B 覆盖。无第四条路径(已核 sidecar 也经 `For`)。

## Owner 决策点

- 这是 §0/§2 **default-behavior flip 的开关**(虽默认不变行为,但引入改默认能力)→ Owner-gated。
  Owner 已在选择题选"做·真接线全局开关(推荐)",即已 surface 批准。默认开保证不改现有行为。

## 门禁(codex 401 → 对抗 verifier 替代)

变异证 + 通用 agent 对抗审查(0 S0/S1)+ 干净基线 `-count=1`。
