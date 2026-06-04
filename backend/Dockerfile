# HUAKAI Gateway development Dockerfile.
# Development image for the current Phase C gateway slice. Operator-grade
# hardening (distroless, non-root, healthcheck) is deferred to Phase 8.
#
# Runtime 必须 env vars (config.Load 只读 env, 不读 YAML 文件):
#   HUAKAI_DATABASE_URL          - PostgreSQL DSN
#   HUAKAI_CREDENTIAL_KEY_B64    - 32-byte base64 加密 key
#   HUAKAI_SESSION_SIGNING_KEY_HEX 或 _B64 - JWT session 签名 key
#   HUAKAI_ADMIN_BOOTSTRAP_TOKEN - 首次启动 (admin_tokens 空时) bootstrap token
#   HUAKAI_RELEASE_MODE          - explicit: production / dev / development / test
#   可选: HUAKAI_ADDR (default :8080), OAuth client_id/secret 等
# 缺关键 env 启动 fail-loud。

FROM golang:1.25.11-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/huakai-gateway ./cmd/gateway

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /out/huakai-gateway /usr/local/bin/huakai-gateway
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/huakai-gateway"]
