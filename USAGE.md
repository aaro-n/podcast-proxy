# 使用示例

## 基础使用

### 1. 启动代理服务

```bash
export PODCAST_PROXY_APIKEY="my-secret-key"
export PORT="8080"
export FORCE_HTTPS="false"

go run .
```

### 2. 在Web界面生成链接

打开浏览器访问 `http://localhost:8080/`

输入：
- **原始RSS地址**: `https://example.com/podcast/feed.xml`
- **API Key**: `my-secret-key`

点击"生成代理链接"，得到类似：
```
http://localhost:8080/feed?url=https://example.com/podcast/feed.xml&apikey=my-secret-key
```

### 3. 在播客客户端中使用

在播客应用（如Apple Podcasts、Spotify等）中：

1. 搜索播客 → 选择"添加自定义源"
2. 粘贴生成的代理链接
3. 享受加速播放！

## 实战场景

### 场景1: 加速国内播客源

**原始源**: `https://feeds.example.com/podcast.xml`

```bash
# 步骤1: 配置代理
export PODCAST_PROXY_APIKEY="china-cdn"
export PUBLIC_HOST="proxy.example.com"  # 使用国内CDN域名

# 步骤2: 生成代理链接
curl "http://localhost:8080/feed?url=https://feeds.example.com/podcast.xml&apikey=china-cdn"

# 步骤3: 在播客客户端中添加
# 链接: http://proxy.example.com/feed?url=https://feeds.example.com/podcast.xml&apikey=china-cdn
```

### 场景2: 支持多个API密钥（组织/用户）

当前版本只支持单个API Key。如需支持多个用户，建议扩展：

**修改 auth.go**:

```go
type MultiKeyAuthManager struct {
    validKeys map[string]bool  // apikey -> valid
}

func (mkam *MultiKeyAuthManager) VerifyAPIKey(apikey string) bool {
    return mkam.validKeys[apikey]
}
```

**使用**:

```bash
export PODCAST_PROXY_APIKEYS="key1,key2,key3"

# 任何一个key都可以使用
curl "http://localhost:8080/feed?url=...&apikey=key1"
curl "http://localhost:8080/feed?url=...&apikey=key2"
```

### 场景3: 处理需要认证的源

某些RSS源需要基本认证：

**修改 proxy.go** 中的 `ProxyRequest.Do()`:

```go
func (pr *ProxyRequest) Do(sourceReq *http.Request) (*http.Response, error) {
    req, err := http.NewRequest("GET", pr.originalURL, nil)
    if err != nil {
        return nil, err
    }

    // 添加基本认证支持
    if basicAuth := os.Getenv("SOURCE_BASIC_AUTH"); basicAuth != "" {
        // basicAuth 格式: "username:password"
        req.SetBasicAuth(basicAuth) // 需要解析
    }

    req.Header.Set("User-Agent", "PodcastProxy/2.0")
    
    return pr.client.Do(req)
}
```

使用：

```bash
export SOURCE_BASIC_AUTH="user:password"
go run .
```

### 场景4: 监控缓存效率

添加统计中间件：

**创建 stats.go**:

```go
package main

import (
    "sync"
    "sync/atomic"
)

type Statistics struct {
    TotalRequests   int64
    CacheHits       int64
    CacheMisses     int64
    BytesServed     int64
    ErrorCount      int64
    mu              sync.RWMutex
}

var stats = &Statistics{}

func (s *Statistics) RecordHit() {
    atomic.AddInt64(&s.CacheHits, 1)
}

func (s *Statistics) RecordMiss() {
    atomic.AddInt64(&s.CacheMisses, 1)
}

func (s *Statistics) GetCacheHitRate() float64 {
    total := atomic.LoadInt64(&s.TotalRequests)
    if total == 0 {
        return 0
    }
    hits := atomic.LoadInt64(&s.CacheHits)
    return float64(hits) / float64(total)
}
```

在 **server.go** 中添加统计端点：

```go
func (s *Server) RegisterRoutes() {
    // ... 现有路由 ...
    
    s.mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]interface{}{
            "total_requests": stats.TotalRequests,
            "cache_hits": stats.CacheHits,
            "cache_misses": stats.CacheMisses,
            "hit_rate": stats.GetCacheHitRate(),
        })
    })
}
```

访问统计信息：

```bash
curl http://localhost:8080/stats
```

### 场景5: 高可用部署

使用多个代理实例和负载均衡：

**docker-compose-ha.yml**:

```yaml
version: '3.8'

services:
  proxy-1:
    image: podcast-proxy:latest
    environment:
      PODCAST_PROXY_APIKEY: shared-key
      PORT: 8080
    ports:
      - "8081:8080"

  proxy-2:
    image: podcast-proxy:latest
    environment:
      PODCAST_PROXY_APIKEY: shared-key
      PORT: 8080
    ports:
      - "8082:8080"

  proxy-3:
    image: podcast-proxy:latest
    environment:
      PODCAST_PROXY_APIKEY: shared-key
      PORT: 8080
    ports:
      - "8083:8080"

  # Nginx负载均衡器
  nginx:
    image: nginx:latest
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
    ports:
      - "8080:8080"
    depends_on:
      - proxy-1
      - proxy-2
      - proxy-3
```

**nginx.conf**:

```nginx
upstream podcast_proxy {
    server proxy-1:8080;
    server proxy-2:8080;
    server proxy-3:8080;
}

server {
    listen 8080;
    
    location / {
        proxy_pass http://podcast_proxy;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
        
        # 启用连接复用
        proxy_http_version 1.1;
        proxy_set_header Connection "";
    }
}
```

启动HA集群：

```bash
docker-compose -f docker-compose-ha.yml up -d
curl http://localhost:8080/feed?url=...&apikey=...
```

### 场景6: 跨域支持

某些播客应用可能需要CORS支持：

**修改 server.go**:

```go
func (s *Server) StartWithMiddleware() error {
    addr := fmt.Sprintf(":%s", s.config.Port)
    handler := logMiddleware(s.mux)
    handler = corsMiddleware(handler)
    handler = logMiddleware(handler)
    return http.ListenAndServe(addr, handler)
}

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range")
        w.Header().Set("Access-Control-Max-Age", "86400")
        
        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }
        
        next.ServeHTTP(w, r)
    })
}
```

### 场景7: 速率限制（防止滥用）

**创建 ratelimit.go**:

```go
package main

import (
    "net/http"
    "sync"
    "time"
)

type RateLimiter struct {
    visitors map[string]*Visitor
    mu       sync.RWMutex
}

type Visitor struct {
    lastSeen time.Time
    count    int
    limit    int
    window   time.Duration
}

func NewRateLimiter() *RateLimiter {
    rl := &RateLimiter{
        visitors: make(map[string]*Visitor),
    }
    
    // 定期清理过期访问者
    go func() {
        for {
            time.Sleep(1 * time.Minute)
            rl.cleanup()
        }
    }()
    
    return rl
}

func (rl *RateLimiter) Allow(ip string, limit int) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    now := time.Now()
    visitor, exists := rl.visitors[ip]
    
    if !exists {
        rl.visitors[ip] = &Visitor{
            lastSeen: now,
            count:    1,
            limit:    limit,
            window:   1 * time.Minute,
        }
        return true
    }
    
    // 检查时间窗口
    if now.Sub(visitor.lastSeen) > visitor.window {
        visitor.count = 1
        visitor.lastSeen = now
        return true
    }
    
    // 检查限制
    if visitor.count >= visitor.limit {
        return false
    }
    
    visitor.count++
    return true
}

func (rl *RateLimiter) cleanup() {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    now := time.Now()
    for ip, visitor := range rl.visitors {
        if now.Sub(visitor.lastSeen) > 10*time.Minute {
            delete(rl.visitors, ip)
        }
    }
}

func rateLimitMiddleware(limiter *RateLimiter, limit int) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := getRemoteAddr(r)
            
            if !limiter.Allow(ip, limit) {
                http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
                return
            }
            
            next.ServeHTTP(w, r)
        })
    }
}
```

使用：

```go
limiter := NewRateLimiter()

func (s *Server) StartWithMiddleware() error {
    addr := fmt.Sprintf(":%s", s.config.Port)
    handler := logMiddleware(s.mux)
    handler = rateLimitMiddleware(limiter, 100)(handler)
    return http.ListenAndServe(addr, handler)
}
```

### 场景8: 自定义日志输出

支持将日志写入文件：

**修改 main.go**:

```go
import "log"
import "os"

func main() {
    // 配置日志
    logFile := os.Getenv("LOG_FILE")
    if logFile != "" {
        f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
        if err != nil {
            log.Fatalf("Failed to open log file: %v", err)
        }
        defer f.Close()
        log.SetOutput(f)
    }

    // 初始化和启动...
    InitConfig()
    // ...
}
```

使用：

```bash
export LOG_FILE="/var/log/podcast-proxy.log"
go run .
```

## 故障排查

### 问题: 播客客户端无法连接

**检查列表**:

```bash
# 1. 验证服务是否运行
curl http://localhost:8080/

# 2. 检查API Key
curl "http://localhost:8080/feed?url=https://example.com/feed.xml&apikey=wrong-key"
# 应返回401

# 3. 测试RSS源
curl "http://localhost:8080/feed?url=https://example.com/feed.xml&apikey=your-key" | head -20

# 4. 检查网络连接
telnet localhost 8080
```

### 问题: 音频播放卡顿

**检查**:

```bash
# 验证Range请求支持
curl -I -H "Range: bytes=0-1000" "http://localhost:8080/audio/xxx?url=..."

# 应返回 206 Partial Content
# 若返回 200 OK，说明Range支持有问题
```

### 问题: 内存占用过高

**优化**:

1. 减少缓存TTL
2. 使用Redis替代内存缓存
3. 增加HTTP连接超时
4. 监控goroutine泄漏

```bash
# 查看pprof信息
curl http://localhost:6060/debug/pprof/heap | go tool pprof -http :8090 -
```

## 性能调优

### 基准测试

```bash
# 创建benchmark_test.go
go test -bench=. -benchmem -benchtime=10s

# 性能分析
go test -cpuprofile=cpu.prof -bench=.
go tool pprof cpu.prof
```

### 监控指标

```bash
# 收集10秒内的请求统计
watch -n 1 'curl http://localhost:8080/stats 2>/dev/null | jq'
```

## 最佳实践

1. ✅ 始终使用HTTPS部署
2. ✅ 定期更新依赖
3. ✅ 监控服务日志和性能
4. ✅ 设置合理的速率限制
5. ✅ 使用强加密的API Key
6. ✅ 定期备份配置
7. ✅ 在生产环境前进行充分测试
