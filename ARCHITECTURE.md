# 架构设计文档

## 模块划分

### 1. **main.go** - 程序入口
**职责**: 初始化应用，启动服务器

```
初始化配置 → 创建服务器 → 注册路由 → 启动监听
```

### 2. **config.go** - 配置管理
**职责**: 环境变量读取、全局配置管理

**主要结构**:
- `ProxyConfig` - 应用配置
- `InitConfig()` - 初始化配置
- `GetConfig()` - 获取全局配置
- `GetHTTPClient()` - 获取HTTP客户端

**特点**:
- 单例模式管理HTTP客户端
- 支持超时配置
- 自动读取环境变量

### 3. **models.go** - 数据模型
**职责**: 定义所有数据结构

**主要结构**:
- `ProxyConfig` - 代理配置
- `RequestContext` - 请求上下文
- `CacheEntry` - 缓存条目
- `ProxyResource` - 资源类型枚举
- `FeedTransformResult` - RSS转换结果

### 4. **auth.go** - 认证管理
**职责**: API Key认证、提取、验证

**主要类**:
- `AuthManager` - 认证管理器
  - `ExtractAPIKey()` - 从请求提取Key（支持query/path）
  - `VerifyAPIKey()` - 验证Key有效性
  - `EncodeKey()` - Base64编码
  - `decodeKey()` - Base64解码

**特点**:
- 支持多种提取方式（query参数、path参数）
- 自动Base64编解码
- 提高安全性

### 5. **utils.go** - 工具函数
**职责**: 通用工具和辅助函数

**主要类**:
- `ProxyURLBuilder` - URL构建
  - 自动判断HTTP/HTTPS
  - 支持自定义公开域名
  - XML属性转义

- `HeaderCopier` - 响应头复制
  - 支持选择性复制
  - 过滤不适合的头

- `LoggerHelper` - 日志助手
  - 记录请求开始/完成
  - 计算耗时

- `StringHelper` - 字符串工具
  - URL类型判断
  - HTML实体解码

- `RangeHelper` - Range请求处理
  - 解析Range头
  - 返回字节范围

### 6. **cache.go** - 缓存管理
**职责**: ETag缓存、HTTP缓存控制

**主要类**:
- `CacheManager` - 缓存管理
  - LRU过期策略
  - 自动清理
  - 线程安全

- `ETagHelper` - ETag生成/验证
  - 基于内容哈希
  - 支持弱ETag
  - ETag匹配判断

- `CacheResponseHelper` - 缓存响应助手
  - 处理If-None-Match
  - 设置缓存头
  - 返回304判断

**特点**:
- 24小时TTL
- 自动过期清理
- 支持ETag缓存
- 线程安全

### 7. **feed.go** - RSS处理
**职责**: RSS源解析、转换、验证

**主要类**:
- `FeedTransformer` - RSS转换器
  - 预编译正则表达式
  - 支持多种标签格式
  - 自动类型判断

  转换规则:
  1. `<enclosure>` → 音频代理
  2. `<itunes:image>` → 图片代理
  3. `<image><url>` → 图片代理
  4. `<media:thumbnail>` → 图片代理
  5. `<media:content>` → 智能判断
  6. `<?xml-stylesheet>` → 样式代理

- `FeedValidator` - RSS源验证
  - 验证URL格式
  - 验证RSS内容完整性

- `FeedMetadata` - 元数据提取
  - 提取标题
  - 提取描述
  - 提取构建时间

**特点**:
- 性能优化：预编译正则
- 智能类型判断
- 安全的XML转义

### 8. **proxy.go** - HTTP代理
**职责**: HTTP请求执行、重定向、流式处理

**主要类**:
- `HTTPClientManager` - HTTP客户端管理
  - 单例模式
  - 连接池配置
  - 超时控制

- `ProxyRequest` - 代理请求
  - 执行实际请求
  - 转发Range头（快速跳转）
  - 转发ETag请求

- `ProxyResponse` - 代理响应
  - 复制响应头
  - 处理重定向
  - 写入响应体

- `StreamRequest` - 流式请求
  - 分块处理大文件
  - 支持Range请求
  - 节省内存

**特点**:
- 连接复用
- 完整Range支持
- 智能重定向处理
- 流式处理大文件

### 9. **handlers.go** - HTTP处理器
**职责**: 各类HTTP请求的处理逻辑

**主要类**:
- `HandlerBase` - 处理器基类
  - 公共初始化
  - 认证管理器
  - 日志记录

- `FeedHandler` - RSS源处理
  - URL验证
  - RSS转换
  - 内容类型设置

- `AudioHandler` - 音频处理
  - Range请求支持
  - ETag缓存
  - 重定向处理

- `ImageHandler` - 图片处理
  - 图片缓存
  - 重定向处理

- `StyleHandler` - 样式处理
  - 样式转发
  - 重定向处理

- `NotFoundHandler` - 404处理

**特点**:
- 统一的处理流程
- 完整的错误处理
- 中心化的认证
- 详细的日志记录

### 10. **server.go** - 服务器管理
**职责**: HTTP服务器配置、路由注册、中间件

**主要类**:
- `Server` - 服务器
  - 路由注册
  - 服务启动
  - 中间件支持

**中间件**:
- `logMiddleware` - 日志中间件
- `CORSMiddleware` - CORS中间件（可选）
- `CompressionMiddleware` - 压缩中间件（可选）

## 数据流

### RSS源转换流程

```
用户请求
  ↓
/feed 处理器
  ↓
FeedHandler.Handle()
  ├─ 验证API Key (AuthManager)
  ├─ 验证URL (FeedValidator)
  ├─ 获取源RSS (ProxyRequest)
  ├─ 验证内容 (FeedValidator)
  ├─ 转换RSS (FeedTransformer)
  │  ├─ 替换enclosure
  │  ├─ 替换iTunes图片
  │  ├─ 替换image URL
  │  ├─ 替换media:thumbnail
  │  ├─ 替换media:content
  │  └─ 替换xml-stylesheet
  ├─ 构建URL (ProxyURLBuilder)
  └─ 返回转换后的RSS
```

### 音频代理流程（支持Range）

```
客户端请求 (可能带Range头)
  ↓
/audio 处理器
  ↓
AudioHandler.Handle()
  ├─ 验证API Key
  ├─ 获取源URL
  ├─ 创建代理请求 (ProxyRequest)
  │  ├─ 转发Range头 ← 快速跳转关键
  │  └─ 转发If-None-Match ← 缓存验证
  ├─ 获取源响应 (ProxyResponse)
  ├─ 处理重定向
  ├─ 复制响应头
  └─ 写入响应体
```

### 缓存流程（ETag 直接转发）

```
客户端请求 (带 If-None-Match)
  ↓
FeedHandler (直接在 ProxyRequest 中转发缓存头给源站)
  ↓
源站进行缓存决策
  ↓
源站返回 304?
  ├─ 是 → 代理直接转发 304 给客户端 (0 字节，极速短路)
  └─ 否 (返回 200) → 接收新内容 → 转换 RSS → 将源站 ETag 附加在响应头 → 返回给客户端 (客户端更新本地缓存)
```

## 扩展点

### 1. 添加新的资源类型

1. 在 `models.go` 中扩展 `ProxyResource` 枚举
2. 在 `handlers.go` 中创建新的Handler
3. 在 `server.go` 中注册路由
4. 在 `utils.go` 中添加对应的URL构建方法

### 2. 实现新的认证方式

1. 在 `auth.go` 中创建新的认证器（实现AuthProvider接口）
2. 在 `handlers.go` 中使用新的认证器

### 3. 支持新的缓存后端

1. 创建新的缓存管理类（实现CacheManager接口）
2. 在 `config.go` 中配置缓存实现

### 4. 添加监控和指标

1. 在需要的地方注入指标收集
2. 在 `server.go` 中添加指标端点

## 性能考虑

### 1. 内存

- `CacheManager` 使用内存缓存，建议高并发时改用Redis
- 响应头复制使用流式处理，避免大对象内存占用

### 2. CPU

- 正则表达式预编译，避免每次编译开销
- 使用goroutine处理并发请求

### 3. 网络

- HTTP连接池复用
- Range请求支持，减少数据传输
- ETag缓存，避免重复下载

## 测试建议

```bash
# 测试RSS转换
curl "http://localhost:8080/feed?url=https://example.com/feed.xml&apikey=test-key"

# 测试Range请求
curl -H "Range: bytes=0-1000" "http://localhost:8080/audio/..."

# 测试ETag缓存
curl -i "http://localhost:8080/image/..." | grep ETag
curl -H "If-None-Match: \"abc123\"" "http://localhost:8080/image/..."

# 测试认证
curl "http://localhost:8080/feed?url=..." # 应返回401

# 性能测试
ab -n 1000 -c 10 "http://localhost:8080/feed?url=...&apikey=..."
```

## 监控指标

建议收集以下指标：

- 请求总数 (按路径/状态码)
- 请求延迟 (p50/p95/p99)
- 缓存命中率
- 错误率
- 活跃连接数
- 内存占用
- CPU使用率

## 安全建议

1. **认证**: 使用强API Key，考虑使用HMAC-SHA256签名
2. **授权**: 实现API Key的权限管理
3. **速率限制**: 防止恶意请求
4. **日志审计**: 记录所有关键操作
5. **HTTPS**: 在生产环境使用TLS
6. **IP白名单**: 限制来源IP
