# HUAKAI Gateway development Dockerfile.
# Phase 3 skeleton; operator-grade hardening (distroless, non-root, healthcheck)
# added in Phase 8.

FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/huakai-gateway ./cmd/gateway

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /out/huakai-gateway /usr/local/bin/huakai-gateway
COPY config.example.yaml /etc/huakai/config.yaml
ENV HUAKAI_CONFIG=/etc/huakai/config.yaml
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/huakai-gateway"]
