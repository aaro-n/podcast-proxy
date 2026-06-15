# 开发指南

## 环境搭建

### 前置要求

- Go 1.21 或更高版本
- Git
- Docker（可选）

### 本地开发

```bash
# 1. 克隆项目
git clone https://github.com/your-repo/podcast-proxy.git
cd podcast-proxy

# 2. 安装依赖
go mod download

# 3. 编译
go build -o podcast-proxy

# 4. 设置环境变量
export PODCAST_PROXY_APIKEY="dev-key"
export PORT="8080"
export DEBUG="1"

# 5. 运行
./podcast-proxy

# 或直接运行
go run .
```

## 代码风格

### 命名规范

- **包名**: 小写，单词不分割 (`config`, `auth`)
- **函数名**: 驼峰命名，导出函数大写开头 (`NewServer`, `Handle`)
- **变量名**: 驼峰命名，简洁明了 (`apikey`, `origURL`)
- **常量**: 大写，下划线分割 (`ResourceAudio`, `StatusUnauthorized`)
- **接口名**: 以`er`后缀结尾 (`Handler`, `Manager`)

### 代码组织

```go
package main

// 常量定义
const (
    DefaultTimeout = 30
)

// 类型定义
type MyStruct struct {
    Field1 string
    Field2 int
}

// 公共方法
func (ms *MyStruct) PublicMethod() {
    // ...
}

// 私有方法
func (ms *MyStruct) privateMethod() {
    // ...
}
```

### 错误处理

```go
// ✅ 好
resp, err := http.Get(url)
if err != nil {
    return nil, fmt.Errorf("failed to fetch: %w", err)
}
defer resp.Body.Close()

// ❌ 避免
resp, _ := http.Get(url)  // 忽略错误
if resp == nil {
    // ...
}
```

### 日志记录

```go
// ✅ 好
log.Printf("处理请求: method=%s path=%s duration=%v", r.Method, r.URL.Path, duration)

// ❌ 避免
log.Println(r.Method + " " + r.URL.Path + " " + duration.String())
```

## 添加新功能

### 1. 添加新的代理资源类型

假设要添加视频代理：

**第1步**: 更新 `models.go`

```go
const (
    ResourceAudio ProxyResource = "audio"
    ResourceVideo ProxyResource = "video"  // 新增
)
```

**第2步**: 创建处理器在 `handlers.go`

```go
type VideoHandler struct {
    *HandlerBase
    cacheManager *CacheManager
    sh           *StringHelper
}

func NewVideoHandler(r *http.Request) *VideoHandler {
    return &VideoHandler{
        HandlerBase: newHandlerBase(r),
        cacheManager: getCacheManager(),
        sh: &StringHelper{},
    }
}

func (vh *VideoHandler) Handle(w http.ResponseWriter, r *http.Request) {
    vh.logger.LogStart()
    
    // 验证API Key
    apikey, valid := vh.auth.VerifyRequest(r, "/video/")
    if !valid || apikey == "" {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        vh.logger.LogComplete(http.StatusUnauthorized)
        return
    }
    
    // 获取源URL并处理...
    // (参考AudioHandler的实现)
    
    vh.logger.LogComplete(http.StatusOK)
}
```

**第3步**: 在 `server.go` 注册路由

```go
func (s *Server) RegisterRoutes() {
    // ... 现有路由 ...
    
    s.mux.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
        handler := NewVideoHandler(r)
        handler.Handle(w, r)
    })
    s.mux.HandleFunc("/video", func(w http.ResponseWriter, r *http.Request) {
        handler := NewVideoHandler(r)
        handler.Handle(w, r)
    })
}
```

**第4步**: 在 `utils.go` 添加URL构建方法

```go
func (b *ProxyURLBuilder) BuildVideoURL(apikey, originalURL string) string {
    return b.buildURL("video", apikey, originalURL)
}
```

### 2. 添加新的中间件

**第1步**: 在 `server.go` 定义中间件

```go
func AuthMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 认证逻辑
        if !isAuthorized(r) {
            http.Error(w, "Unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

**第2步**: 在 `StartWithMiddleware` 中使用

```go
func (s *Server) StartWithMiddleware() error {
    addr := fmt.Sprintf(":%s", s.config.Port)
    handler := logMiddleware(s.mux)
    handler = AuthMiddleware(handler)
    handler = CORSMiddleware(handler)
    return http.ListenAndServe(addr, handler)
}
```

### 3. 改进缓存策略

**第1步**: 创建新的缓存管理器 (`cache_redis.go`)

```go
package main

import "github.com/go-redis/redis/v8"

type RedisCacheManager struct {
    client *redis.Client
    ttl    time.Duration
}

func NewRedisCacheManager(addr string, ttl time.Duration) *RedisCacheManager {
    return &RedisCacheManager{
        client: redis.NewClient(&redis.Options{
            Addr: addr,
        }),
        ttl: ttl,
    }
}

func (rcm *RedisCacheManager) Get(key string) (*CacheEntry, bool) {
    // 实现从Redis获取
}

func (rcm *RedisCacheManager) Set(key string, entry *CacheEntry) {
    // 实现存储到Redis
}
```

**第2步**: 在 `config.go` 选择使用

```go
func getCacheManager() *CacheManager {
    if os.Getenv("USE_REDIS") == "true" {
        return NewRedisCacheManager(
            os.Getenv("REDIS_ADDR"),
            24 * time.Hour,
        )
    }
    // 使用默认内存缓存
}
```

## 测试

### 单元测试

```bash
# 运行所有测试
go test ./...

# 运行特定测试
go test -run TestFeedTransform

# 查看测试覆盖率
go test -cover ./...

# 生成覆盖率报告
go test -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### 集成测试

创建 `integration_test.go`:

```go
package main

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestFeedHandlerIntegration(t *testing.T) {
    // 创建测试服务器
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        handler := NewFeedHandler(r)
        handler.Handle(w, r)
    }))
    defer server.Close()

    // 发送测试请求
    resp, err := http.Get(server.URL + "/feed?url=http://example.com&apikey=test")
    if err != nil {
        t.Fatal(err)
    }
    defer resp.Body.Close()

    // 验证响应
    if resp.StatusCode != http.StatusOK {
        t.Errorf("Expected 200, got %d", resp.StatusCode)
    }
}
```

### 负载测试

```bash
# 使用 Apache Bench
ab -n 1000 -c 10 "http://localhost:8080/feed?url=...&apikey=..."

# 使用 wrk
wrk -t4 -c100 -d30s "http://localhost:8080/feed?url=...&apikey=..."

# 使用 go-wrk (Go实现)
go-wrk -n 10000 -c 100 "http://localhost:8080/feed?url=...&apikey=..."
```

## 调试

### 启用调试日志

```bash
DEBUG=1 go run .
```

在代码中添加调试日志：

```go
if os.Getenv("DEBUG") == "1" {
    log.Printf("DEBUG: 变量值=%v", value)
}
```

### 使用Delve调试器

```bash
# 安装
go install github.com/go-delve/delve/cmd/dlv@latest

# 启动调试
dlv debug

# 常用命令
# break main.main          - 设置断点
# continue                 - 继续执行
# next                     - 下一行
# step                     - 步入
# print variable           - 打印变量
# goroutines               - 列出协程
```

### HTTP请求调试

```bash
# 使用curl的详细模式
curl -v "http://localhost:8080/feed?url=...&apikey=..."

# 保存请求/响应
curl -D - "http://localhost:8080/feed?url=...&apikey=..." > response.txt

# 使用httpie (更友好)
http get localhost:8080/feed url=... apikey=...
```

## 性能分析

### CPU分析

```bash
# 生成CPU性能数据
go test -cpuprofile=cpu.prof -bench=.

# 分析
go tool pprof cpu.prof
```

### 内存分析

```bash
# 生成内存性能数据
go test -memprofile=mem.prof

# 分析
go tool pprof mem.prof
```

### 实时分析

在代码中添加pprof端点：

```go
import _ "net/http/pprof"

func init() {
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()
}
```

然后访问:
- `http://localhost:6060/debug/pprof/` - 性能分析主页
- `http://localhost:6060/debug/pprof/heap` - 内存分析
- `http://localhost:6060/debug/pprof/goroutine` - 协程分析

## 代码审查清单

提交PR前请检查：

- [ ] 代码遵循命名规范
- [ ] 错误都被正确处理
- [ ] 添加了必要的日志
- [ ] 编写了单元测试
- [ ] 测试覆盖率 > 80%
- [ ] 没有未使用的导入
- [ ] 没有硬编码的值（使用常量）
- [ ] 添加了中文注释（关键部分）
- [ ] 更新了相关文档
- [ ] 代码能通过 `go vet` 检查

```bash
# 代码检查
go fmt ./...       # 格式化代码
go vet ./...       # 静态分析
golint ./...       # Lint检查
```

## 依赖管理

```bash
# 添加依赖
go get github.com/user/package

# 更新依赖
go get -u github.com/user/package

# 清理未使用的依赖
go mod tidy

# 验证依赖
go mod verify

# 查看依赖树
go mod graph
```

## 构建和发布

### 本地构建

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o podcast-proxy-linux

# macOS
GOOS=darwin GOARCH=amd64 go build -o podcast-proxy-mac

# Windows
GOOS=windows GOARCH=amd64 go build -o podcast-proxy.exe
```

### Docker构建

```bash
# 构建镜像
docker build -t podcast-proxy:latest .

# 标记版本
docker tag podcast-proxy:latest podcast-proxy:v2.0

# 推送到仓库
docker push your-repo/podcast-proxy:latest
```

### 版本管理

使用语义版本 (Semantic Versioning):

```
v主版本.次版本.修订版本

v2.0.0 - 主版本升级（不兼容变更）
v2.1.0 - 次版本升级（新功能，向后兼容）
v2.0.1 - 修订版本升级（bug修复）
```

## 常见问题

### Q: 如何增加日志详细度？

A: 在 `utils.go` 中的 `LoggerHelper` 类增加详细信息：

```go
func (l *LoggerHelper) LogRequest(r *http.Request) {
    fmt.Printf("Headers: %v\n", r.Header)
    fmt.Printf("Query: %v\n", r.URL.Query())
}
```

### Q: 如何支持新的缓存后端？

A: 创建实现CacheManager接口的新类，参考cache_redis.go示例。

### Q: 如何提高并发性能？

A: 
1. 增加HTTP连接池大小（proxy.go）
2. 使用Redis替代内存缓存
3. 考虑水平扩展

### Q: 如何调试Range请求问题？

```bash
# 检查Range请求是否被转发
curl -v -H "Range: bytes=0-1000" "http://localhost:8080/audio/..."

# 查看响应头
curl -i -H "Range: bytes=0-1000" "http://localhost:8080/audio/..."
```

## 更多资源

- [Go官方文档](https://golang.org/doc/)
- [HTTP/1.1 Range Requests (RFC 7233)](https://tools.ietf.org/html/rfc7233)
- [RSS 2.0 Specification](https://www.rssboard.org/rss-specification)
- [Effective Go](https://golang.org/doc/effective_go)
