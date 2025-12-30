# Multi-stage Dockerfile for LLM Gateway with SQLite CGO support
# 构建阶段
FROM golang:1.21-alpine AS builder

# 设置工作目录
WORKDIR /app

# 安装构建依赖（CGO 需要的 C 编译器）
RUN apk add --no-cache gcc musl-dev git

# 复制 go mod 文件
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 构建应用（启用 CGO，并使用编译优化标志）
# -s: 去除符号表
# -w: 去除 DWARF 调试信息
# CGO_ENABLED=1: 支持 SQLite
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o gateway \
    ./cmd/main.go ./cmd/handlers.go ./cmd/handlers_dashboard.go ./cmd/middleware.go

# 运行阶段
FROM alpine:latest

# 安装运行时依赖
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    curl \
    && rm -rf /var/cache/apk/*

# 设置时区
ENV TZ=Asia/Shanghai

# 创建非 root 用户
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

# 设置工作目录
WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/gateway /app/gateway

# 创建数据目录和数据库文件
RUN mkdir -p /app/data && \
    touch /app/data/gateway.db && \
    chown -R appuser:appgroup /app

# 切换到非 root 用户
USER appuser

# 暴露端口
EXPOSE 8000

# 健康检查
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8000/health || exit 1

# 启动命令
CMD ["/app/gateway"]