# Rust sidecar 代理穿透(②-3 CONNECT/SOCKS5)实现计划

日期:2026-06-24
分支:feat/rust-egress-proxy-tunnel(off origin/feat/frontend-portal)

## 范围(scope)
解 sidecar×账号级代理硬阻塞:当前 `WrapTransportWithProxy` 对 sidecar RT 走 fail-loud
(ErrProxyUnsupportedTransport),绑代理的账号根本用不了 sidecar。本切片让代理信息穿进 Rust
控制帧,Rust 先建代理隧道(HTTP CONNECT / SOCKS5)再在隧道之上做 BoringSSL 握手——出口 IP
走代理,JA3/JA4 仍是伪装指纹。

改动文件:
- exploratory/.../tls-sidecar/src/proto.rs:ControlRequest 加 `proxy: Option<ProxySpec>`
- exploratory/.../tls-sidecar/src/proxy_tunnel.rs(新):CONNECT/SOCKS5 隧道建立(独立模块,守包结构纪律)
- exploratory/.../tls-sidecar/src/connect.rs:TcpStream::connect 前若 proxy=Some 先建隧道,隧道流喂 tokio_boring
- exploratory/.../tls-sidecar/src/main.rs:声明 mod proxy_tunnel
- backend/internal/transport/mimicry/sidecar_client.go:sidecarControlRequest 加 Proxy 字段;
  sidecarTransport 实现 WithProxy → proxyAwareRoundTripper;DialTLS 透传 proxyURL→sidecarProxySpec

**绝不碰** backend/internal/provider/proxy_resolver.go(已有 proxyAwareRoundTripper 分支)。

## 成功标准
- cargo build -p tls-sidecar && cargo test -p tls-sidecar 全绿
- go build ./... && go vet ./internal/transport/... && go test -count=1 ./internal/transport/... 全绿
- 每个新测试变异证(注入缺陷 RED 再还原)

## 安全红线(fail-closed)
代理连接 / CONNECT 非 200 / SOCKS5 握手任何一步失败 → 整连失败返回 error,**绝不 fallback 直连
目标**(直连=真实出口 IP 泄露,破坏账号级 IP 隔离)。Rust connect 路径在 proxy=Some 时,只在
隧道流上做 TLS;隧道建立失败直接 `?` 向上抛错,不存在任何 TcpStream::connect(target) 旁路。

## 不变量(指纹)
TLS 握手仍在隧道之上:tokio_boring::connect(config, target_host, tunnel_stream)。config 来自
connect_config_force_h1(profile,...),与无代理路完全相同;validate_expected_ja4_before_connect
独立于隧道(它用内存 duplex 抓 ClientHello,不经代理)。故指纹逻辑不受代理影响。

## SSRF / 凭据
- SSRF 校验留在 Go proxyadmin.proxyHostSafe 写时静态校验,本切片只透传已校验 proxyURL。
- password 不进日志:Rust 侧隧道错误信息只含 scheme://host:port(不含 user/pass);Go 侧沿用
  RedactProxyURL。ProxySpec 结构化下发(不传原始 URL),password 字段 skip 日志。

## 新依赖
无。HTTP Basic 认证的 base64 用内联手写编码器(避免 Owner-gated 新 crate 依赖)。

## blast radius
- 帧协议向后兼容:proxy 缺省 serde(default)+skip_serializing_if,老线缆不带该键。
- Go omitempty:无代理时不下发 proxy 字段,沿用今日字节。
- 行为翻转:绑代理的 sidecar 账号从"fail-loud 不可用"变"经代理可用"——这是补能力非默认翻转,
  无代理路径字节零变化。

## Owner-gated
本切片是安全项(动 IP 隔离)+ 补能力,无 schema / billing / auth-core 改动,无新依赖。
默认行为(无代理账号)零变化。代理穿透本身只在账号显式绑代理时触发。
