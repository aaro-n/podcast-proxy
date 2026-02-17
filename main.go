package main
 
import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)
 
var apiKeyEnv string
var forceHTTPS bool // 新增变量
 
func main() {
	// 从环境变量读取 API Key
	apiKey := os.Getenv("PODCAST_PROXY_APIKEY")
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}
	if apiKey == "" {
		log.Fatal("请设置环境变量 PODCAST_PROXY_APIKEY 或 API_KEY 以提供访问密钥")
	}
	apiKeyEnv = apiKey
 
	// 环境变量控制是否强制 https
	forceHTTPS = os.Getenv("FORCE_HTTPS") == "true"
 
	// 端口，默认 8080
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
 
	// register with trailing slash for prefix matching because we
	// accept /audio/<apikey> and /image/<apikey> paths too.

	// Helper for logging
	logHandler := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			log.Printf("Start: %s %s from %s", r.Method, r.URL.String(), r.RemoteAddr)
			h(w, r)
			duration := time.Since(start)
			log.Printf("Completed: %s %s in %v", r.Method, r.URL.String(), duration)
		}
	}

	http.HandleFunc("/feed", logHandler(feedHandler))
	http.HandleFunc("/audio/", logHandler(audioHandler)) // also handles /audio
	http.HandleFunc("/audio", logHandler(audioHandler))
	http.HandleFunc("/image/", logHandler(imageHandler))
	http.HandleFunc("/image", logHandler(imageHandler))
	// 样式代理独立路径
	http.HandleFunc("/style/", logHandler(styleHandler))
	http.HandleFunc("/style", logHandler(styleHandler))

	// 生成器页面
	http.HandleFunc("/", logHandler(generateHandler))

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
	// encode key for inclusion in generated URLs
	encodedKey := base64.StdEncoding.EncodeToString([]byte(apikey))
 
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
	if forceHTTPS {
		scheme = "https"
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	// 如果设置了 PUBLIC_HOST 环境变量，则强制使用该域名
	if publicHost := os.Getenv("PUBLIC_HOST"); publicHost != "" {
		host = publicHost
	}
 
	// helper to escape characters that would break an XML attribute value.
	// after we switch to placing the key in the path there will be no unescaped
	// ampersand at the top level, but keep the function for safety (it is
	// basically a no-op now.
	xmlEscape := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "&", "&amp;"), "\"", "&quot;")
	}

	proxyForAudio := func(orig string) string {
		raw := fmt.Sprintf("%s://%s/audio/%s?url=%s", scheme, host,
			url.PathEscape(encodedKey), url.QueryEscape(orig))
		return xmlEscape(raw)
	}
	proxyForImage := func(orig string) string {
		raw := fmt.Sprintf("%s://%s/image/%s?url=%s", scheme, host,
			url.PathEscape(encodedKey), url.QueryEscape(orig))
		return xmlEscape(raw)
	}
	// 样式 URL 的代理使用独立端口（如果有的话）
	proxyForStyle := func(orig string) string {
		raw := fmt.Sprintf("%s://%s/style/%s?url=%s", scheme, host,
			url.PathEscape(encodedKey), url.QueryEscape(orig))
		return xmlEscape(raw)
	}
 
	// ---- 使用正则表达式进行替换 ----
	// We only rewrite URLs for audio, image and pagination links.  Other
	// elements including descriptions, titles, etc. are left untouched.
	// If the feed contains an xml-stylesheet reference, proxy that too so
	// browser can fetch the style from our domain instead of failing with
	// a 404 when the href is relative.
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
		} else if !isAudio {
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
 
	// 规则4: 如果存在 <\?xml-stylesheet ... href="...">，则代理样式文件。
	// 样式文件单独通过 /style/ 路径，并可能使用独立端口。
	reStylesheet := regexp.MustCompile(`(<\?xml-stylesheet[^>]*href=")([^"]+)(")`)
	content = reStylesheet.ReplaceAllStringFunc(content, func(match string) string {
		parts := reStylesheet.FindStringSubmatch(match)
		return parts[1] + proxyForStyle(parts[2]) + parts[3]
	})

	// NOTE: we intentionally leave atom:link entries untouched so that
	// everything except images/audio (and optional stylesheet) remains
	// identical to the original feed.  pagination links etc. will therefore
	// still point at the upstream domain; this is OK if you only want
	// the media assets proxied.
 
	transformed := []byte(content)
	// ---- 替换结束 ----
 
	// 转发源 RSS 的响应头
	forwardRSSHeaders(w, resp.Header)
 
	// 设置自身的 Content-Type，并写出转换后的 RSS
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(transformed)
}
 
// /audio?url=原始音频URL&apikey=密钥
func audioHandler(w http.ResponseWriter, r *http.Request) {
	// 认证：先尝试 query，再看 path
	apikey := r.URL.Query().Get("apikey")
	if apikey == "" {
		if p := strings.TrimPrefix(r.URL.Path, "/audio/"); p != "" {
			parts := strings.SplitN(p, "/", 2)
			apikey = parts[0]
		}
	}
	// request may contain encoded key; decode if so
	if decoded, err := base64.StdEncoding.DecodeString(apikey); err == nil {
		apikey = string(decoded)
	}
	if apikey != apiKeyEnv {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		return
	}

	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url 参数是必填项", http.StatusBadRequest)
		return
	}
	// unescape xml entities left in the query
	origURL = strings.ReplaceAll(origURL, "&amp;", "&")

	req, err := http.NewRequest("GET", origURL, nil)
	if err != nil {
		http.Error(w, "无效的音频 URL: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PodcastProxy/1.0")
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
 
	// create client that will not auto-follow redirects
	client := http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "获取音频失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// if server responded with redirect, forward it (rewriting and
	// re-encoding the location through our proxy)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			scheme := "http"
			if forceHTTPS || r.TLS != nil {
				scheme = "https"
			}
			host := r.Host
			if publicHost := os.Getenv("PUBLIC_HOST"); publicHost != "" {
				host = publicHost
			}
			// construct new proxy location; key is already decoded earlier
			encoded := base64.StdEncoding.EncodeToString([]byte(apikey))
			newLoc := fmt.Sprintf("%s://%s/audio/%s?url=%s",
				scheme, host, url.PathEscape(encoded), url.QueryEscape(loc))
			http.Redirect(w, r, newLoc, resp.StatusCode)
			return
		}
	}

	copyHeader(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
 
// /image?url=原始图片URL&apikey=密钥
func imageHandler(w http.ResponseWriter, r *http.Request) {
	// 认证：允许通过 path 提供 key
	apikey := r.URL.Query().Get("apikey")
	if apikey == "" {
		if p := strings.TrimPrefix(r.URL.Path, "/image/"); p != "" {
			parts := strings.SplitN(p, "/", 2)
			apikey = parts[0]
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(apikey); err == nil {
		apikey = string(decoded)
	}
	if apikey != apiKeyEnv {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		return
	}
 
	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url 参数是必填项", http.StatusBadRequest)
		return
	}
	origURL = strings.ReplaceAll(origURL, "&amp;", "&")
 
	req, err := http.NewRequest("GET", origURL, nil)
	if err != nil {
		http.Error(w, "无效的图片 URL: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PodcastProxy/1.0")
 
	client := http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "获取图片失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			scheme := "http"
			if forceHTTPS || r.TLS != nil {
				scheme = "https"
			}
			host := r.Host
			if publicHost := os.Getenv("PUBLIC_HOST"); publicHost != "" {
				host = publicHost
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(apikey))
			newLoc := fmt.Sprintf("%s://%s/image/%s?url=%s",
				scheme, host, url.PathEscape(encoded), url.QueryEscape(loc))
			http.Redirect(w, r, newLoc, resp.StatusCode)
			return
		}
	}

	copyHeader(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// /style?url=原始样式URL&apikey=密钥
// 这个处理器和 imageHandler 几乎一样
func styleHandler(w http.ResponseWriter, r *http.Request) {
	// 认证：允许通过 path 提供 key
	apikey := r.URL.Query().Get("apikey")
	if apikey == "" {
		if p := strings.TrimPrefix(r.URL.Path, "/style/"); p != "" {
			parts := strings.SplitN(p, "/", 2)
			apikey = parts[0]
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(apikey); err == nil {
		apikey = string(decoded)
	}
	if apikey != apiKeyEnv {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		return
	}
 
	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url 参数是必填项", http.StatusBadRequest)
		return
	}
	origURL = strings.ReplaceAll(origURL, "&amp;", "&")
 
	req, err := http.NewRequest("GET", origURL, nil)
	if err != nil {
		http.Error(w, "无效的样式 URL: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "PodcastProxy/1.0")
 
	client := http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "获取样式失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc != "" {
			scheme := "http"
			if forceHTTPS || r.TLS != nil {
				scheme = "https"
			}
			host := r.Host
			if publicHost := os.Getenv("PUBLIC_HOST"); publicHost != "" {
				host = publicHost
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(apikey))
			newLoc := fmt.Sprintf("%s://%s/style/%s?url=%s",
				scheme, host, url.PathEscape(encoded), url.QueryEscape(loc))
			http.Redirect(w, r, newLoc, resp.StatusCode)
			return
		}
	}

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
	hopByHopHeaders := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailers",
		"Transfer-Encoding",
		"Upgrade",
		"Content-Length",
		"Content-Encoding",
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
		if strings.EqualFold(k, "Content-Type") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}

// 简单的生成器页面
func generateHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	
	html := `
<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Podcast Proxy Generator</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; max-width: 600px; margin: 2rem auto; padding: 2rem; background-color: #f4f6f8; color: #333; }
		h1 { text-align: center; color: #2c3e50; margin-bottom: 2rem; }
		.card { background: white; padding: 2rem; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
		.form-group { margin-bottom: 1.5rem; }
		label { display: block; margin-bottom: 0.5rem; font-weight: 600; color: #495057; }
		input[type="text"], input[type="password"] { width: 100%; padding: 0.75rem; font-size: 1rem; border: 1px solid #ced4da; border-radius: 4px; box-sizing: border-box; transition: border-color 0.15s ease-in-out; }
		input:focus { border-color: #80bdff; outline: 0; box-shadow: 0 0 0 0.2rem rgba(0,123,255,.25); }
		button { display: block; width: 100%; padding: 0.75rem; font-size: 1rem; font-weight: 600; color: white; background-color: #007bff; border: none; border-radius: 4px; cursor: pointer; transition: background-color 0.15s ease-in-out; }
		button:hover { background-color: #0056b3; }
		#result { margin-top: 2rem; display: none; }
		.result-box { padding: 1rem; background-color: #e9ecef; border-radius: 4px; word-break: break-all; font-family: monospace; border: 1px solid #dee2e6; position: relative; }
		.copy-btn { position: absolute; top: 5px; right: 5px; font-size: 0.8rem; padding: 0.2rem 0.5rem; background: #6c757d; color: white; border-radius: 3px; cursor: pointer; }
		.copy-btn:hover { background: #5a6268; }
	</style>
</head>
<body>
	<div class="card">
		<h1>Podcast Proxy</h1>
		<div class="form-group">
			<label for="url">Podcast Feed URL (原始RSS地址)</label>
			<input type="text" id="url" placeholder="https://example.com/feed.xml" required>
		</div>
		<div class="form-group">
			<label for="apikey">API Key (访问密钥)</label>
			<input type="password" id="apikey" placeholder="输入 API Key" required>
		</div>
		<button onclick="generate()">生成代理链接</button>

		<div id="result">
			<label>代理后的 Feed URL:</label>
			<div class="result-box">
				<span id="output"></span>
				<button class="copy-btn" onclick="copyToClipboard()">复制</button>
			</div>
		</div>
	</div>

	<script>
		function generate() {
			const urlInput = document.getElementById('url');
			const keyInput = document.getElementById('apikey');
			const url = urlInput.value.trim();
			const apikey = keyInput.value.trim();
			
			if (!url || !apikey) {
				alert('请填写完整的 RSS URL 和 API Key');
				return;
			}
			
			// 简单的 URL 校验
			try {
				new URL(url);
			} catch (_) {
				alert('请输入有效的 URL (例如 https://...)');
				return;
			}

			const currentHost = window.location.protocol + "//" + window.location.host;
			const proxyUrl = currentHost + "/feed?url=" + encodeURIComponent(url) + "&apikey=" + encodeURIComponent(apikey);
			
			const output = document.getElementById('output');
			output.textContent = proxyUrl;
			document.getElementById('result').style.display = 'block';
		}

		function copyToClipboard() {
			const text = document.getElementById('output').textContent;
			navigator.clipboard.writeText(text).then(() => {
				const btn = document.querySelector('.copy-btn');
				const origText = btn.textContent;
				btn.textContent = '已复制!';
				setTimeout(() => btn.textContent = origText, 2000);
			}).catch(err => {
				console.error('无法复制', err);
			});
		}
	</script>
</body>
</html>
`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}
