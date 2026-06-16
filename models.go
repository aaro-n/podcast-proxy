package main

// ProxyConfig 代理配置
type ProxyConfig struct {
	APIKey      string
	Port        string
	ForceHTTPS  bool
	PublicHost  string
	Timeout     int // 秒
}

// ProxyResource 代理资源类型
type ProxyResource string

const (
	ResourceAudio ProxyResource = "audio"
	ResourceImage ProxyResource = "image"
	ResourceStyle ProxyResource = "style"
	ResourceFeed  ProxyResource = "feed"
)


