// cache.go  
package main  
  
import (  
    "sync"  
    "time"  
)  
  
type CacheItem struct {  
    Data      []byte  
    ExpiresAt time.Time  
}  
  
type Cache struct {  
    items map[string]*CacheItem  
    mutex sync.RWMutex  
}  
  
func NewCache() *Cache {  
    cache := &Cache{  
        items: make(map[string]*CacheItem),  
    }  
      
    // 启动清理协程  
    go cache.cleanup()  
    return cache  
}  
  
func (c *Cache) Get(key string) ([]byte, bool) {  
    c.mutex.RLock()  
    defer c.mutex.RUnlock()  
      
    item, exists := c.items[key]  
    if !exists || time.Now().After(item.ExpiresAt) {  
        return nil, false  
    }  
      
    return item.Data, true  
}  
  
func (c *Cache) Set(key string, data []byte, expiration time.Duration) {  
    c.mutex.Lock()  
    defer c.mutex.Unlock()  
      
    c.items[key] = &CacheItem{  
        Data:      data,  
        ExpiresAt: time.Now().Add(expiration),  
    }  
}  
  
func (c *Cache) cleanup() {  
    ticker := time.NewTicker(5 * time.Minute)  
    defer ticker.Stop()  
      
    for range ticker.C {  
        c.mutex.Lock()  
        now := time.Now()  
        for key, item := range c.items {  
            if now.After(item.ExpiresAt) {  
                delete(c.items, key)  
            }  
        }  
        c.mutex.Unlock()  
    }  
}
