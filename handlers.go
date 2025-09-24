// handlers.go
package main
 
import (
    "compress/gzip"
    "crypto/sha256"
    "encoding/base64"
    "encoding/xml"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "regexp"
    "strings"
    "time"
)
 
// min 辅助函数，用于获取两个整数中的较小值
func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
 
// rewriteURLsInXML 在XML内容中重写URL
func rewriteURLsInXML(xmlContent, baseURL, authType, authInfo string) string {
    result := xmlContent
    
    // 构建代理URL前缀
    proxyPrefix := baseURL + "/proxy?"
    if authType == "apikey" {
        proxyPrefix += "apikey=" + authInfo + "&url="
    } else if authType == "userpass" {
        creds := strings.SplitN(authInfo, ":", 2)
        if len(creds) == 2 {
            proxyPrefix += "username=" + creds[0] + "&password=" + creds[1] + "&url="
        }
    }
    
    // 使用正则表达式匹配并替换URL
    // 1. 替换enclosure标签中的url属性
    enclosurePattern := regexp.MustCompile(`(<enclosure\s+url=")([^"]+)`)
    result = enclosurePattern.ReplaceAllString(result, `$1`+proxyPrefix+`$2`)
    
    // 2. 替换image标签中的url
    imageURLPattern := regexp.MustCompile(`(<image>\s*<url>)([^<]+)`)
    result = imageURLPattern.ReplaceAllString(result, `$1`+proxyPrefix+`$2`)
    
    // 3. 替换itunes:image标签中的href属性
    itunesImagePattern := regexp.MustCompile(`(<itunes:image\s+href=")([^"]+)`)
    result = itunesImagePattern.ReplaceAllString(result, `$1`+proxyPrefix+`$2`)
    
    // 4. 替换channel中的image url
    channelImagePattern := regexp.MustCompile(`(<image>\s*<url>)([^<]+)`)
    result = channelImagePattern.ReplaceAllString(result, `$1`+proxyPrefix+`$2`)
    
    // 5. 替换item中的image url
    itemImagePattern := regexp.MustCompile(`(<item>.*?<image>\s*<url>)([^<]+)`)
    result = itemImagePattern.ReplaceAllString(result, `$1`+proxyPrefix+`$2`)
    
    return result
}
 
func feedHandler(w http.ResponseWriter, r *http.Request) {
    originalFeedURL := r.URL.Query().Get("url")
    
    if originalFeedURL == "" {
        http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
        return
    }
    
    // URL 验证
    if !isValidURL(originalFeedURL) {
        http.Error(w, "Invalid URL format", http.StatusBadRequest)
        return
    }
    
    // 检查缓存
    cacheKey := fmt.Sprintf("feed:%x", sha256.Sum256([]byte(originalFeedURL)))
    if cachedData, found := cache.Get(cacheKey); found {
        log.Printf("Cache hit for feed: %s", originalFeedURL)
        w.Header().Set("Content-Type", "application/xml; charset=utf-8")
        w.Write(cachedData)
        return
    }
    
    log.Printf("Fetching original feed: %s", originalFeedURL)
    
    // 带重试的请求
    var resp *http.Response
    var err error
    
    for attempt := 0; attempt < config.MaxRetries; attempt++ {
        if attempt > 0 {
            time.Sleep(time.Duration(attempt) * time.Second)
            log.Printf("Retry attempt %d for %s", attempt, originalFeedURL)
        }
        
        req, reqErr := http.NewRequest("GET", originalFeedURL, nil)
        if reqErr != nil {
            http.Error(w, fmt.Sprintf("Failed to create request: %v", reqErr), http.StatusInternalServerError)
            return
        }
        
        // 设置更完整的请求头
        req.Header.Set("User-Agent", "podcast-proxy/1.0")
        req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml, */*")
        req.Header.Set("Accept-Encoding", "gzip, deflate")
        req.Header.Set("Cache-Control", "no-cache")
        
        resp, err = httpClient.Do(req)
        if err == nil && resp.StatusCode == http.StatusOK {
            // 检查内容类型
            contentType := resp.Header.Get("Content-Type")
            log.Printf("Response Content-Type: %s", contentType)
            
            // 如果内容类型不是XML/RSS相关，记录警告但继续尝试解析
            if !strings.Contains(strings.ToLower(contentType), "xml") && 
               !strings.Contains(strings.ToLower(contentType), "rss") {
                log.Printf("Warning: Unexpected Content-Type: %s for URL: %s", contentType, originalFeedURL)
            }
            break
        }
        
        if resp != nil {
            log.Printf("Request failed with status: %s", resp.Status)
            resp.Body.Close()
        }
    }
    
    if err != nil {
        log.Printf("Failed to fetch feed after %d attempts: %v", config.MaxRetries, err)
        http.Error(w, fmt.Sprintf("Failed to fetch original feed: %v", err), http.StatusInternalServerError)
        return
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        log.Printf("Feed returned non-200 status: %s for %s", resp.Status, originalFeedURL)
        http.Error(w, fmt.Sprintf("Original feed returned status: %s", resp.Status), http.StatusBadGateway)
        return
    }
    
    // === 新增：转发原始响应标头 ===
    // 复制原始响应的标头，但跳过可能干扰的头部
    for key, values := range resp.Header {
        // 跳过这些头部，因为它们会在内容处理完成后自动设置
        skipHeaders := map[string]bool{
            "Content-Length":     true,
            "Transfer-Encoding":  true,
            "Content-Encoding":   true,
            "Connection":         true,
            "Keep-Alive":         true,
            "Proxy-Authenticate": true,
            "Proxy-Authorization": true,
            "TE":                true,
            "Trailers":          true,
            "Upgrade":           true,
        }
        
        if !skipHeaders[key] {
            for _, value := range values {
                w.Header().Add(key, value)
            }
        }
    }
    // === 标头转发结束 ===
    
    // 处理压缩内容
    var reader io.Reader = resp.Body
    contentEncoding := resp.Header.Get("Content-Encoding")
    
    if strings.Contains(strings.ToLower(contentEncoding), "gzip") {
        log.Printf("Decompressing gzip content")
        gzipReader, err := gzip.NewReader(resp.Body)
        if err != nil {
            log.Printf("Failed to create gzip reader: %v", err)
            http.Error(w, fmt.Sprintf("Failed to decompress response: %v", err), http.StatusInternalServerError)
            return
        }
        defer gzipReader.Close()
        reader = gzipReader
    }
    
    body, err := io.ReadAll(reader)
    if err != nil {
        log.Printf("Failed to read feed body: %v", err)
        http.Error(w, fmt.Sprintf("Failed to read original feed body: %v", err), http.StatusInternalServerError)
        return
    }
    
    // 添加调试日志 - 记录响应内容预览
    bodyPreview := string(body)
    if len(bodyPreview) > 500 {
        bodyPreview = bodyPreview[:500]
    }
    log.Printf("Response body preview: %s", bodyPreview)
    log.Printf("Response body length: %d bytes", len(body))
    
    // 检查是否为有效的XML内容
    trimmedBody := strings.TrimSpace(string(body))
    if !strings.HasPrefix(trimmedBody, "<") {
        log.Printf("Error: Response does not start with XML tag. First 100 chars: %s", trimmedBody[:min(100, len(trimmedBody))])
        http.Error(w, "Response is not valid XML content", http.StatusInternalServerError)
        return
    }
    
    // 获取认证信息
    authType, authInfo := getAuthInfo(r)
    baseURL := getBaseURL(r)
    
    // 使用字符串替换重写URL，保持原始XML结构
    modifiedContent := rewriteURLsInXML(string(body), baseURL, authType, authInfo)
    
    log.Printf("URL rewriting completed. Modified content length: %d bytes", len(modifiedContent))
    
    // 缓存结果
    cache.Set(cacheKey, []byte(modifiedContent), config.CacheExpiration)
    
    // 确保设置正确的Content-Type（可能会覆盖从原始响应复制的）
    w.Header().Set("Content-Type", "application/xml; charset=utf-8")
    w.Write([]byte(modifiedContent))
}
 
func proxyHandler(w http.ResponseWriter, r *http.Request) {
    targetURL := r.URL.Query().Get("url")
    if targetURL == "" {
        http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
        return
    }
    
    if !isValidURL(targetURL) {
        http.Error(w, "Invalid URL format", http.StatusBadRequest)
        return
    }
    
    log.Printf("Proxying media: %s", targetURL)
    
    req, err := http.NewRequest("GET", targetURL, nil)
    if err != nil {
        log.Printf("Failed to create proxy request: %v", err)
        http.Error(w, fmt.Sprintf("Failed to create request for target URL: %v", err), http.StatusInternalServerError)
        return
    }
    
    // 复制相关头部
    if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
        req.Header.Set("Range", rangeHeader)
    }
    if userAgent := r.Header.Get("User-Agent"); userAgent != "" {
        req.Header.Set("User-Agent", userAgent)
    } else {
        req.Header.Set("User-Agent", "podcast-proxy/1.0")
    }
    
    // 添加Accept-Encoding支持
    req.Header.Set("Accept-Encoding", "gzip, deflate")
    
    resp, err := httpClient.Do(req)
    if err != nil {
        log.Printf("Failed to fetch target URL: %v", err)
        http.Error(w, fmt.Sprintf("Failed to fetch target URL: %v", err), http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()
    
    // 复制响应头部
    for key, values := range resp.Header {
        for _, value := range values {
            w.Header().Add(key, value)
        }
    }
    
    w.WriteHeader(resp.StatusCode)
    
    // 处理压缩响应
    var reader io.Reader = resp.Body
    contentEncoding := resp.Header.Get("Content-Encoding")
    
    if strings.Contains(strings.ToLower(contentEncoding), "gzip") {
        gzipReader, err := gzip.NewReader(resp.Body)
        if err != nil {
            log.Printf("Failed to create gzip reader for proxy: %v", err)
            return
        }
        defer gzipReader.Close()
        reader = gzipReader
    }
    
    // 流式复制响应体
    _, err = io.Copy(w, reader)
    if err != nil {
        log.Printf("Error copying response body: %v", err)
    }
}
 
func indexHandler(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/" {
        http.NotFound(w, r)
        return
    }
    
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    
    html := `<!DOCTYPE html>
<html>
<head>
    <title>播客 RSS 代理服务器</title>
    <meta charset="utf-8">
</head>
<body>
    <h1>播客 RSS 代理服务器</h1>
    <p>这是一个播客 RSS 代理服务器，用于代理播客内容。</p>
    <h2>使用方法</h2>
    <h3>API Key 认证（优先）</h3>
    <p>访问 <code>/feed?url=&lt;RSS_URL&gt;&amp;apikey=&lt;API_KEY&gt;</code> 来获取代理后的 RSS feed。</p>
    <p>访问 <code>/proxy?url=&lt;MEDIA_URL&gt;&amp;apikey=&lt;API_KEY&gt;</code> 来代理媒体文件。</p>
    <h3>用户认证</h3>
    <p>访问 <code>/feed?url=&lt;RSS_URL&gt;&amp;username=&lt;USERNAME&gt;&amp;password=&lt;PASSWORD&gt;</code></p>
    <p>或者使用 HTTP Basic Authentication：</p>
    <p><code>curl -u username:password "http://localhost:8080/feed?url=&lt;RSS_URL&gt;"</code></p>
</body>
</html>`
    
    fmt.Fprint(w, html)
}
 
// --- 辅助函数 (Helpers) ---
 
// 获取当前认证类型和认证信息
func getAuthInfo(r *http.Request) (authType string, authInfo string) {
    // 首先检查API Key认证
    var userAPIKey string
    authHeader := r.Header.Get("Authorization")
    if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
        userAPIKey = strings.TrimPrefix(authHeader, "Bearer ")
    } else {
        userAPIKey = r.URL.Query().Get("apikey")
    }
    
    if userAPIKey != "" {
        return "apikey", userAPIKey
    }
    
    // 然后检查用户认证
    var username, password string
    
    // 尝试从 Authorization header 获取 Basic Auth
    if authHeader != "" && strings.HasPrefix(authHeader, "Basic ") {
        encoded := strings.TrimPrefix(authHeader, "Basic ")
        if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
            creds := strings.SplitN(string(decoded), ":", 2)
            if len(creds) == 2 {
                username = creds[0]
                password = creds[1]
            }
        }
    }
    
    // 如果没有从 header 获取到，尝试从 URL 参数获取
    if username == "" {
        username = r.URL.Query().Get("username")
        password = r.URL.Query().Get("password")
    }
    
    if username != "" && password != "" {
        return "userpass", fmt.Sprintf("%s:%s", username, password)
    }
    
    return "", ""
}
 
// 修改 createProxyURL 函数以支持不同的认证方式
func createProxyURL(baseURL, targetURL string, authType string, authInfo string) string {
    if targetURL == "" {
        return ""
    }
    
    proxyURL, _ := url.Parse(baseURL + "/proxy")
    params := url.Values{}
    params.Set("url", targetURL)
    
    // 根据认证类型添加相应的认证参数
    switch authType {
    case "apikey":
        params.Set("apikey", authInfo)
    case "userpass":
        // 将用户名和密码分开存储
        creds := strings.SplitN(authInfo, ":", 2)
        if len(creds) == 2 {
            params.Set("username", creds[0])
            params.Set("password", creds[1])
        }
    }
    
    proxyURL.RawQuery = params.Encode()
    return proxyURL.String()
}
 
func getBaseURL(r *http.Request) string {
    scheme := "http"
    if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
        scheme = "https"
    }
    return fmt.Sprintf("%s://%s", scheme, r.Host)
}
 
func isValidURL(urlStr string) bool {
    if urlStr == "" {
        return false
    }
    
    parsedURL, err := url.Parse(urlStr)
    if err != nil {
        return false
    }
    
    // 检查协议
    if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
        return false
    }
    
    // 检查主机名
    if parsedURL.Host == "" {
        return false
    }
    
    // 防止本地地址访问 (SSRF 防护)
    if strings.Contains(parsedURL.Host, "localhost") || 
       strings.Contains(parsedURL.Host, "127.0.0.1") ||  
       strings.Contains(parsedURL.Host, "::1") {
        return false
    }
    
    return true
}
 
func getUserAPIKey(r *http.Request) string {
    // 优先从 Authorization header 获取
    authHeader := r.Header.Get("Authorization")
    if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
        return strings.TrimPrefix(authHeader, "Bearer ")
    }
    
    // 回退到 URL 参数
    return r.URL.Query().Get("apikey")
}
