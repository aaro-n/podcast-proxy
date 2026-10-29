# 播客代理服务器 (Podcast Proxy) - v2.0

一个高性能的播客RSS源代理服务，支持ETag缓存、Range请求和完整的模块化架构。

## 功能特性

✅ **RSS源转换** - 自动将播客源中的所有媒体URL转换为代理地址
✅ **原生 HTML 展示** - 浏览器可协商 HTML 页面；订阅客户端始终获得 RSS/Atom XML，不注入 XSLT 指令
✅ **智能缓存** - 区分源站 validator 与转换后表示 ETag，支持 ETag/Last-Modified、Age/Expires、Vary 和 stale-if-error
✅ **快速跳转** - 音频代理转发 Range/If-Range 并透传正确的 206/Content-Range/Content-Length
✅ **流量节省** - feed 使用转换表示的 validator；源站 validator 仅用于验证对应的源缓存
✅ **完整认证** - API Key认证，支持Base64编码
✅ **模块化架构** - 清晰的代码结构，易于扩展和维护
✅ **多资源类型** - 支持音频、图片、样式表等多种资源代理

## 项目结构

```
podcast-proxy/
├── main.go              # 程序入口
├── config.go            # 配置管理
├── auth.go              # 认证管理
├── models.go            # 数据模型
├── utils.go             # 工具函数（URL构建、日志等）
├── cache.go             # ETag缓存管理
├── feed.go              # RSS解析和转换
├── proxy.go             # HTTP代理逻辑
├── handlers.go          # HTTP处理器
├── server.go            # HTTP服务器
├── Dockerfile           # Docker配置
├── docker-compose.yml   # Docker Compose
├── go.mod              # Go模块
└── README.md           # 说明文档
```

## 快速开始

### 环境变量配置

```bash
# 必需
export PODCAST_PROXY_APIKEY="your-api-key"

# 可选
export PORT="8080"                    # 默认8080
export FORCE_HTTPS="false"           # 是否强制HTTPS
export PUBLIC_HOST="example.com"     # 公开域名（用于生成代理URL）
export TIMEOUT="30"                  # HTTP超时（秒）
export FEED_MAX_BYTES="8388608"     # feed最大尺寸（默认8 MiB）
export FEED_MAX_CONCURRENCY="16"     # 同时回源feed数量
export TRUST_PROXY_HEADERS="false"  # 仅处于可信反向代理后时启用

# 出站守卫（防止服务被当成开放代理滥用）
export EGRESS_GUARD="true"           # 默认开启；设为 false 关闭（不推荐）
export EGRESS_ALLOW_PRIVATE="false"  # 允许访问内网地址（NAS/局域网源站时才开）
export EGRESS_ALLOWED_PORTS="80,443" # 出站端口白名单
export MEDIA_MAX_CONCURRENCY="32"    # 音频/图片同时回源上限
```

生成可直接添加到 PinePods 的单源订阅 URL：配置 `PUBLIC_HOST` 和 `PODCAST_PROXY_APIKEY` 后，向 `/links` 发送带 Bearer API Key 的 JSON POST 请求（请求体仅包含源 feed 的 `url`）。返回的 URL 使用仅限该 feed 的 scoped token，不会在 PinePods 中存储代理主 API Key。`/links` 必须通过 HTTPS 或受保护的管理网络访问。

媒体 URL 使用带资源范围的 HMAC token；token 不含 API Key，并保持稳定以兼容 PinePods 的 enclosure URL 去重。代理按最近 feed 刷新登记的 URL 处理媒体，因此支持带短期签名参数的音频 URL 轮换。Feed 查询参数也会参与签名/缓存隔离。Compose 的 `ALLOW_LEGACY_MEDIA_KEY` 默认关闭；需要短期迁移旧链接时才显式开启。

### 本地运行

```bash
go run .
```

### Docker运行

```bash
docker build -t podcast-proxy .
docker run -e PODCAST_PROXY_APIKEY=your-api-key -p 8080:8080 podcast-proxy
```

### Docker Compose运行

```bash
docker-compose up -d
```

运行可选的真实源连通与 PinePods feed 兼容性测试时，使用 `PODCAST_PROXY_LIVE_FEED_TESTS=1 go test -run TestLivePodcastSourcesAgainstPinePods`；该测试访问外部 RSS，默认不启用。

## API 使用

### 1. 链接格式说明

因为已移除了 Web 生成器界面（以保持系统专注在核心 API 的轻量级和安全性），代理链接现在可以直接通过 API 格式或者脚本生成。

### 2. 获取代理RSS源

```
GET /feed?url={原始RSS源URL}&apikey={API Key}
```

**参数:**
- `url` - 原始RSS源URL（必需）
- `apikey` - API密钥（必需）
- `display` - 可选，设为 `1` 时返回服务器渲染的 HTML 播客页面；默认时浏览器请求也会根据 `Accept: text/html` 协商 HTML，播客客户端获得 RSS/Atom XML。设为 `0` 可强制 XML。

管理员生成的单源链接使用 `feed_token` 代替主 API Key，且只允许请求签名绑定的 URL。若设置源站 `Cache-Control: private` 或 `no-store`，代理不会在共享内存缓存中保存该 feed。

**示例:**
```
http://localhost:8080/feed?url=https://example.com/feed.xml&apikey=your-api-key
```

**响应:**
默认返回转换后的 RSS/Atom XML，其中所有媒体 URL 已被替换为代理地址，且不包含 `xml-stylesheet` processing instruction。浏览器打开链接时会协商到原生 HTML 页面；不依赖浏览器的 XSLT 支持，旧浏览器也能直接显示 HTML5 页面和音频链接回退。

### 3. 音频代理 (支持Range请求)

```
GET /audio/{signed-resource-token}/podcast.mp3?url={音频URL}
```

支持以下功能：
- **快速跳转** - `Range`、`If-Range` 会透传到媒体源；支持 `206 Partial Content`、`Content-Range` 与 `Content-Length`
- **缓存验证** - `If-None-Match`、`If-Modified-Since`、`If-Range` 在代理/源站间分别处理，不混用 validator

### 4. 图片代理

```
GET /image/{signed-resource-token}/cover.jpg?url={图片URL}
```

### 5. 样式表代理

```
GET /style/{signed-resource-token}/style.css?url={样式URL}
```

## 缓存策略

### ETag 缓存

RSS/Atom 遵循本项目原有约定：源站提供 ETag 时，订阅响应沿用源站 ETag，客户端的 `If-None-Match` 原样交给源站判断；源站没有 ETag 时，代理才为转换后的内容生成本地 ETag。HTML 展示是独立表示，使用本地 ETag。

1. RSS/Atom 客户端条件请求由源站决定 `304` 或 `200`；源站返回 `304` 时复用已转换的缓存 body。源站返回 `200` 时使用新返回的 feed 内容。
2. HTML 的 validator 对应 HTML 表示本身，不能当作源 RSS 的 validator 转发；过期缓存刷新时由代理使用缓存关联的源站 validator。
3. `Age`、`Date`、`Expires` 会按缓存年龄计算 freshness；`Vary` 按实际转发的上游请求头隔离变体。`Vary: *` 不进入共享缓存，避免在不同表示间错误复用 body。
4. `no-cache` 每次回源验证，`no-store` 不存储。`private` 不进入共享内存缓存、响应也不会声明 `public`。源站若设置 `stale-if-error`，在网络错误或 5xx 时才可在该窗口内提供 stale body，并带 `Warning: 110/111`。
5. `/feed` 不按 Range 返回部分 XML；音频路由透传 Range/If-Range，并转发上游的 206、`Content-Range` 与 `Content-Length`。

```
请求: GET /feed?url=...&apikey=...
    If-None-Match: "origin-etag"

响应: `304 Not Modified`（源站判断 validator 匹配）
```

## 快速跳转（Range请求）

客户端可以发送Range请求来跳转到音频的特定位置：

```
请求: GET /audio/xxx?url=...
      Range: bytes=1000000-2000000

响应: 206 Partial Content
      Content-Range: bytes 1000000-2000000/10000000
      Content-Length: 1000001
```

这样可以：
- ✅ 快速跳转到播客的中间位置
- ✅ 只下载需要的数据，节省流量
- ✅ 支持所有主流播客客户端

## 优化建议

### 1. 性能优化

#### a. 连接池
```go
// proxy.go中已实现
Transport: &http.Transport{
    MaxIdleConns:        100,
    MaxIdleConnsPerHost: 100,
    IdleConnTimeout:     90 * time.Second,
}
```

#### b. 缓存优化
- 使用Redis替代内存缓存（高并发场景）
- 增加缓存TTL以减轻源站压力
- 实现分布式缓存

**建议代码：**
```go
// 使用Redis缓存（未来优化）
type RedisCacheManager struct {
    client *redis.Client
}

func (rcm *RedisCacheManager) Get(key string) (*CacheEntry, bool) {
    val, err := rcm.client.Get(ctx, key).Result()
    // ...
}
```

#### c. CDN集成
- 配置CDN节点缓存媒体资源
- 在PUBLIC_HOST中指向CDN地址
- 自动地理位置分发

### 2. 安全性优化

#### a. 速率限制
```go
// 添加到middleware
type RateLimiter struct {
    limiter *rate.Limiter
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !rl.limiter.Allow() {
            http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

#### b. 请求签名
```go
// 增强认证安全性
type SignedRequest struct {
    timestamp string
    signature string  // HMAC-SHA256
}

func (sr *SignedRequest) Verify(secret string) bool {
    // 计算签名并验证
}
```

#### c. IP白名单
```go
type IPWhitelist struct {
    allowed map[string]bool
}

func (iw *IPWhitelist) Check(r *http.Request) bool {
    // 检查IP是否在白名单
}
```

### 3. 可靠性优化

#### a. 重试机制
```go
type RetryClient struct {
    maxRetries int
    backoff    time.Duration
}

func (rc *RetryClient) Do(req *http.Request) (*http.Response, error) {
    for i := 0; i < rc.maxRetries; i++ {
        resp, err := rc.client.Do(req)
        if err == nil {
            return resp, nil
        }
        if i < rc.maxRetries-1 {
            time.Sleep(rc.backoff * time.Duration(math.Pow(2, float64(i))))
        }
    }
    return nil, errors.New("max retries exceeded")
}
```

#### b. 超时控制
```go
// config.go中已支持
type ProxyConfig struct {
    Timeout int // 秒
}

// 可增加不同资源的不同超时
type TimeoutConfig struct {
    FeedTimeout  int
    AudioTimeout int
    ImageTimeout int
}
```

#### c. 健康检查
```go
type HealthCheck struct {
    lastCheck time.Time
}

func (hc *HealthCheck) Check() bool {
    // 定期检查源站可用性
}
```

### 4. 功能优化

#### a. 代理池轮询
```go
type ProxyPool struct {
    proxies []string
    current int
}

func (pp *ProxyPool) Next() string {
    pp.current = (pp.current + 1) % len(pp.proxies)
    return pp.proxies[pp.current]
}
```

#### b. 统计信息收集
```go
type Stats struct {
    TotalRequests   int64
    CacheHits       int64
    CacheMisses     int64
    TotalBytesProxy int64
    ErrorCount      int64
}

func (s *Stats) RecordRequest(hit bool, bytes int64) {
    s.TotalRequests++
    if hit {
        s.CacheHits++
    } else {
        s.CacheMisses++
    }
    s.TotalBytesProxy += bytes
}
```

#### c. 指标导出（Prometheus）
```go
import "github.com/prometheus/client_golang/prometheus"

var (
    requestCount = prometheus.NewCounterVec(
        prometheus.CounterOpts{Name: "proxy_requests_total"},
        []string{"path", "status"},
    )
    requestDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{Name: "proxy_request_duration_seconds"},
        []string{"path"},
    )
)
```

### 5. 扩展性优化

#### a. 添加新的代理资源类型
```go
// 在 models.go中
type ProxyResource string

const (
    ResourceAudio ProxyResource = "audio"
    ResourceVideo ProxyResource = "video"      // 新增
    ResourceSubtitle ProxyResource = "subtitle" // 新增
)

// 在 handlers.go中添加对应的Handler
type VideoHandler struct {
    *HandlerBase
    cacheManager *CacheManager
}

func NewVideoHandler(r *http.Request) *VideoHandler {
    return &VideoHandler{
        HandlerBase: newHandlerBase(r),
        cacheManager: getCacheManager(),
    }
}

func (vh *VideoHandler) Handle(w http.ResponseWriter, r *http.Request) {
    // 实现视频处理逻辑
}
```

#### b. 支持其他认证方式
```go
// auth.go扩展
type AuthProvider interface {
    Extract(r *http.Request) string
    Verify(key string) bool
}

type BearerTokenAuth struct {
    token string
}

func (bta *BearerTokenAuth) Extract(r *http.Request) string {
    auth := r.Header.Get("Authorization")
    if strings.HasPrefix(auth, "Bearer ") {
        return strings.TrimPrefix(auth, "Bearer ")
    }
    return ""
}

func (bta *BearerTokenAuth) Verify(key string) bool {
    return key == bta.token
}
```

#### c. 支持多源故障转移
```go
type MultiSourceProxy struct {
    sources []string
}

func (msp *MultiSourceProxy) FetchWithFallback(url string) (*http.Response, error) {
    for _, source := range msp.sources {
        proxyURL := msp.rewriteURL(url, source)
        resp, err := http.Get(proxyURL)
        if err == nil && resp.StatusCode < 500 {
            return resp, nil
        }
    }
    return nil, errors.New("all sources failed")
}
```

## 监控和日志

### 日志级别

目前使用标准log包，建议升级为结构化日志：

```go
import "github.com/sirupsen/logrus"

var log = logrus.New()

func init() {
    log.SetFormatter(&logrus.JSONFormatter{})
    log.SetLevel(logrus.InfoLevel)
}

// 使用
log.WithFields(logrus.Fields{
    "method": r.Method,
    "path": r.URL.Path,
    "duration": duration,
}).Info("Request completed")
```

### 调试模式

```bash
# 启用调试日志
DEBUG=1 go run .
```

## 故障排除

### 问题1: 播客客户端无法识别代理源

**解决方案:**
1. 确保RSS Content-Type正确: `application/rss+xml`
2. 检查XML格式是否完整
3. 验证URL编码是否正确

```bash
curl -H "Accept: application/rss+xml" "http://localhost:8080/feed?url=..."
```

### 问题2: 音频播放缓慢

**解决方案:**
1. 检查Range请求是否被正确转发
2. 确认源站支持Range请求 (`Accept-Ranges: bytes`)
3. 考虑使用CDN加速

```bash
curl -I -H "Range: bytes=0-1000" "http://localhost:8080/audio/..."
```

### 问题3: 缓存未生效

**解决方案:**
1. 验证ETag是否被源站返回
2. 检查Cache-Control头
3. 清空本地缓存重试

```bash
curl -v "http://localhost:8080/image/..." | grep -i etag
```

## 贡献指南

欢迎贡献！请：

1. Fork项目
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启Pull Request

## 性能基准

在标准配置下（单核CPU，4GB内存）：

- **吞吐量**: ~1000 req/s
- **平均延迟**: ~50ms
- **缓存命中率**: ~70%（24小时TTL）
- **内存占用**: ~50MB（基础） + 缓存

## 许可证

MIT License - 详见 LICENSE 文件

## 更新日志

### v2.0 (当前)
- ✨ 完整模块化重构
- ✨ 添加ETag缓存支持
- ✨ 完整支持Range请求（快速跳转）
- ✨ 改进错误处理和日志
- ✨ 优化Web UI
- 🐛 修复多个小bug

### v1.0
- 初始版本

## 联系方式

有问题或建议？欢迎提Issue或讨论！

---

**提示**: 在生产环境中，建议：
1. 使用HTTPS/TLS加密通信
2. 配置WAF/反向代理（Nginx/Caddy）
3. 启用日志聚合和监控
4. 定期备份配置
5. 实施容量规划和自动扩展
