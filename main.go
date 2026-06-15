package main

import (
	"log"
)

func main() {
	// 初始化配置
	if err := InitConfig(); err != nil {
		log.Fatalf("配置初始化失败: %v", err)
	}

	cfg := GetConfig()

	// 创建并启动服务器
	server := NewServer(cfg)
	server.RegisterRoutes()

	// 启动服务（带日志中间件）
	if err := server.StartWithMiddleware(); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
