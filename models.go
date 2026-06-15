package main

// ProxyConfig 代理配置
type ProxyConfig struct {
	APIKey      string
	Port        string
	ForceHTTPS  bool
	PublicHost  string
	Timeout     int // 秒
}

// RequestContext 请求上下文
type RequestContext struct {
	APIKey  string
	OrigURL string
	Path    string
}

// ProxyResource 代理资源类型
type ProxyResource string

const (
	ResourceAudio ProxyResource = "audio"
	ResourceImage ProxyResource = "image"
	ResourceStyle ProxyResource = "style"
	ResourceFeed  ProxyResource = "feed"
)

// FeedTransformResult RSS转换结果
type FeedTransformResult struct {
	Content     []byte
	ContentType string
	Headers     map[string]string
}
