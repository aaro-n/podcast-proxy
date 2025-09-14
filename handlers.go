// handlers.go  
package main  
  
import (  
    "crypto/sha256"  
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
    userAPIKey := getUserAPIKey(r)  
      
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
      
    body, err := io.ReadAll(resp.Body)  
    if err != nil {  
        log.Printf("Failed to read feed body: %v", err)  
        http.Error(w, fmt.Sprintf("Failed to read original feed body: %v", err), http.StatusInternalServerError)  
        return  
    }  
      
    var feed RSS  
    if err := xml.Unmarshal(body, &feed); err != nil {  
        log.Printf("Failed to parse XML: %v", err)  
        http.Error(w, fmt.Sprintf("Failed to parse feed XML: %v", err), http.StatusInternalServerError)  
        return  
    }  
      
    // URL 重写  
    baseURL := getBaseURL(r)  
    if feed.Channel.Image.URL != "" {  
        feed.Channel.Image.URL = createProxyURL(baseURL, feed.Channel.Image.URL, userAPIKey)  
    }  
    if feed.Channel.ITunesImage.Href != "" {  
        feed.Channel.ITunesImage.Href = createProxyURL(baseURL, feed.Channel.ITunesImage.Href, userAPIKey)  
    }  
      
    for i := range feed.Channel.Items {  
        item := &feed.Channel.Items[i]  
        if item.Enclosure.URL != "" {  
            item.Enclosure.URL = createProxyURL(baseURL, item.Enclosure.URL, userAPIKey)  
        }  
        if item.ITunesImage.Href != "" {  
            item.ITunesImage.Href = createProxyURL(baseURL, item.ITunesImage.Href, userAPIKey)  
        }  
    }  
      
    newXML, err := xml.MarshalIndent(feed, "", "  ")  
    if err != nil {  
        log.Printf("Failed to marshal XML: %v", err)  
        http.Error(w, fmt.Sprintf("Failed to generate new XML: %v", err), http.StatusInternalServerError)  
        return  
    }  
      
    // 添加 XML 头部  
    fullXML := []byte(xml.Header + string(newXML))  
      
    // 缓存结果  
    cache.Set(cacheKey, fullXML, config.CacheExpiration)  
      
    w.Header().Set("Content-Type", "application/xml; charset=utf-8")  
    w.Write(fullXML)  
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
    <h1>播客  
  
Wiki pages you might want to explore:  
- [System Architecture (aaro-n/podcast-proxy)](/wiki/aaro-n/podcast-proxy#1.1)
