package main

import (
	"encoding/base64"
	"net/http"
	"strings"
)

// AuthManager 认证管理器
type AuthManager struct {
	config *ProxyConfig
}

// NewAuthManager 创建认证管理器
func NewAuthManager() *AuthManager {
	return &AuthManager{
		config: GetConfig(),
	}
}

// ExtractAPIKey 从请求中提取API Key
// 支持从 query 参数或 path 中提取
func (am *AuthManager) ExtractAPIKey(r *http.Request, pathPrefix string) string {
	// 1. 优先从 query 参数获取
	if apikey := r.URL.Query().Get("apikey"); apikey != "" {
		return am.decodeKey(apikey)
	}

	// 2. 从 path 中提取（支持 /audio/base64key?url=...）
	if pathPrefix != "" {
		if p := strings.TrimPrefix(r.URL.Path, pathPrefix); p != "" {
			parts := strings.SplitN(p, "/", 2)
			if len(parts) > 0 {
				return am.decodeKey(parts[0])
			}
		}
	}

	return ""
}

// VerifyAPIKey 验证API Key
func (am *AuthManager) VerifyAPIKey(apikey string) bool {
	return apikey == am.config.APIKey
}

// VerifyRequest 验证请求的API Key
func (am *AuthManager) VerifyRequest(r *http.Request, pathPrefix string) (string, bool) {
	apikey := am.ExtractAPIKey(r, pathPrefix)
	if apikey == "" {
		return "", false
	}
	return apikey, am.VerifyAPIKey(apikey)
}

// EncodeKey Base64编码API Key
func (am *AuthManager) EncodeKey(apikey string) string {
	return base64.StdEncoding.EncodeToString([]byte(apikey))
}

// decodeKey Base64解码API Key（内部方法）
func (am *AuthManager) decodeKey(encoded string) string {
	// 尝试Base64解码
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return string(decoded)
	}
	// 如果解码失败，直接返回原始值
	return encoded
}
