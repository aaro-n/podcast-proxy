// middleware.go  
package main  
  
import (  
    "crypto/subtle"  
    "log"  
    "net/http"  
    "strings"  
)  
  
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {  
    return func(w http.ResponseWriter, r *http.Request) {  
        // 优先从 Authorization header 获取 API Key  
        var userAPIKey string  
        authHeader := r.Header.Get("Authorization")  
        if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {  
            userAPIKey = strings.TrimPrefix(authHeader, "Bearer ")  
        } else {  
            // 回退到 URL 参数  
            userAPIKey = r.URL.Query().Get("apikey")  
        }  
          
        if userAPIKey == "" {  
            log.Printf("Missing API key from %s", r.RemoteAddr)  
            http.Error(w, "Unauthorized: Missing API Key", http.StatusUnauthorized)  
            return  
        }  
          
        // 使用恒定时间比较来防止计时攻击  
        apiKeyBytes := []byte(config.APIKey)  
        userApiKeyBytes := []byte(userAPIKey)  
          
        if len(apiKeyBytes) != len(userApiKeyBytes) || subtle.ConstantTimeCompare(apiKeyBytes, userApiKeyBytes) != 1 {  
            log.Printf("Unauthorized access attempt from %s", r.RemoteAddr)  
            http.Error(w, "Unauthorized: Invalid API Key", http.StatusUnauthorized)  
            return  
        }  
          
        next(w, r)  
    }  
}
