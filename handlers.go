// handlers.go
package main
 
import (
    "bytes"
    "crypto/sha256"
    "encoding/base64"
    "encoding/xml"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/url"
    "strings"
    "time"
)
 
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
        
        req.Header.Set("User-Agent", "podcast-proxy/1.0")
        
        resp, err = httpClient.Do(req)
        if err == nil && resp.StatusCode == http.StatusOK {
            break
        }
        
        if resp != nil {
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
    
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        log.Printf("Failed to read feed body: %v", err)
        http.Error(w, fmt.Sprintf("Failed to read original feed body: %v", err), http.StatusInternalServerError)
        return
    }
    
    // 获取认证信息并URL重写
    authType, authInfo := getAuthInfo(r)
    baseURL := getBaseURL(r)
    
    // 使用新的XML处理方式，只替换URL，保持其他内容不变
    processedXML, err := processRSSFeed(body, baseURL, authType, authInfo)
    if err != nil {
        log.Printf("Failed to process RSS feed: %v", err)
        http.Error(w, fmt.Sprintf("Failed to process RSS feed: %v", err), http.StatusInternalServerError)
        return
    }
    
    // 缓存结果
    cache.Set(cacheKey, processedXML, config.CacheExpiration)
    
    // 确保设置正确的Content-Type（可能会覆盖从原始响应复制的）
    w.Header().Set("Content-Type", "application/xml; charset=utf-8")
    w.Write(processedXML)
}
 
// processRSSFeed 处理RSS XML，只替换特定的URL，保持其他内容不变
func processRSSFeed(originalXML []byte, baseURL, authType, authInfo string) ([]byte, error) {
    decoder := xml.NewDecoder(bytes.NewReader(originalXML))
    var buf bytes.Buffer
    encoder := xml.NewEncoder(&buf)
    encoder.Indent("", "  ") // 保持格式化
    
    for {
        token, err := decoder.Token()
        if err == io.EOF {
            break
        }
        if err != nil {
            return nil, fmt.Errorf("XML decode error: %v", err)
        }
        
        switch t := token.(type) {
        case xml.StartElement:
            // 检查是否是需要替换URL的标签
            if shouldReplaceURL(t) {
                // 读取内容并替换URL
                var content string
                if err := decoder.DecodeElement(&content, &t); err != nil {
                    return nil, fmt.Errorf("failed to decode element: %v", err)
                }
                
                // 只替换非空URL
                if strings.TrimSpace(content) != "" {
                    newURL := createProxyURL(baseURL, content, authType, authInfo)
                    // 修复：传递 t 而不是 &t
                    if err := encoder.EncodeElement(newURL, t); err != nil {
                        return nil, fmt.Errorf("failed to encode element: %v", err)
                    }
                } else {
                    // 如果内容为空，原样写入
                    // 修复：传递 &t 而不是 t
                    if err := encoder.EncodeElement(content, t); err != nil {
                        return nil, fmt.Errorf("failed to encode empty element: %v", err)
                    }
                }
                continue
            }
            
            // 检查是否是包含URL属性的标签
            if shouldReplaceAttributeURL(t) {
                // 处理属性
                modifiedElement := t
                for i, attr := range modifiedElement.Attr {
                    if shouldReplaceAttributeName(modifiedElement.Name.Local, attr.Name.Local) {
                        if strings.TrimSpace(attr.Value) != "" {
                            modifiedElement.Attr[i].Value = createProxyURL(baseURL, attr.Value, authType, authInfo)
                        }
                    }
                }
                
                // 读取子内容
                var content interface{}
                if err := decoder.DecodeElement(&content, &t); err != nil {
                    return nil, fmt.Errorf("failed to decode element with attributes: %v", err)
                }
                
                // 写入修改后的开始标签和内容
                if err := encoder.EncodeElement(content, modifiedElement); err != nil {
                    return nil, fmt.Errorf("failed to encode element with modified attributes: %v", err)
                }
                continue
            }
            
        case xml.EndElement:
            // 正常处理结束标签
            if err := encoder.EncodeToken(t); err != nil {
                return nil, fmt.Errorf("failed to encode end element: %v", err)
            }
            continue
            
        case xml.CharData:
            // 正常处理文本内容
            if err := encoder.EncodeToken(t); err != nil {
                return nil, fmt.Errorf("failed to encode char data: %v", err)
            }
            continue
            
        case xml.Comment:
            // 保留注释
            if err := encoder.EncodeToken(t); err != nil {
                return nil, fmt.Errorf("failed to encode comment: %v", err)
            }
            continue
            
        case xml.ProcInst:
            // 保留处理指令（如XML声明）
            if err := encoder.EncodeToken(t); err != nil {
                return nil, fmt.Errorf("failed to encode proc inst: %v", err)
            }
            continue
        }
        
        // 默认情况：原样写入token
        if err := encoder.EncodeToken(token); err != nil {
            return nil, fmt.Errorf("failed to encode token: %v", err)
        }
    }
    
    // 结束编码
    if err := encoder.Flush(); err != nil {
        return nil, fmt.Errorf("failed to flush encoder: %v", err)
    }
    
    // 添加XML头部
    result := buf.Bytes()
    if !bytes.HasPrefix(result, []byte("<?xml")) {
        result = append([]byte(xml.Header), result...)
    }
    
    return result, nil
}
 
// shouldReplaceURL 判断是否需要替换该标签的内容
func shouldReplaceURL(element xml.StartElement) bool {
    // 检查标签名
    tagName := element.Name.Local
    
    // 需要替换URL的标签列表
    urlTags := map[string]bool{
        "url":   true,  // image/url
        "href":  true,  // itunes:image@href
    }
    
    return urlTags[tagName]
}
 
// shouldReplaceAttributeURL 判断是否需要替换该标签的属性URL
func shouldReplaceAttributeURL(element xml.StartElement) bool {
    tagName := element.Name.Local
    
    // 需要替换属性URL的标签列表
    attrURLTags := map[string]bool{
        "enclosure": true,  // enclosure@url
        "image":     true,  // image@url (某些格式)
    }
    
    return attrURLTags[tagName]
}
 
// shouldReplaceAttributeName 判断是否需要替换该属性名
func shouldReplaceAttributeName(tagName, attrName string) bool {
    // 需要替换的属性名列表
    urlAttrs := map[string]bool{
        "url":  true,  // enclosure@url, image@url
        "href": true,  // itunes:image@href
    }
    
    return urlAttrs[attrName]
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
    
    // 流式复制响应体
    _, err = io.Copy(w, resp.Body)
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
 
// createProxyURL 创建代理URL
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
