// main.go  
package main  
  
import (  
    "context"  
    "log"  
    "net/http"  
    "os"  
    "os/signal"  
    "syscall"  
    "time"  
)  
  
var (  
    config     *Config  
    httpClient *http.Client  
    cache      *Cache  
)  
  
func main() {  
    // 1. 加载配置  
    config = LoadConfig()  
      
    // 2. 初始化全局组件  
    httpClient = &http.Client{  
        Timeout: config.RequestTimeout,  
        Transport: &http.Transport{  
            MaxIdleConns:        100,  
            MaxIdleConnsPerHost: 10,  
            IdleConnTimeout:     90 * time.Second,  
        },  
    }  
      
    cache = NewCache()  
      
    // 3. 注册路由  
    mux := http.NewServeMux()  
    mux.HandleFunc("/", indexHandler)  
    mux.HandleFunc("/feed", authMiddleware(feedHandler))  
    mux.HandleFunc("/proxy", authMiddleware(proxyHandler))  
    mux.HandleFunc("/health", healthHandler)  
      
    // 4. 启动服务器  
    server := &http.Server{  
        Addr:         ":" + config.Port,  
        Handler:      mux,  
        ReadTimeout:  15 * time.Second,  
        WriteTimeout: 15 * time.Second,  
        IdleTimeout:  60 * time.Second,  
    }  
      
    // 优雅关闭  
    go func() {  
        log.Printf("Starting server on port %s...", config.Port)  
        log.Println("Server is protected. API Key has been loaded.")  
        log.Printf("Usage: http://localhost:%s/feed?url=<your_podcast_rss_url>&apikey=<your_api_key>", config.Port)  
          
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {  
            log.Fatal(err)  
        }  
    }()  
      
    // 等待中断信号  
    quit := make(chan os.Signal, 1)  
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)  
    <-quit  
      
    log.Println("Shutting down server...")  
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)  
    defer cancel()  
      
    if err := server.Shutdown(ctx); err != nil {  
        log.Fatal("Server forced to shutdown:", err)  
    }  
      
    log.Println("Server exited")  
}  
  
func healthHandler(w http.ResponseWriter, r *http.Request) {  
    w.Header().Set("Content-Type", "application/json")  
    w.WriteHeader(http.StatusOK)  
    w.Write([]byte(`{"status":"ok","timestamp":"` + time.Now().Format(time.RFC3339) + `"}`))  
}
