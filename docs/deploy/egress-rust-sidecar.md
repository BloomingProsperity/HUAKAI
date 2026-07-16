# 出口 TLS mimicry —— Rust BoringSSL sidecar(默认出口)运维说明

面向部署 / 运维。说明反转号、OAuth 号的**出站 TLS 伪装**默认怎么走、如何应急回退、
dev/CI 如何免起 sidecar。api-key 官方号仍走裸标准 transport(不伪装),不受本文影响。

## 1. 默认出口 = Rust sidecar(生产 fail-closed)

生产模式(`HUAKAI_RELEASE_MODE=production`)下,所有 mimicry 出站默认经 **Rust BoringSSL
tls-sidecar** 逐字节复刻真实客户端 ClientHello(JA3/JA4 + 扩展顺序 + ALPN),而非 Go-native
uTLS。网关经共享卷上的同一 unix socket 把拨号控制帧下发给 sidecar;sidecar 是纯传输层
(只做 TLS 握手伪装 + 代理隧道),**不碰 body**。

- 默认 socket 路径:`/run/huakai/tls-sidecar.sock`(`config.DefaultTransportSidecarSocket`)。
- 生产模式未显式配 socket 时按此默认(代码级默认走 Rust);显式 `HUAKAI_TRANSPORT_SIDECAR_SOCKET`
  永远优先。
- **纯 Rust fail-closed**:`HUAKAI_TRANSPORT_SIDECAR_FALLBACK` 默认 `false` —— sidecar 不可达时
  出口中断(而非静默降级 Go uTLS),使伪装缺陷暴露而非掩盖。

四家伪装出口指纹已内置于 sidecar 二进制的 Rust profile:

| mimicry mode        | Rust profile id        | ALPN 广告      | 备注 |
|---------------------|------------------------|----------------|------|
| Claude Code         | `anthropic-cli-mimicry-v1` | http/1.1   | 反转 Claude 号 |
| ChatGPT / Codex CLI | `openai-codex-cli-v1`  | 不发 ALPN      | codex CLI |
| Gemini Advanced     | `gemini-cli-v1`        | h2, http/1.1  | 广告 h2,实连由上游协商 |
| Kiro                | `kiro-cli-v1`          | 不发 ALPN      | 只对齐 JA4 前缀 + cipher/groups 集合(rustls 源逐请求乱序不复刻) |

antigravity / cursor / copilot / windsurf 暂无真实抓包模板 → 未映射(fail-closed,不臆造指纹)。

## 2. 部署接线(compose 已就绪)

`docker-compose.prod.yml` 与 `docker-compose.direct.yml` 都已加 `tls-sidecar` 服务:

- 与 gateway 共享一个命名卷挂到 `/run/huakai`(socket 落在此)。
- gateway `depends_on` sidecar 的 `service_healthy`(healthcheck = socket 已绑),避免连空 socket。
- gateway env 已配 `HUAKAI_TRANSPORT_SIDECAR_SOCKET=/run/huakai/tls-sidecar.sock`。

sidecar 镜像:`exploratory/rust-core-gateway/merged/crates/tls-sidecar/Dockerfile`(多阶段,从源码
编译 vendored BoringSSL fork)。首次构建较慢(BoringSSL 从源码建)。

起栈即默认全出口经 Rust,无需额外开关:

```
docker compose -f docker-compose.prod.yml up -d      # 域名 + 自动 HTTPS
docker compose -f docker-compose.direct.yml up -d    # 无域名 IP 直连
```

## 3. 应急回退 Go uTLS(break-glass)

若 sidecar 出故障需临时回退到 Go-native uTLS 出口:

```
HUAKAI_TRANSPORT_SIDECAR_FALLBACK=true
```

设为 `true` 后,sidecar 拨号失败会回落 Go uTLS(非强制 sidecar 的 mode)。**默认关**;仅应急用。
排查完成后应改回 `false` 恢复纯 Rust fail-closed。

## 4. dev / CI 免起 sidecar

dev/test 模式(`HUAKAI_RELEASE_MODE` 非 `production`)默认 socket 留空 → 出口回落 Go-native
uTLS,无需起 sidecar 容器。`docker-compose.dev.yml` 无 gateway 服务(手动 `go run`),故未加
sidecar。本机 / CI 因此不会因缺 sidecar 而 fail-closed 断出口。

## 5. Go-native uTLS = 冻结回退路径

`internal/transport/mimicry/{utls_dialer,template,db_profile,proxy_dialer}.go` 与
`registry.go` 的 `NewDefaultTemplateRegistry` 是 Go-native uTLS 出口,现为**非默认回退路径**:
仅 break-glass 或 dev/CI 启用。**新增 / 调整伪装能力应落在 sidecar 的 Rust profile**,不在 Go 侧
扩展。这些文件物理保留(留 break-glass),待真号实测稳定后单独批次退役。
