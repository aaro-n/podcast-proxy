// middleware.go
package main

import (
        "crypto/subtle"
        "log"
        "net/http"
)

// authMiddleware 包装一个 HandlerFunc，为其增加 API Key 认证功能
// 它会检查请求 URL 中的 "apikey" 参数
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
        return func(w http.ResponseWriter, r *http.Request) {
                userAPIKey := r.URL.Query().Get("apikey")

                // 使用恒定时间比较来防止计时攻击，增强安全性
                apiKeyBytes := []byte(serverAPIKey)
                userApiKeyBytes := []byte(userAPIKey)

                // 检查 key 长度是否一致，以及内容是否匹配
                if len(apiKeyBytes) != len(userApiKeyBytes) || subtle.ConstantTimeCompare(apiKeyBytes, userApiKeyBytes) != 1 {
                        log.Printf("Unauthorized access attempt from %s with key '%s'", r.RemoteAddr, userAPIKey)
                        http.Error(w, "Unauthorized: Invalid API Key", http.StatusUnauthorized)
                        return
                }

                // 如果 key 有效，则继续处理请求
                next(w, r)
        }
}
