package main

// ProxyConfig 代理配置
type ProxyConfig struct {
	APIKey               string
	Port                 string
	ForceHTTPS           bool
	PublicHost           string
	Timeout              int // 秒
	MediaDirectRedirect  bool // 音视频媒体资源是否直接 302 重定向直连（不通过 VPS 代理中转）
}

// ProxyResource 代理资源类型
type ProxyResource string

const (
	ResourceAudio ProxyResource = "audio"
	ResourceImage ProxyResource = "image"
	ResourceStyle ProxyResource = "style"
	ResourceFeed  ProxyResource = "feed"
)


