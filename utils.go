package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ProxyURLBuilder URL构建器
type ProxyURLBuilder struct {
	scheme string
	host   string
}

// NewProxyURLBuilder 创建URL构建器
func NewProxyURLBuilder(r *http.Request) *ProxyURLBuilder {
	builder := &ProxyURLBuilder{
		scheme: "http",
		host:   r.Host,
	}

	// 判断是否使用HTTPS
	if GetConfig().ForceHTTPS || r.TLS != nil {
		builder.scheme = "https"
	}

	// 使用PUBLIC_HOST覆盖
	if publicHost := os.Getenv("PUBLIC_HOST"); publicHost != "" {
		builder.host = publicHost
	}

	return builder
}

// BuildAudioURL 构建音频代理URL
func (b *ProxyURLBuilder) BuildAudioURL(apikey, originalURL string) string {
	return b.buildURL("audio", apikey, originalURL)
}

// BuildImageURL 构建图片代理URL
func (b *ProxyURLBuilder) BuildImageURL(apikey, originalURL string) string {
	return b.buildURL("image", apikey, originalURL)
}

// BuildStyleURL 构建样式代理URL
func (b *ProxyURLBuilder) BuildStyleURL(apikey, originalURL string) string {
	return b.buildURL("style", apikey, originalURL)
}

// buildURL 内部构建代理URL，带有虚拟文件名和原生后缀，以通过各大播客客户端和校验器的严格检测
func (b *ProxyURLBuilder) buildURL(resourceType, apikey, originalURL string) string {
	auth := NewAuthManager()
	encodedKey := auth.EncodeKey(apikey)

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
		url.PathEscape(encodedKey),
		virtualFile,
		url.QueryEscape(originalURL),
	)

	// XML属性转义
	return xmlEscape(raw)
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
		path:   r.URL.String(),
		addr:   getRemoteAddr(r),
		start:  time.Now(),
	}
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
	// 1. 尝试获取X-Forwarded-For（代理情况）
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// 2. 尝试获取X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// 3. 从RemoteAddr提取（移除端口）
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


