package main

// ProxyConfig 代理配置
type ProxyConfig struct {
	APIKey              string
	Port                string
	ForceHTTPS          bool
	PublicHost          string
	Timeout             int  // 秒
	MediaDirectRedirect bool // 音视频媒体资源是否直接 302 重定向直连（不通过 VPS 代理中转）

	// 出站守卫：限制代理可以向哪里发起请求，避免被当成开放代理滥用
	EgressGuard        bool            // 是否启用出站守卫
	EgressAllowPrivate bool            // 是否允许访问内网地址（个人局域网源站场景）
	EgressAllowedPorts map[string]bool // 允许的出站端口白名单

	// 媒体并发上限：限制同时进行中的音频/图片回源数量
	MediaMaxConcurrency int
}

// ProxyResource 代理资源类型
type ProxyResource string

const (
	ResourceAudio ProxyResource = "audio"
	ResourceImage ProxyResource = "image"
	ResourceStyle ProxyResource = "style"
	ResourceFeed  ProxyResource = "feed"
)
