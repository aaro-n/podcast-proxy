// config.go
package main
 
import (
    "log"
    "os"
    "time"
)
 
type Config struct {
    APIKey           string
    Username         string
    Password         string
    Port             string
    CacheExpiration  time.Duration
    MaxRetries       int
    RequestTimeout   time.Duration
    AllowedHosts     []string
}
 
func LoadConfig() *Config {
    apiKey := os.Getenv("API_KEY")
    if apiKey == "" {
        log.Fatal("FATAL: API_KEY environment variable is not set.")
    }
 
    username := os.Getenv("USERNAME")
    password := os.Getenv("PASSWORD")
 
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
 
    return &Config{
        APIKey:          apiKey,
        Username:        username,
        Password:        password,
        Port:            port,
        CacheExpiration: 10 * time.Minute,
        MaxRetries:      5,  // 增加重试次数
        RequestTimeout:  60 * time.Second,  // 增加超时时间
        AllowedHosts:    []string{}, // 空表示允许所有，可配置白名单
    }
}
