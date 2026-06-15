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

// buildURL 内部构建代理URL
func (b *ProxyURLBuilder) buildURL(resourceType, apikey, originalURL string) string {
	auth := NewAuthManager()
	encodedKey := auth.EncodeKey(apikey)

	raw := fmt.Sprintf("%s://%s/%s/%s?url=%s",
		b.scheme,
		b.host,
		resourceType,
		url.PathEscape(encodedKey),
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

// IsAudioURL 判断是否是音频URL
func (*StringHelper) IsAudioURL(u string) bool {
	u = strings.ToLower(u)
	audioExts := []string{".mp3", ".m4a", ".aac", ".ogg", ".wav", ".flac"}
	for _, ext := range audioExts {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}

// RangeHelper Range请求助手
type RangeHelper struct{}

// ParseRange 解析Range请求头
// 返回 (start, end, total, ok)
func (*RangeHelper) ParseRange(rangeHeader string, contentLength int64) (int64, int64, int64, bool) {
	if rangeHeader == "" {
		return 0, contentLength - 1, contentLength, false
	}

	// 简单解析，格式：bytes=start-end
	if strings.HasPrefix(rangeHeader, "bytes=") {
		spec := strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.Split(spec, "-")
		if len(parts) == 2 {
			// bytes=0-100
			if parts[0] != "" && parts[1] != "" {
				var start, end int64
				fmt.Sscanf(parts[0], "%d", &start)
				fmt.Sscanf(parts[1], "%d", &end)
				if start >= 0 && end >= start && end < contentLength {
					return start, end, contentLength, true
				}
			}
		}
	}

	return 0, contentLength - 1, contentLength, false
}
