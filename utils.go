package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProxyURLBuilder URL构建器
type ProxyURLBuilder struct {
	scheme    string
	host      string
	sourceURL string
}

// NewProxyURLBuilder 创建URL构建器
func NewProxyURLBuilder(r *http.Request, sourceURL ...string) *ProxyURLBuilder {
	builder := &ProxyURLBuilder{
		scheme: "http",
		host:   "",
	}
	if len(sourceURL) > 0 {
		builder.sourceURL = sourceURL[0]
	}

	// 判断是否使用HTTPS
	if GetConfig().ForceHTTPS || r.TLS != nil {
		builder.scheme = "https"
	}

	// 使用PUBLIC_HOST覆盖
	if GetConfig().PublicHost != "" {
		builder.host = GetConfig().PublicHost
	}
	if r.TLS != nil {
		builder.scheme = "https"
	}
	if GetConfig().TrustProxyHeaders {
		if proto := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])); proto == "https" {
			builder.scheme = "https"
		}
	}

	return builder
}

// BuildAudioURL 构建音频代理URL
func (b *ProxyURLBuilder) BuildAudioURL(apikey, originalURL string, credentials ...string) string {
	return b.buildURL("audio", apikey, originalURL, credentials...)
}

// BuildImageURL 构建图片代理URL
func (b *ProxyURLBuilder) BuildImageURL(apikey, originalURL string, credentials ...string) string {
	return b.buildURL("image", apikey, originalURL, credentials...)
}

// BuildStyleURL 构建样式代理URL
func (b *ProxyURLBuilder) BuildStyleURL(apikey, originalURL string, credentials ...string) string {
	return b.buildURL("style", apikey, originalURL, credentials...)
}

// BuildFeedXSLURL is retained for legacy integrations; native feed presentation
// no longer adds an XSLT processing instruction to RSS/Atom responses.
func (b *ProxyURLBuilder) BuildFeedXSLURL(apikey string) string {
	token := NewAuthManager().SignFeedURL(b.sourceURL)
	return fmt.Sprintf("%s://%s/podcast.xsl/%s?url=%s", b.scheme, b.host, token, url.QueryEscape(b.sourceURL))
}

// BuildFeedURL returns a scoped feed URL suitable for entering directly into PinePods.
func (b *ProxyURLBuilder) BuildFeedURL(apikey string) string {
	return b.BuildFeedURLFor(apikey, b.sourceURL)
}

func (b *ProxyURLBuilder) BuildFeedURLFor(apikey, feedURL string) string {
	token := NewAuthManager().SignScopedFeedURL(feedURL)
	username, password := LookupFeedCredentials(b.sourceURL)
	if username != "" || password != "" {
		RegisterFeedCredentials(feedURL, username, password)
	}
	return fmt.Sprintf("%s://%s/feed?url=%s&feed_token=%s", b.scheme, b.host, url.QueryEscape(feedURL), token)
}

// BuildAbsoluteFeedURL is used by browser-facing XML processing instructions.
func (b *ProxyURLBuilder) BuildAbsoluteFeedURL(apikey string) string {
	return b.BuildFeedURL(apikey)
}

// buildURL 内部构建代理URL，带有虚拟文件名和原生后缀，以通过各大播客客户端和校验器的严格检测
func (b *ProxyURLBuilder) buildURL(resourceType, apikey, originalURL string, credentials ...string) string {
	// RSS 属性里原始 URL 常带 &amp;，先还原为 & 再 QueryEscape，
	// 否则会产生 %26amp%3B 二次编码，严格播放器可能校验失败。
	originalURL = strings.ReplaceAll(originalURL, "&amp;", "&")
	resource := ProxyResource(resourceType)
	token := NewAuthManager().SignResource(resource, originalURL)
	RegisterResourceRoute(resource, token, originalURL, credentials...)

	// 根据资源类型和原始URL猜测文件后缀，从而在路径中追加虚拟文件名
	virtualFile := "file"
	ext := ""

	// 解析原始URL获取其实际后缀
	if parsed, err := url.Parse(originalURL); err == nil {
		path := parsed.Path
		if idx := strings.LastIndex(path, "."); idx != -1 {
			possibleExt := path[idx:]
			// 确保后缀不含斜杠且长度合理
			if !strings.Contains(possibleExt, "/") && len(possibleExt) <= 6 {
				ext = strings.ToLower(possibleExt)
			}
		}
	}

	// 默认备用后缀
	if ext == "" {
		switch resourceType {
		case "audio":
			ext = ".mp3"
		case "image":
			ext = ".jpg"
		case "style":
			ext = ".css"
		}
	}

	switch resourceType {
	case "audio":
		virtualFile = "podcast" + ext
	case "image":
		virtualFile = "cover" + ext
	case "style":
		virtualFile = "style" + ext
	}

	raw := fmt.Sprintf("%s://%s/%s/%s/%s?url=%s",
		b.scheme,
		b.host,
		resourceType,
		token,
		virtualFile,
		url.QueryEscape(originalURL),
	)

	return raw
}

// xmlEscape XML属性值转义
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func registerCredentialsOnMediaURL(proxyURL string, credentials ...string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}
	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return proxyURL
	}
	resource := ProxyResource(parts[0])
	username, password := "", ""
	if len(credentials) > 0 {
		username = credentials[0]
	}
	if len(credentials) > 1 {
		password = credentials[1]
	}
	RegisterResourceCredentials(resource, parts[1], username, password)
	return proxyURL
}

// SourceURL returns the feed address used to resolve relative media links.
func (b *ProxyURLBuilder) SourceURL() string {
	return b.sourceURL
}

// HeaderCopier 响应头复制器
type HeaderCopier struct {
	dst http.ResponseWriter
	src http.Header
}

// NewHeaderCopier 创建响应头复制器
func NewHeaderCopier(dst http.ResponseWriter, src http.Header) *HeaderCopier {
	return &HeaderCopier{
		dst: dst,
		src: src,
	}
}

// Copy 复制所有响应头
func (hc *HeaderCopier) Copy() {
	for k, vv := range hc.src {
		for _, v := range vv {
			hc.dst.Header().Add(k, v)
		}
	}
}

// CopyExcept 排除指定响应头后复制
func (hc *HeaderCopier) CopyExcept(excludeHeaders []string) {
	excludeMap := make(map[string]bool)
	for _, h := range excludeHeaders {
		excludeMap[strings.ToLower(h)] = true
	}

	for k, vv := range hc.src {
		if excludeMap[strings.ToLower(k)] {
			continue
		}
		for _, v := range vv {
			hc.dst.Header().Add(k, v)
		}
	}
}

// LoggerHelper 日志助手
type LoggerHelper struct {
	method string
	path   string
	addr   string
	start  time.Time
}

// NewLoggerHelper 创建日志助手
func NewLoggerHelper(r *http.Request) *LoggerHelper {
	return &LoggerHelper{
		method: r.Method,
		path:   sanitizeLogPath(r.URL),
		addr:   getRemoteAddr(r),
		start:  time.Now(),
	}
}

// sanitizeLogPath 日志脱敏：只保留路径，丢弃 query。
// query 里含有 apikey 和源站地址，写进日志后会长期留在磁盘上，
// 既浪费空间，也容易在贴日志排查问题时意外泄露凭据。
func sanitizeLogPath(u *url.URL) string {
	path := u.Path
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		switch parts[1] {
		case "audio", "image", "style", "podcast.xsl":
			if parts[2] != "" {
				parts[2] = "<redacted>"
			}
		}
	}
	path = strings.Join(parts, "/")
	if u.RawQuery != "" {
		return path + "?<redacted>"
	}
	return path
}

// LogStart 记录请求开始
func (l *LoggerHelper) LogStart() {
	fmt.Printf("[%s] Start: %s %s from %s\n",
		l.start.Format("15:04:05"), l.method, l.path, l.addr)
}

// LogComplete 记录请求完成
func (l *LoggerHelper) LogComplete(statusCode int) {
	duration := time.Since(l.start)
	fmt.Printf("[%s] Complete: %s %s -> %d in %v\n",
		l.start.Format("15:04:05"), l.method, l.path, statusCode, duration)
}

// RejectRequest 静默拒绝请求（关闭/中断 TCP 连接，不返回任何数据），使扫描器无法识别服务
func RejectRequest(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			conn.Close()
			return
		}
	}
	// 备用：若无法 Hijack，则写入 Connection: close 头并直接关闭，尽可能不发送多余的 HTTP 实体数据
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusNotFound)
}

// getRemoteAddr 获取客户端IP
func getRemoteAddr(r *http.Request) string {
	// Forwarded client IP headers are spoofable unless the request came through a
	// trusted reverse proxy. Log the socket peer instead of trusting user input.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}

	return r.RemoteAddr
}

// StringHelper 字符串助手
type StringHelper struct{}

// DecodeAmpersand HTML实体解码
func (*StringHelper) DecodeAmpersand(s string) string {
	return strings.ReplaceAll(s, "&amp;", "&")
}

// IsImageURL 判断是否是图片URL
func (*StringHelper) IsImageURL(u string) bool {
	u = strings.ToLower(u)
	imageExts := []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg"}
	for _, ext := range imageExts {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}
