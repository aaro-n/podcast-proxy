package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

var config *ProxyConfig

// InitConfig 初始化配置
func InitConfig() error {
	config = &ProxyConfig{
		Port:    "8080",
		Timeout: 30,
	}

	// 读取API Key
	apiKey := os.Getenv("PODCAST_PROXY_APIKEY")
	if apiKey == "" {
		apiKey = os.Getenv("API_KEY")
	}
	if apiKey == "" {
		log.Fatal("请设置环境变量 PODCAST_PROXY_APIKEY 或 API_KEY")
	}
	config.APIKey = apiKey

	// 读取其他配置
	if port := os.Getenv("PORT"); port != "" {
		config.Port = port
	}

	config.ForceHTTPS = os.Getenv("FORCE_HTTPS") == "true"

	if host := os.Getenv("PUBLIC_HOST"); host != "" {
		config.PublicHost = host
	}

	if timeout := os.Getenv("TIMEOUT"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil {
			config.Timeout = t
		}
	}

	config.MediaDirectRedirect = os.Getenv("MEDIA_DIRECT_REDIRECT") == "true"

	// 出站守卫：默认开启，只有显式 EGRESS_GUARD=false 才关闭
	config.EgressGuard = os.Getenv("EGRESS_GUARD") != "false"
	config.EgressAllowPrivate = os.Getenv("EGRESS_ALLOW_PRIVATE") == "true"
	config.EgressAllowedPorts = parsePortSet(os.Getenv("EGRESS_ALLOWED_PORTS"), "80,443")

	// 媒体并发上限
	config.MediaMaxConcurrency = 32
	if n := os.Getenv("MEDIA_MAX_CONCURRENCY"); n != "" {
		if v, err := strconv.Atoi(n); err == nil && v > 0 {
			config.MediaMaxConcurrency = v
		}
	}

	log.Printf("配置初始化完成 - Port: %s, ForceHTTPS: %v, Timeout: %ds, MediaDirectRedirect: %v",
		config.Port, config.ForceHTTPS, config.Timeout, config.MediaDirectRedirect)
	log.Printf("出站守卫 - 启用: %v, 允许内网: %v, 允许端口: %s, 媒体并发上限: %d",
		config.EgressGuard, config.EgressAllowPrivate, os.Getenv("EGRESS_ALLOWED_PORTS"), config.MediaMaxConcurrency)
	return nil
}

// parsePortSet 解析逗号分隔的端口白名单，非法项自动忽略；为空时回退到 fallback
func parsePortSet(raw, fallback string) map[string]bool {
	add := func(dst map[string]bool, s string) {
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			n, err := strconv.Atoi(p)
			if err != nil || n <= 0 || n > 65535 {
				continue
			}
			dst[strconv.Itoa(n)] = true
		}
	}

	set := make(map[string]bool)
	add(set, raw)
	if len(set) == 0 {
		add(set, fallback)
	}
	return set
}

// GetConfig 获取全局配置
func GetConfig() *ProxyConfig {
	if config == nil {
		InitConfig()
	}
	return config
}

// GetHTTPClient 获取HTTP客户端（全局复用）
func GetHTTPClient() *HTTPClientManager {
	return getSharedHTTPClient()
}

// getSharedHTTPClient 内部全局HTTP客户端
var _httpClient *HTTPClientManager

func getSharedHTTPClient() *HTTPClientManager {
	if _httpClient == nil {
		cfg := GetConfig()
		_httpClient = &HTTPClientManager{
			client: nil, // 延迟初始化
		}
		_httpClient.init(time.Duration(cfg.Timeout) * time.Second)
	}
	return _httpClient
}
