package main

import (
	"log"
	"os"
	"strconv"
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

	log.Printf("配置初始化完成 - Port: %s, ForceHTTPS: %v, Timeout: %ds, MediaDirectRedirect: %v", 
		config.Port, config.ForceHTTPS, config.Timeout, config.MediaDirectRedirect)
	return nil
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
