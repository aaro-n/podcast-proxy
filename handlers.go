// handlers.go
package main

import (
        "encoding/xml"
        "fmt"
        "io"
        "log"
        "net/http"
        "net/url"
)

// --- 处理器 (Handlers) ---

// feedHandler 处理 RSS 代理请求
func feedHandler(w http.ResponseWriter, r *http.Request) {
        originalFeedURL := r.URL.Query().Get("url")
        userAPIKey := r.URL.Query().Get("apikey")

        if originalFeedURL == "" {
                http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
                return
        }

        log.Printf("Fetching original feed: %s", originalFeedURL)

        resp, err := http.Get(originalFeedURL)
        if err != nil {
                http.Error(w, fmt.Sprintf("Failed to fetch original feed: %v", err), http.StatusInternalServerError)
                return
        }
        defer resp.Body.Close()

        if resp.StatusCode != http.StatusOK {
                http.Error(w, fmt.Sprintf("Original feed returned status: %s", resp.Status), http.StatusBadGateway)
                return
        }

        body, err := io.ReadAll(resp.Body)
        if err != nil {
                http.Error(w, fmt.Sprintf("Failed to read original feed body: %v", err), http.StatusInternalServerError)
                return
        }

        var feed RSS
        if err := xml.Unmarshal(body, &feed); err != nil {
                http.Error(w, fmt.Sprintf("Failed to parse feed XML: %v", err), http.StatusInternalServerError)
                return
        }

        baseURL := getBaseURL(r)
        feed.Channel.Image.URL = createProxyURL(baseURL, feed.Channel.Image.URL, userAPIKey)
        feed.Channel.ITunesImage.Href = createProxyURL(baseURL, feed.Channel.ITunesImage.Href, userAPIKey)

        for i := range feed.Channel.Items {
                item := &feed.Channel.Items[i]
                item.Enclosure.URL = createProxyURL(baseURL, item.Enclosure.URL, userAPIKey)
                item.ITunesImage.Href = createProxyURL(baseURL, item.ITunesImage.Href, userAPIKey)
        }

        newXML, err := xml.MarshalIndent(feed, "", "  ")
        if err != nil {
                http.Error(w, fmt.Sprintf("Failed to generate new XML: %v", err), http.StatusInternalServerError)
                return
        }

        w.Header().Set("Content-Type", "application/xml; charset=utf-8")
        w.Write([]byte(xml.Header))
        w.Write(newXML)
}

// proxyHandler 代理媒体文件（图片/音频）
func proxyHandler(w http.ResponseWriter, r *http.Request) {
        targetURL := r.URL.Query().Get("url")
        if targetURL == "" {
                http.Error(w, "Missing 'url' query parameter", http.StatusBadRequest)
                return
        }

        log.Printf("Proxying media: %s", targetURL)

        req, err := http.NewRequest("GET", targetURL, nil)
        if err != nil {
                http.Error(w, fmt.Sprintf("Failed to create request for target URL: %v", err), http.StatusInternalServerError)
                return
        }

        if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
                req.Header.Set("Range", rangeHeader)
        }
        req.Header.Set("User-Agent", r.UserAgent())

        client := &http.Client{}
        resp, err := client.Do(req)
        if err != nil {
                http.Error(w, fmt.Sprintf("Failed to fetch target URL: %v", err), http.StatusBadGateway)
                return
        }
        defer resp.Body.Close()

        for key, values := range resp.Header {
                for _, value := range values {
                        w.Header().Add(key, value)
                }
        }
        w.WriteHeader(resp.StatusCode)
        io.Copy(w, resp.Body)
}

// indexHandler 提供一个简单的使用说明页面
func indexHandler(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/" {
        http.NotFound(w, r)
        return
    }
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        fmt.Fprint(w, `<h1>播客 RSS 代理服务器</h1>...`) // 说明文字与之前版本相同
}


// --- 辅助函数 (Helpers) ---

func getBaseURL(r *http.Request) string {
        scheme := "http"
        if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
                scheme = "https"
        }
        return fmt.Sprintf("%s://%s", scheme, r.Host)
}

func createProxyURL(baseURL, targetURL, apiKey string) string {
        if targetURL == "" {
                return ""
        }
        proxyURL, _ := url.Parse(baseURL + "/proxy")
        params := url.Values{}
        params.Set("url", targetURL)
        params.Set("apikey", apiKey)
        proxyURL.RawQuery = params.Encode()
        return proxyURL.String()
}
