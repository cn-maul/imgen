# syntax=docker/dockerfile:1
# 多阶段构建：node 构建前端 → go 编译静态二进制（内嵌 dist）→ 运行。
# 镜像与本机运行时均为纯 Go，无需外部依赖。

FROM node:22-alpine AS web
RUN apk add --no-cache git && corepack enable && corepack prepare pnpm@11 --activate
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
# 依赖安装与构建解耦，源码变动时不触发重装。
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm run build

FROM golang:1.27-alpine AS build
# 纯 Go sqlite（modernc.org/sqlite），CGO 关闭，产物为单文件静态二进制。
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG VERSION=dev
ARG BUILD_TIME=unknown
RUN go build -tags imgenweb \
    -ldflags "-s -w -X github.com/imgen/imgen/internal/app.Version=${VERSION} -X github.com/imgen/imgen/internal/app.BuildTime=${BUILD_TIME}" \
    -o /imgen ./cmd/imgen

# 非 root 运行；数据目录为可写卷，密钥/数据库/图片落盘于此。
FROM alpine:3
RUN adduser -D -u 10001 imgen
WORKDIR /home/imgen
COPY --from=build /imgen /imgen
VOLUME ["/home/imgen/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8080/api/v1/health || exit 1
ENTRYPOINT ["/imgen"]
CMD ["serve", "--data", "/home/imgen/data"]
