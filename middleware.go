// middleware.go  
package main  
  
import (  
    "crypto/subtle"  
    "encoding/base64"  
    "log"  
    "net/http"  
    "strings"  
)  
  
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {  
    return func(w http.ResponseWriter, r *http.Request) {  
        // === 优先检查API Key认证 ===  
        var userAPIKey string  
        authHeader := r.Header.Get("Authorization")  
        if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {  
            userAPIKey = strings.TrimPrefix(authHeader, "Bearer ")  
        } else {  
            // 回退到 URL 参数  
            userAPIKey = r.URL.Query().Get("apikey")  
        }  
          
        // 如果提供了API Key，则使用API Key认证  
        if userAPIKey != "" {  
            apiKeyBytes := []byte(config.APIKey)  
            userApiKeyBytes := []byte(userAPIKey)  
              
            if len(apiKeyBytes) == len(userApiKeyBytes) && subtle.ConstantTimeCompare(apiKeyBytes, userApiKeyBytes) == 1 {  
                log.Printf("API Key authentication successful from %s", r.RemoteAddr)  
                next(w, r)  
                return  
            } else {  
                log.Printf("Invalid API Key from %s", r.RemoteAddr)  
                http.Error(w, "Unauthorized: Invalid API Key", http.StatusUnauthorized)  
                return  
            }  
        }  
          
        // === 如果没有API Key，则检查用户认证 ===  
        if config.Username != "" && config.Password != "" {  
            var username, password string  
              
            // 尝试从 Authorization header 获取 Basic Auth  
            if authHeader != "" && strings.HasPrefix(authHeader, "Basic ") {  
                // 解码 Basic Auth  
                encoded := strings.TrimPrefix(authHeader, "Basic ")  
                decoded, err := base64.StdEncoding.DecodeString(encoded)  
                if err == nil {  
                    creds := strings.SplitN(string(decoded), ":", 2)  
                    if len(creds) == 2 {  
                        username = creds[0]  
                        password = creds[1]  
                    }  
                }  
            }  
              
            // 如果没有从 header 获取到，尝试从 URL 参数获取  
            if username == "" {  
                username = r.URL.Query().Get("username")  
                password = r.URL.Query().Get("password")  
            }  
              
            if username == "" || password == "" {  
                log.Printf("Missing username or password from %s", r.RemoteAddr)  
                // 发送 401 和 WWW-Authenticate header 提示客户端使用 Basic Auth  
                w.Header().Set("WWW-Authenticate", `Basic realm="Podcast Proxy"`)  
                http.Error(w, "Unauthorized: Missing username or password", http.StatusUnauthorized)  
                return  
            }  
              
            // 使用恒定时间比较来防止计时攻击  
            configUsernameBytes := []byte(config.Username)  
            configPasswordBytes := []byte(config.Password)  
            userUsernameBytes := []byte(username)  
            userPasswordBytes := []byte(password)  
              
            usernameMatch := len(configUsernameBytes) == len(userUsernameBytes) &&  
                subtle.ConstantTimeCompare(configUsernameBytes, userUsernameBytes) == 1  
            passwordMatch := len(configPasswordBytes) == len(userPasswordBytes) &&  
                subtle.ConstantTimeCompare(configPasswordBytes, userPasswordBytes) == 1  
              
            if usernameMatch && passwordMatch {  
                log.Printf("User authentication successful for user '%s' from %s", username, r.RemoteAddr)  
                next(w, r)  
                return  
            } else {  
                log.Printf("Invalid username or password from %s", r.RemoteAddr)  
                w.Header().Set("WWW-Authenticate", `Basic realm="Podcast Proxy"`)  
                http.Error(w, "Unauthorized: Invalid username or password", http.StatusUnauthorized)  
                return  
            }  
        }  
          
        // === 如果两种认证方式都没有配置或提供 ===  
        log.Printf("No authentication method available from %s", r.RemoteAddr)  
        http.Error(w, "Unauthorized: No authentication method configured", http.StatusUnauthorized)  
    }  
}
