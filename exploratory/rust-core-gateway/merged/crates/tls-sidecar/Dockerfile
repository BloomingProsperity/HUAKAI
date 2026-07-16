# HUAKAI TLS sidecar 镜像 —— 从源码构建 vendored BoringSSL fork + tls-sidecar 二进制。
#
# **build context = exploratory/rust-core-gateway/**(不是 crate 目录),因为 workspace 在
# merged/,而 merged/Cargo.toml 以 `../vendor/boring` 引用 vendored boring fork——两者都要进
# 构建上下文。compose 里配 context 指向该目录 + dockerfile 指向本文件相对路径。
#
# 运行时由 env 决定 unix socket 路径(main.rs 读 HUAKAI_TLS_SIDECAR_SOCKET);网关经该 socket
# 下发拨号控制帧。sidecar 是纯传输层 mimicry(BoringSSL ClientHello/JA3/JA4 + 代理隧道),不碰 body。

# ---- 构建阶段 ----
FROM rust:1.96-bookworm AS builder

# BoringSSL 从源码构建:cmake + perl + make + C/C++ 编译器;boring-sys bindgen 需 clang/libclang。
# bookworm 自带 golang 偏旧,BoringSSL util 工具链装官方新版 Go 保险。
RUN apt-get update && apt-get install -y --no-install-recommends \
        cmake \
        clang \
        libclang-dev \
        perl \
        make \
    && rm -rf /var/lib/apt/lists/*

ENV GO_VERSION=1.23.4
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz \
    && tar -C /usr/local -xzf /tmp/go.tgz \
    && rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /build
# 复制 workspace(merged/)与 vendored boring fork(vendor/)。
COPY merged merged
COPY vendor vendor

# 只构建 tls-sidecar(退役的 core_gateway 不构建),release。
RUN cargo build --release --manifest-path merged/Cargo.toml -p tls-sidecar

# ---- 运行阶段 ----
FROM debian:bookworm-slim AS runtime

# 出站 TLS 校验上游证书需 ca-certificates;非 root 运行。
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --no-create-home --uid 10001 huakai

COPY --from=builder /build/merged/target/release/tls-sidecar /usr/local/bin/tls-sidecar

# 与网关共享的 unix socket 目录(compose 把命名卷挂到此)。
RUN mkdir -p /run/huakai && chown huakai:huakai /run/huakai
USER huakai

# socket 路径(main.rs 缺省 /tmp/huakai-tls-sidecar.sock;这里指到共享卷)。
ENV HUAKAI_TLS_SIDECAR_SOCKET=/run/huakai/tls-sidecar.sock

ENTRYPOINT ["/usr/local/bin/tls-sidecar"]
