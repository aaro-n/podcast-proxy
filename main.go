package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp" // 导入 regexp 包
	"strings"
	"time"
)

var apiKeyEnv string

func main() {
	// 从环境变量读取 API Key
	// 优先 PODCAST_PROXY_APIKEY，其次 API_KEY
	apiKey := os.Getenv("PODCAST_PROXY_APIKEY")
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}
	if apiKey == "" {
		log.Fatal("请设置环境变量 PODCAST_PROXY_APIKEY 或 API_KEY 以提供访问密钥")
	}
	apiKeyEnv = apiKey

	// 端口，默认 8080
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	http.HandleFunc("/feed", feedHandler)
	http.HandleFunc("/audio", audioHandler)
	http.HandleFunc("/image", imageHandler)

	log.Printf("Podcast proxy 服务启动，监听 %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// /feed?url=原始RSS源URL&apikey=密钥
func feedHandler(w http.ResponseWriter, r *http.Request) {
	// 校验 apikey
	apikey := r.URL.Query().Get("apikey")
	if apikey != apiKeyEnv {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		return
	}

	feedURL := r.URL.Query().Get("url")
	if feedURL == "" {
		http.Error(w, "url 参数是必填项", http.StatusBadRequest)
		return
	}

	// 获取原始 RSS
	req, err := http.NewRequest("GET", feedURL, nil)
	if err != nil {
		http.Error(w, "无效的源URL: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PodcastProxy/1.0")

	client := http.Client{
		Timeout: 30 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "获取源 RSS 失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// 直接透传源站的错误响应体，可能包含有用信息
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		log.Printf("源 RSS 返回错误: %s, URL: %s", resp.Status, feedURL)
		return
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "读取源 RSS 失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 构造代理 URL 模板
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host

	proxyForAudio := func(orig string) string {
		return fmt.Sprintf("%s://%s/audio?url=%s&apikey=%s", scheme, host, url.QueryEscape(orig), url.QueryEscape(apikey))
	}
	proxyForImage := func(orig string) string {
		return fmt.Sprintf("%s://%s/image?url=%s&apikey=%s", scheme, host, url.QueryEscape(orig), url.QueryEscape(apikey))
	}

	// ---- 使用正则表达式进行替换 ----
	content := string(bodyBytes)

	// 规则1: 替换 <enclosure url="...">
	reEnclosure := regexp.MustCompile(`(<enclosure\s+[^>]*?url=")([^"]+)`)
	content = reEnclosure.ReplaceAllStringFunc(content, func(match string) string {
		parts := reEnclosure.FindStringSubmatch(match)
		return parts[1] + proxyForAudio(parts[2])
	})

	// 规则2: 替换 <itunes:image href="...">
	reItunesImage := regexp.MustCompile(`(<itunes:image\s+[^>]*?href=")([^"]+)`)
	content = reItunesImage.ReplaceAllStringFunc(content, func(match string) string {
		parts := reItunesImage.FindStringSubmatch(match)
		return parts[1] + proxyForImage(parts[2])
	})

	// 规则3: 替换 <image><url>...</url></image>
	reImageURL := regexp.MustCompile(`(<image>[\s\S]*?<url>)([^<]+)(<\/url>)`)
	content = reImageURL.ReplaceAllStringFunc(content, func(match string) string {
		parts := reImageURL.FindStringSubmatch(match)
		return parts[1] + proxyForImage(strings.TrimSpace(parts[2])) + parts[3]
	})

	// 规则4: 替换 <media:thumbnail url="...">
	reMediaThumbnail := regexp.MustCompile(`(<media:thumbnail\s+[^>]*?url=")([^"]+)`)
	content = reMediaThumbnail.ReplaceAllStringFunc(content, func(match string) string {
		parts := reMediaThumbnail.FindStringSubmatch(match)
		return parts[1] + proxyForImage(parts[2])
	})

	// 规则5: 替换 <media:content url="...">
	reMediaContent := regexp.MustCompile(`<media:content\s+[^>]*?url="[^"]+"[^>]*>`)
	content = reMediaContent.ReplaceAllStringFunc(content, func(match string) string {
		isAudio := strings.Contains(match, `type="audio/`)
		isImage := strings.Contains(match, `type="image/`)
		proxyFunc := proxyForAudio // 默认是音频代理

		if isImage {
			proxyFunc = proxyForImage
		} else if !isAudio { // 如果 type 属性不存在或不是 audio，则进行回退判断
			urlRegex := regexp.MustCompile(`url="([^"]+)"`)
			urlMatch := urlRegex.FindStringSubmatch(match)
			if len(urlMatch) > 1 {
				lowerURL := strings.ToLower(urlMatch[1])
				imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg"}
				for _, ext := range imageExts {
					if strings.HasSuffix(lowerURL, ext) {
						proxyFunc = proxyForImage
						break
					}
				}
			}
		}

		urlAttrRegex := regexp.MustCompile(`(url=")([^"]+)`)
		return urlAttrRegex.ReplaceAllStringFunc(match, func(attrMatch string) string {
			parts := urlAttrRegex.FindStringSubmatch(attrMatch)
			return parts[1] + proxyFunc(parts[2])
		})
	})

	// 规则6 (可选但推荐): 替换 <atom:link rel="self" href="...">
	// 这可以防止播客客户端在下次刷新时绕过代理
	reAtomLink := regexp.MustCompile(`(<atom:link\s+[^>]*?rel="self"[^>]*?href=")([^"]+)`)
	selfURL := fmt.Sprintf("%s://%s%s", scheme, host, r.RequestURI)
	content = reAtomLink.ReplaceAllStringFunc(content, func(match string) string {
		parts := reAtomLink.FindStringSubmatch(match)
		// 使用 xml.EscapeString 对 URL 进行转义，以防 URL 中包含 & 等特殊字符
		return parts[1] + selfURL
	})

	transformed := []byte(content)
	// ---- 替换结束 ----

	// 转发源 RSS 的响应头
	forwardRSSHeaders(w, resp.Header)

	// 设置自身的 Content-Type，并写出转换后的 RSS
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	// 长度由 http 库自动处理，不再需要手动设置
	// w.Header().Set("Content-Length", strconv.Itoa(len(transformed)))
	w.WriteHeader(http.StatusOK)
	w.Write(transformed)
}

// /audio?url=原始音频URL&apikey=密钥
func audioHandler(w http.ResponseWriter, r *http.Request) {
	// 认证
	apikey := r.URL.Query().Get("apikey")
	if apikey != apiKeyEnv {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		return
	}

	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url 参数是必填项", http.StatusBadRequest)
		return
	}

	// 请求原始音频，自动处理跳转
	req, err := http.NewRequest("GET", origURL, nil)
	if err != nil {
		http.Error(w, "无效的音频 URL: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PodcastProxy/1.0")
	// 透传 Range 请求头，以支持音频拖动
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	client := http.Client{
		// 注意：代理大文件时不应设置过短的超时
		// Timeout: 60 * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "获取音频失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 透传原始响应头
	copyHeader(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// /image?url=原始图片URL&apikey=密钥
func imageHandler(w http.ResponseWriter, r *http.Request) {
	// 认证
	apikey := r.URL.Query().Get("apikey")
	if apikey != apiKeyEnv {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		return
	}

	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url 参数是必填项", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequest("GET", origURL, nil)
	if err != nil {
		http.Error(w, "无效的图片 URL: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PodcastProxy/1.0")

	client := http.Client{
		Timeout: 30 * time.Second, // 图片超时可以短一些
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "获取图片失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeader(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// helper：把源响应头透传到目标响应
func copyHeader(dst http.ResponseWriter, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Header().Add(k, v)
		}
	}
}

// 透明复制响应头（转发播客 RSS 的响应头），跳过不合适的字段
func forwardRSSHeaders(w http.ResponseWriter, src http.Header) {
	// Hop-by-hop headers. These are removed when sent to the backend.
	// http://www.w3.org/Protocols/rfc2616/rfc2616-sec13.html
	hopByHopHeaders := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te", // canonicalized version of "TE"
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
		// 以下为自定义添加，因为内容会被重写
		"Content-Length",
		"Content-Encoding", // 例如 gzip，因为我们解压后处理了，需要重新编码
	}

	for k, vv := range src {
		isHopByHop := false
		for _, h := range hopByHopHeaders {
			if strings.EqualFold(k, h) {
				isHopByHop = true
				break
			}
		}
		if isHopByHop {
			continue
		}

		// 不覆盖我们自己设置的 Content-Type
		if strings.EqualFold(k, "Content-Type") {
			continue
		}

		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}
