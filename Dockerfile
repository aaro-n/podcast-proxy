# --- Stage 1: Build ---
FROM golang:1.21-alpine AS builder

WORKDIR /app

# 关键改动：将 main.go 替换为 *.go，以复制所有 Go 源文件
COPY *.go ./

# 在容器内自动初始化一个模块，以满足现代 Go 的构建要求
RUN go mod init podcast-proxy

# 构建命令 go build . 会自动找到当前目录下的所有 .go 文件并一起编译
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /podcast-proxy .

# --- Stage 2: Final Image ---
FROM alpine:latest

COPY --from=builder /podcast-proxy /podcast-proxy

EXPOSE 8080

ENTRYPOINT ["/podcast-proxy"]
