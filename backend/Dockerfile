# HUAKAI Gateway Dockerfile —— 前端 SPA 内嵌单二进制。
#
# **build context = 仓库根**(不是 backend/),因为要把 frontend/ 一并纳入,在镜像内
# vite build 后由 Go 的 go:embed(-tags embed)打进同一个二进制,运行时由网关 webui.Handler
# 直接提供 SPA(无需外挂前端/反代静态)。故下面 COPY 路径带 backend/ / frontend/ 前缀。
# compose 已配 context: ..(仓库根)+ dockerfile: backend/Dockerfile;根级 .dockerignore 排除
# node_modules/参考仓/产物。
#
# Runtime 必须 env vars (config.Load 只读 env, 不读 YAML 文件):
#   HUAKAI_DATABASE_URL          - PostgreSQL DSN
#   HUAKAI_CREDENTIAL_KEY_B64    - 32-byte base64 加密 key
#   HUAKAI_SESSION_SIGNING_KEY_HEX 或 _B64 - JWT session 签名 key
#   HUAKAI_ADMIN_BOOTSTRAP_TOKEN - 首次启动 (admin_tokens 空时) bootstrap token
#   HUAKAI_RELEASE_MODE          - explicit: production / dev / development / test
#   可选: HUAKAI_ADDR (default :8080), OAuth client_id/secret 等
# 缺关键 env 启动 fail-loud。

# ── 前端构建阶段:vite build → /fe/dist(随后由 Go 阶段 go:embed 进二进制)──
FROM node:22-alpine AS frontend
WORKDIR /fe
# 先只拷依赖清单以最大化 layer 缓存:src 改动不会让 npm ci 失效。
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ── Go 构建阶段:context=仓库根,路径带 backend/ 前缀;拷入前端产物到 go:embed 落点并启用 -tags embed ──
FROM golang:1.25.11-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum* ./
RUN go mod download
COPY backend/ ./
# 用真实前端产物覆盖 internal/webui/dist 的占位 index.html;//go:embed all:dist 据此把 SPA 编进二进制。
COPY --from=frontend /fe/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -tags embed -o /out/huakai-gateway ./cmd/gateway

FROM alpine:3.20
RUN apk --no-cache add ca-certificates wget \
    && adduser -D -u 10001 huakai
COPY --from=builder --chown=huakai:huakai /out/huakai-gateway /usr/local/bin/huakai-gateway
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
USER huakai
ENTRYPOINT ["/usr/local/bin/huakai-gateway"]
