# --- Stage 1: Build ---
FROM golang:1.21-alpine AS builder

WORKDIR /app

# 复制 go.mod 和其他 Go 源文件
COPY go.mod ./
COPY *.go ./

# 下载依赖
RUN go mod download || true

# 构建二进制文件
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /podcast-proxy .

# --- Stage 2: Final Image ---
FROM alpine:latest

COPY --from=builder /podcast-proxy /podcast-proxy

EXPOSE 8080

ENTRYPOINT ["/podcast-proxy"]
