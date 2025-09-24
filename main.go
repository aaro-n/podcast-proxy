package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
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
		http.Error(w, fmt.Sprintf("源 RSS 返回错误: %s", resp.Status), resp.StatusCode)
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

	// 逐节点解析并重组 RSS（包含 MRSS/media 的扩展标签处理）
	transformed, err := transformRSS(bodyBytes, proxyForAudio, proxyForImage)
	if err != nil {
		http.Error(w, "处理 RSS 失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 转发源 RSS 的响应头（尽量复制可转发的字段）
	forwardRSSHeaders(w, resp.Header)

	// 设置自身的 Content-Type，并写出转换后的 RSS
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
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

	client := http.Client{
		Timeout: 60 * time.Second,
		// 默认会跟随重定向
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
		Timeout: 60 * time.Second,
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

// helper：把源响应头透传到目标响应，但跳过不合适的字段
func copyHeader(dst http.ResponseWriter, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Header().Add(k, v)
		}
	}
}

// 透明复制响应头（转发播客 RSS 的响应头），跳过不合适的字段
func forwardRSSHeaders(w http.ResponseWriter, src http.Header) {
	for k, vv := range src {
		lower := strings.ToLower(k)
		// 跳过 Hop-by-Hop 头部和长度相关头部
		if lower == "content-length" || lower == "transfer-encoding" || lower == "content-encoding" {
			continue
		}
		if lower == "connection" || lower == "keep-alive" || lower == "proxy-authenticate" || lower == "proxy-authorization" || lower == "te" || lower == "trailers" || lower == "upgrade" {
			continue
		}
		// 不覆盖显式设定的 Content-Type，由后续设置控制
		if strings.EqualFold(k, "Content-Type") {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}

// transformRSS 将原始 RSS 的 XML 逐节点解析并重写入口、图片相关链接。
// proxyAudio: 将原始音频 URL 替换为代理音频的地址
// proxyImage: 将原始图片 URL 替换为代理图片的地址
func transformRSS(input []byte, proxyAudio, proxyImage func(string) string) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(input))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)

	// 用于跟踪当前解析的 XML 路径（仅本地名，不处理命名空间前缀）
	var stack []string

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch tk := tok.(type) {
		case xml.StartElement:
			// 拷贝并可能修改 StartElement
			start := tk

			// 1) 修改特定元素的属性
			// <enclosure url="..."> 的 url 应替换为代理音频
			if strings.EqualFold(start.Name.Local, "enclosure") {
				for i := range start.Attr {
					if strings.EqualFold(start.Attr[i].Name.Local, "url") {
						start.Attr[i].Value = proxyAudio(start.Attr[i].Value)
					}
				}
			}

			// 2) iTunes <image href="..."> 处理（保持不变）
			// iTunes 命名空间的 image 标签通常 Local = "image" 且 Space 指向 iTunes 的命名空间
			if strings.EqualFold(start.Name.Local, "image") && isItunesImageNamespace(start.Name.Space) {
				for i := range start.Attr {
					if strings.EqualFold(start.Attr[i].Name.Local, "href") {
						start.Attr[i].Value = proxyImage(start.Attr[i].Value)
					}
				}
			}

			// 3) MRSS/media 内容扩展：处理 <media:content> 和 <media:thumbnail>
			// 判断命名空间是否为 MRSS/媒体相关
			if isMediaMRSSNamespace(start.Name.Space) {
				// 3a) <media:content url="..." type="..."/>
				if strings.EqualFold(start.Name.Local, "content") {
					// 先获取 type attribute 用于 mime 判断
					mime := ""
					for _, a := range start.Attr {
						if strings.EqualFold(a.Name.Local, "type") {
							mime = a.Value
							break
						}
					}
					// 替换 url 属性
					for i := range start.Attr {
						if strings.EqualFold(start.Attr[i].Name.Local, "url") {
							start.Attr[i].Value = proxyForMediaURL(start.Attr[i].Value, mime, proxyAudio, proxyImage)
						}
					}
				}
				// 3b) <media:thumbnail url="..."/>
				if strings.EqualFold(start.Name.Local, "thumbnail") {
					for i := range start.Attr {
						if strings.EqualFold(start.Attr[i].Name.Local, "url") {
							start.Attr[i].Value = proxyImage(start.Attr[i].Value)
						}
					}
				}
			}

			// 将修改后的 StartElement 写出
			if err := enc.EncodeToken(start); err != nil {
				return nil, err
			}
			stack = append(stack, start.Name.Local)

		case xml.EndElement:
			// 出栈
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if err := enc.EncodeToken(tk); err != nil {
				return nil, err
			}

		case xml.CharData:
			// 2) 对 <image> 下的 <url> 内容进行改写：channel/image/url
			// 当当前父级是 image，当前元素名是 url
			if len(stack) >= 2 && stack[len(stack)-2] == "image" && stack[len(stack)-1] == "url" {
				orig := string(tk)
				newVal := proxyImage(orig)
				if err := enc.EncodeToken(xml.CharData([]byte(newVal))); err != nil {
					return nil, err
				}
			} else {
				if err := enc.EncodeToken(tk); err != nil {
					return nil, err
				}
			}

		default:
			// 其他任意类型的 token 直接透传
			if err := enc.EncodeToken(tok); err != nil {
				return nil, err
			}
		}
	}

	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// isItunesImageNamespace 判断是否为 iTunes image 标签的命名空间
func isItunesImageNamespace(ns string) bool {
	// 常见命名空间 URI，可能随实现变动
	// 1) http://www.itunes.com/dtds/podcast-1.0.dtd
	// 2) http://www.itunes.apple.com/dtds/podcast-1.0.dtd
	// 3) ""（无命名空间时也可能出现）
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return true // 兼容情况：无命名空间时也尝试替换 href
	}
	n := strings.ToLower(ns)
	return strings.Contains(n, "itunes") || strings.Contains(n, "dtds/podcast-1.0")
}

// 判断是否为 MRSS 媒体命名空间（简化容错实现）
func isMediaMRSSNamespace(ns string) bool {
	ns = strings.ToLower(strings.TrimSpace(ns))
	if ns == "" {
		return false
	}
	// 常见 MRSS 命名空间标记
	return strings.Contains(ns, "mrss") || strings.Contains(ns, "media")
}

// 根据 mime/type 或扩展名，选择代理目标
func proxyForMediaURL(orig string, mime string, proxyAudio func(string) string, proxyImage func(string) string) string {
	lmime := strings.ToLower(strings.TrimSpace(mime))
	// 根据 mime 优先判断
	if strings.HasPrefix(lmime, "audio/") {
		return proxyAudio(orig)
	}
	if strings.HasPrefix(lmime, "image/") {
		return proxyImage(orig)
	}
	// 根据扩展名猜测
	lower := strings.ToLower(orig)
	audioExts := []string{".mp3", ".m4a", ".aac", ".wav", ".flac", ".ogg"}
	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg"}

	for _, e := range audioExts {
		if strings.HasSuffix(lower, e) {
			return proxyAudio(orig)
		}
	}
	for _, e := range imageExts {
		if strings.HasSuffix(lower, e) {
			return proxyImage(orig)
		}
	}
	// 默认回退到音频代理，尽量保守
	return proxyAudio(orig)
}
