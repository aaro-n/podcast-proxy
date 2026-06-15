package main

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// CacheManager 缓存管理器 - 支持ETag和HTTP缓存控制
type CacheManager struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	ttl     time.Duration // 缓存过期时间
}

// NewCacheManager 创建缓存管理器
func NewCacheManager(ttl time.Duration) *CacheManager {
	cm := &CacheManager{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
	}
	// 启动清理协程
	go cm.cleanup()
	return cm
}

// GetCacheKey 生成缓存key（URL+资源类型）
func (*CacheManager) GetCacheKey(resourceType string, url string) string {
	h := md5.Sum([]byte(url))
	return fmt.Sprintf("%s:%x", resourceType, h)
}

// Get 从缓存获取条目
func (cm *CacheManager) Get(key string) (*CacheEntry, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	entry, exists := cm.entries[key]
	if !exists {
		return nil, false
	}

	// 检查是否过期
	if time.Since(entry.timestamp) > cm.ttl {
		return nil, false
	}

	return entry, true
}

// Set 存储条目到缓存
func (cm *CacheManager) Set(key string, entry *CacheEntry) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	entry.timestamp = time.Now()
	cm.entries[key] = entry
}

// Clear 清空缓存
func (cm *CacheManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.entries = make(map[string]*CacheEntry)
}

// cleanup 定期清理过期条目
func (cm *CacheManager) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cm.mu.Lock()
		now := time.Now()
		for key, entry := range cm.entries {
			if now.Sub(entry.timestamp) > cm.ttl {
				delete(cm.entries, key)
			}
		}
		cm.mu.Unlock()
	}
}

// ETagHelper ETag助手 - 生成和验证ETag
type ETagHelper struct{}

// GenerateETag 生成ETag
// 基于内容哈希
func (*ETagHelper) GenerateETag(data []byte) string {
	h := md5.Sum(data)
	return fmt.Sprintf(`"%x"`, h)
}

// GenerateWeakETag 生成弱ETag
func (*ETagHelper) GenerateWeakETag(data []byte) string {
	h := md5.Sum(data)
	return fmt.Sprintf(`W/"%x"`, h)
}

// CheckMatch 检查ETag是否匹配
func (*ETagHelper) CheckMatch(etag string, clientETag string) bool {
	// 移除弱ETag前缀
	normalizeETag := func(e string) string {
		if len(e) > 2 && e[:2] == `W/` {
			e = e[2:]
		}
		return e
	}

	return normalizeETag(etag) == normalizeETag(clientETag)
}

// CacheResponseHelper 缓存响应助手
type CacheResponseHelper struct {
	cacheManager *CacheManager
	etagHelper   *ETagHelper
}

// NewCacheResponseHelper 创建缓存响应助手
func NewCacheResponseHelper(cacheManager *CacheManager) *CacheResponseHelper {
	return &CacheResponseHelper{
		cacheManager: cacheManager,
		etagHelper:   &ETagHelper{},
	}
}

// HandleCachedResponse 处理缓存响应
// 返回 (shouldUseCache, statusCode)
func (crh *CacheResponseHelper) HandleCachedResponse(
	w http.ResponseWriter,
	r *http.Request,
	resourceType string,
	url string,
	responseHeaders http.Header,
) (bool, int) {
	cacheKey := crh.cacheManager.GetCacheKey(resourceType, url)

	// 检查客户端是否发送了If-None-Match
	clientETag := r.Header.Get("If-None-Match")
	if clientETag != "" {
		// 检查缓存中是否有对应的ETag
		if entry, exists := crh.cacheManager.Get(cacheKey); exists {
			if crh.etagHelper.CheckMatch(entry.ETag, clientETag) {
				// ETag匹配，返回304
				w.WriteHeader(http.StatusNotModified)
				return false, http.StatusNotModified
			}
		}

		// 或者检查源响应的ETag
		if sourceETag := responseHeaders.Get("ETag"); sourceETag != "" {
			if crh.etagHelper.CheckMatch(sourceETag, clientETag) {
				w.WriteHeader(http.StatusNotModified)
				return false, http.StatusNotModified
			}
		}
	}

	return true, http.StatusOK
}

// SetCacheHeaders 设置缓存相关响应头
func (crh *CacheResponseHelper) SetCacheHeaders(
	w http.ResponseWriter,
	eTag string,
	cacheControlMax int, // 秒数，0表示不缓存
) {
	w.Header().Set("ETag", eTag)

	if cacheControlMax > 0 {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", cacheControlMax))
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	w.Header().Set("Vary", "Accept-Encoding")
}

// CacheEntry扩展字段
type CacheEntry struct {
	ETag      string
	ContentType string
	Data      []byte
	Headers   map[string]string
	timestamp time.Time
}
