// main.go
package main

import (
        "log"
        "net/http"
        "os"
)

// serverAPIKey 是一个包级别的变量，用于存储从环境变量加载的 API Key。
// middleware.go 中的 authMiddleware 可以直接访问它。
var serverAPIKey string

func main() {
        // 1. 加载配置
        serverAPIKey = os.Getenv("API_KEY")
        if serverAPIKey == "" {
                log.Fatal("FATAL: API_KEY environment variable is not set.")
        }

        port := os.Getenv("PORT")
        if port == "" {
                port = "8080"
        }

        // 2. 注册路由
        // 根路径是公开的，用于显示说明
        http.HandleFunc("/", indexHandler)
        // /feed 和 /proxy 路径需要经过认证中间件
        http.HandleFunc("/feed", authMiddleware(feedHandler))
        http.HandleFunc("/proxy", authMiddleware(proxyHandler))

        // 3. 启动服务器
        log.Printf("Starting server on port %s...", port)
        log.Println("Server is protected. API Key has been loaded.")
        log.Printf("Usage: http://localhost:%s/feed?url=<your_podcast_rss_url>&apikey=<your_api_key>", port)

        if err := http.ListenAndServe(":"+port, nil); err != nil {
                log.Fatal(err)
        }
}
