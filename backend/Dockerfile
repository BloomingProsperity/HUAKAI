# HUAKAI Gateway Dockerfile —— Go 网关与 Rust TLS sidecar 同镜像运行。
#
# **build context = 仓库根**（不是 backend/），因为生产镜像同时编译
# exploratory/rust-core-gateway 下的 Rust sidecar。当前仓库没有可信前端源码，
# 因此网关按无 embed 构建，API 可独立上线；后续前端重构完成后再恢复独立静态构建合同。
#
# Runtime 必须 env vars (config.Load 只读 env, 不读 YAML 文件):
#   HUAKAI_DATABASE_URL          - PostgreSQL DSN
#   HUAKAI_CREDENTIAL_KEY_B64    - 32-byte base64 加密 key
#   HUAKAI_SESSION_SIGNING_KEY_HEX 或 _B64 - JWT session 签名 key
#   HUAKAI_ADMIN_BOOTSTRAP_TOKEN - 首次启动 (admin_tokens 空时) bootstrap token
#   HUAKAI_RELEASE_MODE          - explicit: production / dev / development / test
#   可选: HUAKAI_ADDR (default :8080), OAuth client_id/secret 等
# 缺关键 env 启动 fail-loud。

# ── Go 构建阶段：后端 API 独立构建，不嵌入不存在的前端产物。──
FROM golang:1.25.12-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/huakai-gateway ./cmd/gateway

# ── Rust 构建阶段：编译已审计的 BoringSSL sidecar，生产出口不再包含 Go TLS 伪装。──
FROM rust:1.96-bookworm AS rust-builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential clang cmake libclang-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /rust/merged
COPY exploratory/rust-core-gateway/vendor/ /rust/vendor/
COPY exploratory/rust-core-gateway/merged/ ./
RUN cargo build --locked --release -p tls-sidecar

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates wget \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 huakai \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin huakai \
    && install -d -o huakai -g huakai /run/huakai
COPY --from=builder --chown=huakai:huakai /out/huakai-gateway /usr/local/bin/huakai-gateway
COPY --from=rust-builder --chown=huakai:huakai /rust/merged/target/release/tls-sidecar /usr/local/bin/huakai-tls-sidecar
COPY --chown=huakai:huakai backend/deploy/gateway-entrypoint.sh /usr/local/bin/huakai-entrypoint
RUN chmod 0755 /usr/local/bin/huakai-entrypoint
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8080/readyz || exit 1
USER huakai
ENTRYPOINT ["/usr/local/bin/huakai-entrypoint"]
