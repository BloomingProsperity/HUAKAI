# ⚠️ EXPLORATORY — 本目录是 Rust 出站强伪装 sidecar(方向 C),不是生产数据面

> **[2026-06-16 退役清理 · cleanup/retire-old-rust]** 旧的 **`core_gateway` 全数据面 fork**
> (让 Rust 当整个网关、用 gRPC 跟 Go 对话的旧方向)+ 3 条废弃实验 lane
> (`claude-lane`/`claude-m3`/`codex-lane`)+ 旧 gRPC 契约 `merged/proto/route.proto`
> **已删除**。方向已定为 C:**Go `gatewayhttp` 是大脑,Rust 只做出站强伪装传输**。
> 现存唯一 Rust crate = `merged/crates/tls-sidecar`(BoringSSL fork + JA3/JA4 + H2
> SETTINGS 指纹 + 防走私);Go 侧接线在 `backend/internal/transport/mimicry/sidecar_client.go`
> (unix socket + fail-closed 契约)。删除内容可从 git 历史(本分支父提交)找回。
>
> ⚠️ EXPLORATORY — 本目录是 Rust 反代探索, **不是生产数据面**. 仅 R-3 wave (TLS
> 伪装 / vendor profile) 用. 生产数据面在 backend/ Go 主仓.
