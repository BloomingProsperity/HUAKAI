# 前端 SPA 嵌入单二进制 —— 构建链补齐 + Dockerfile gated 改动

日期:2026-06-25 · 作者:Claude(/loop 自驱)· 状态:✅ 已完成(2026-06-26 Owner「解锁所有」后落地 Dockerfile + compose context;真 docker build 验证镜像内二进制确含真前端 SPA 与 assets)

## 背景与缺口(核源码确认)

`backend/internal/webui` 早已有 embed 脚手架并已接进网关:

- `embed_on.go`(`//go:build embed`)以 `//go:embed all:dist` 把 `dist/` 打进二进制;`Dist()` 返回该 FS。
- `embed_off.go`(`//go:build !embed`,**默认构建**)的 `Dist()` 返回 `nil`。
- `backend/cmd/gateway/middleware.go` 已挂载:`if spa := webui.Handler(webui.Dist()); spa != nil { … }`。
- `webui.go` 的 Handler 对非 API 路径回退 `index.html`(客户端路由),`IsAPIPath` 守卫由 `webui_guard_test` 保证不吞已注册路由。

**断点在构建链**:

1. `backend/internal/webui/dist/` 里只有 6/19 留下的**占位** `index.html`(548 字节,空 `#root`),不是真正的 React 产物。仓库 `.gitignore` 忽略 `dist/` 但显式保留这个占位文件(`!backend/internal/webui/dist/index.html`),以便干净检出也能用 `-tags embed` 编译。
2. `frontend/` 的 `vite build` 产物落在 `frontend/dist/`,**没有任何步骤把它拷进 `webui/dist/`**(`vite.config.ts` 注释也明说"后续 embed 切片把 dist 拷进")。
3. `backend/Dockerfile:19` 的 `go build` **既无 `-tags embed` 也不构建前端** → 发货镜像里 `Dist()` 返回 nil → **前端根本没被服务**。

净效果:前端 16 个模块虽已建好,**当前发货二进制不会提供任何前端页面**。

## 本切片交付(自主范围:本地构建工具链)

- `scripts/build-frontend-embed.sh`:在仓库根执行 → `npm ci`(无 lock 则 `npm install`)+ `vite build` → 清空 `webui/dist` 后整体拷入真实产物。产物受 `.gitignore` 忽略,是构建期临时态,**不提交**。
- `backend/Makefile` 新增:
  - `embed-assets`:调用上面的脚本。
  - `build-embed`:`embed-assets` 后 `go build -tags embed`,产出内嵌 SPA 的单二进制。

### 本地验证(已实跑)

```
scripts/build-frontend-embed.sh        # vite build + 拷贝 → webui/dist 变成真产物(assets/ + 真 index.html)
cd backend
go test -tags embed ./internal/webui/  # ok
go build -tags embed ./cmd/gateway     # 产出 ~56MB 内嵌二进制
go test -tags embed ./cmd/gateway/ -run Guard  # ok(无路由被 SPA 壳吞)
```

## 待 Owner 拍板(gated:deployment/Dockerfile)

按既往约定 deploy/Dockerfile 改动 Owner-gated,故本切片**不动 Dockerfile**,在此 surface 所需改动:

`backend/Dockerfile` 当前是单 Go 阶段。需补一个 Node 构建阶段并启用 embed,等价于:

```dockerfile
# 1) 新增前端构建阶段
FROM node:22-alpine AS frontend
WORKDIR /fe
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ ./
RUN npm run build            # 产物 /fe/dist

# 2) Go 构建阶段:拷入前端产物到 embed 落点,并启用 -tags embed
FROM golang:1.25.11-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=frontend /fe/dist ./internal/webui/dist     # 覆盖占位
RUN CGO_ENABLED=0 GOOS=linux go build -tags embed -o /out/huakai-gateway ./cmd/gateway
```

注意:Docker build context 需包含 `frontend/`(当前 `backend/Dockerfile` 的 context 可能仅 `backend/`,需 Owner 确认 build context 或把 Dockerfile 上移到仓库根)。这点连同上述 diff 一并请 Owner 定夺。

## 风险与回滚

- 本切片只加脚本 + Makefile target + 本文档,不改任何生产代码/路由/Dockerfile,blast radius ≈ 0;默认 `make build`(不带 embed)行为完全不变。
- 回滚 = 删三个文件即可。
