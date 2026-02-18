# Podcast Proxy 使用指南

## 快速开始

### 1. 启动容器

```bash
docker run -d \
  --name podcast-proxy \
  -p 80:8080 \
  -e API_KEY="your-secret-key" \
  podcast-proxy:latest
```

### 2. 在浏览器中查看 RSS

访问以下 URL（带 `display=1` 参数）：

```
http://localhost:8080/feed?url=https://www.omnycontent.com/d/playlist/.../podcast.rss&apikey=your-secret-key&display=1
```

浏览器会直接显示格式化的 XML，而不是下载文件。

### 3. 在 RSS 订阅器中添加

访问以下 URL（不带 `display=1` 参数）：

```
http://localhost:8080/feed?url=https://www.omnycontent.com/d/playlist/.../podcast.rss&apikey=your-secret-key
```

复制这个 URL 到你的 RSS 订阅器（Apple Podcasts、Spotify、Pocket Casts 等）。

---

## 工作原理

### Feed 代理流程

```
1. 客户端请求
   ↓
   GET /feed?url=原始RSS&apikey=密钥
   ↓
2. 代理获取原始 RSS
   ↓
   HTTP GET 原始URL
   ↓
3. URL 替换
   ↓
   • 所有图片 URL → http://localhost:8080/image/...
   • 所有音频 URL → http://localhost:8080/audio/...
   • 所有样式 URL → http://localhost:8080/style/...
   ↓
4. 返回修改后的 RSS
   ↓
   Content-Type: application/rss+xml (订阅)
        或 text/xml (浏览)
```

### 音频/图片代理流程

```
1. 订阅器请求音频
   ↓
   GET /audio/密钥?url=原始音频URL
   ↓
2. 代理获取音频
   ↓
   HTTP GET 原始URL
   ↓
3. 处理 302 重定向
   ↓
   如果收到 302，代理会：
   • 拦截 Location 响应头
   • 将新 URL 重新路由到代理
   • 返回新的 302 给客户端
   ↓
4. 转发音频数据
   ↓
   保留所有响应头（Content-Type、Range 等）
   └─ 支持断点续传
```

---

## 功能列表

| 功能 | 支持 | 说明 |
|------|------|------|
| RSS Feed 代理 | ✅ | 完全支持，包括缓存 |
| 播客图片代理 | ✅ | 支持 itunes:image、media:thumbnail 等 |
| 播客音频代理 | ✅ | 支持 enclosure、media:content |
| HTTP 302 重定向 | ✅ | 自动拦截并重新路由 |
| 断点续传 | ✅ | 支持 Range 请求 |
| 两种 Content-Type | ✅ | `application/rss+xml` 和 `text/xml` |
| 浏览器显示 | ✅ | 添加 `display=1` 参数 |
| API Key 认证 | ✅ | Query 参数或 Path 嵌入 |

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|-----|------|
| `API_KEY` | 无 | **必需** - 访问密钥 |
| `PORT` | 8080 | 监听端口 |
| `FORCE_HTTPS` | false | 强制使用 HTTPS 协议 |
| `PUBLIC_HOST` | 无 | 公网域名（用于代理 URL 生成） |

### 示例

```bash
docker run -d \
  --name podcast-proxy \
  -p 8080:8080 \
  -e API_KEY="secure-key-123" \
  -e PUBLIC_HOST="podcast.example.com" \
  -e FORCE_HTTPS="true" \
  podcast-proxy:latest
```

---

## URL 格式说明

### Feed 代理

```
/feed?url=<RSS源URL>&apikey=<密钥>&display=<0|1>

参数说明:
- url      (必需) - 原始 RSS Feed 的 URL，需要 URL 编码
- apikey   (必需) - API 密钥，需与环境变量匹配
- display  (可选) - display=1 时返回 text/xml（浏览器显示）
                不指定时返回 application/rss+xml（订阅用）
```

### 音频代理

```
/audio/<encoded-key>?url=<音频URL>

参数说明:
- encoded-key - Base64 编码的 API 密钥
- url         - 原始音频 URL，需要 URL 编码
```

### 图片代理

```
/image/<encoded-key>?url=<图片URL>

参数说明:
- encoded-key - Base64 编码的 API 密钥
- url         - 原始图片 URL，需要 URL 编码
```

---

## 常见问题

### Q: 为什么浏览器打开代理链接会下载？

**A:** 默认返回 `application/rss+xml` MIME 类型，这会触发浏览器下载。
- 添加 `&display=1` 参数改为 `text/xml` 即可在浏览器显示
- 订阅应用应该使用没有 `display=1` 的链接

### Q: 如何在公网使用？

**A:** 设置 `PUBLIC_HOST` 环境变量：

```bash
docker run -d \
  -e API_KEY="your-key" \
  -e PUBLIC_HOST="podcast.example.com" \
  -e FORCE_HTTPS="true" \
  podcast-proxy:latest
```

### Q: 支持多少并发请求？

**A:** 取决于源服务器和网络。代理本身：
- 使用全局 HTTP 客户端，减少内存占用
- 预编译正则表达式，提高 CPU 效率
- 支持无限并发（受 Go runtime 限制）

### Q: 分页链接会被代理吗？

**A:** 否。按设计，`<atom:link>` 和 `<link>` 保持指向原始源，以保证：
- 完整功能访问（搜索、过滤等）
- 避免无限递归
- 简化代理逻辑

---

## 生产部署建议

### Docker Compose

```yaml
version: '3.8'
services:
  podcast-proxy:
    image: podcast-proxy:latest
    ports:
      - "8080:8080"
    environment:
      API_KEY: ${PODCAST_API_KEY}
      PUBLIC_HOST: podcast.example.com
      FORCE_HTTPS: "true"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/"]
      interval: 30s
      timeout: 10s
      retries: 3
```

### Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: podcast-proxy
spec:
  replicas: 3
  selector:
    matchLabels:
      app: podcast-proxy
  template:
    metadata:
      labels:
   app: podcast-proxy
    spec:
      containers:
      - name: podcast-proxy
        image: podcast-proxy:latest
        ports:
        - containerPort: 8080
        env:
        - name: API_KEY
          valueFrom:
            secretKeyRef:
              name: podcast-proxy-secret
          key: api-key
        - name: PUBLIC_HOST
          value: "podcast.example.com"
        - name: FORCE_HTTPS
          value: "true"
        livenessProbe:
          httpGet:
            path: /
            port: 8080
        initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
          path: /
            port: 8080
          initialDelaySeconds: 5
     periodSeconds: 5
```

---

## 日志分析

容器日志格式：

```
2026/02/18 00:58:42 Podcast proxy 服务启动，监听 :8080
2026/02/18 00:59:02 Start: GET /feed?url=... from 172.17.0.1:37490
2026/02/18 00:59:20 Completed: GET /feed?url=... in 17.958127182s
```

**日志字段**:
- **Start** - 请求开始时间和 URL
- **from** - 客户端 IP
- **Completed** - 请求结束和总耗时

---

## 性能优化

### 缓存

代理会自动缓存原始 RSS Feed：
- 首次请求：~18 秒
- 后续请求：~500 毫秒（快 36 倍）

缓存遵循源服务器的 Cache-Control 响应头。

### 优化技巧

1. **使用公网域名**
   ```bash
   -e PUBLIC_HOST="podcast.example.com"
   ```
   避免 URL 生成时出现内部 IP

2. **启用 HTTPS**
   ```bash
   -e FORCE_HTTPS="true"
   ```
   确保所有代理 URL 使用 HTTPS

3. **增加副本数**
   ```bash
   replicas: 5  # Kubernetes
   ```
   分散负载，提高吞吐量

---

## 许可证

MIT License

---

## 支持

如有问题，请：
1. 检查容器日志：`docker logs podcast-proxy`
2. 查看测试报告：[TEST_REPORT.md](TEST_REPORT.md)
3. 验证 API Key：`-e API_KEY="your-key"`
