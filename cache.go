package main

import (
	"crypto/md5"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	ETag        string
	ContentType string
	Data        []byte
	Headers     map[string]string
	timestamp   time.Time
}

// FeedCacheEntry stores a transformed feed alongside the upstream validators
// used to refresh it without confusing source and representation ETags.
type FeedCacheEntry struct {
	Body               []byte
	ETag               string
	SourceETag         string
	SourceLastModified string
	SourceCacheControl string
	SourceExpires      string
	SourceDate         string
	SourceVary         string
	SourceAge          time.Duration
	SourceAgePresent   bool
	StaleIfError       time.Duration
	Vary               string
	LastModified       string
	CacheControl       string
	ContentType        string
	StoredAt           time.Time
	FreshnessLifetime  time.Duration
	ExpiresAt          time.Time
	NoCache            bool
	NoStore            bool
	Private            bool
	Stale              bool
	Cacheable          bool
	VaryRequestHeaders map[string]string
}

type FeedCache struct {
	mu         sync.Mutex
	entries    map[string]FeedCacheEntry
	maxEntries int
	maxBytes   int64
	bytes      int64
}

const defaultNoValidatorRecheckInterval = 30 * time.Second

func NewFeedCache(maxEntries int, maxBytes int64) *FeedCache {
	return &FeedCache{entries: make(map[string]FeedCacheEntry), maxEntries: maxEntries, maxBytes: maxBytes}
}

func FeedCacheKey(feedURL, scheme, host, apiKey string) string {
	keyDigest := sha256.Sum256([]byte(apiKey))
	digest := sha256.Sum256([]byte(feedURL + "\n" + scheme + "\n" + host + "\n" + fmt.Sprintf("%x", keyDigest)))
	return fmt.Sprintf("%x", digest)
}

func requestVariantFingerprint(headers http.Header) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, strings.ToLower(http.CanonicalHeaderKey(name)))
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		value := strings.Join(headers.Values(http.CanonicalHeaderKey(name)), ",")
		fmt.Fprintf(&canonical, "%d:%s=%d:%s\n", len(name), name, len(value), value)
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return fmt.Sprintf("%x", digest)
}

func feedCacheVariantHeaders(vary string, request http.Header) (map[string]string, bool) {
	values := make(map[string]string)
	for _, field := range strings.Split(vary, ",") {
		name := http.CanonicalHeaderKey(strings.TrimSpace(field))
		if name == "" {
			continue
		}
		if name == "*" {
			return nil, false
		}
		values[strings.ToLower(name)] = strings.Join(request.Values(name), ",")
	}
	return values, true
}

func (entry FeedCacheEntry) matchesRequestVary(request http.Header) bool {
	for name, value := range entry.VaryRequestHeaders {
		if strings.Join(request.Values(http.CanonicalHeaderKey(name)), ",") != value {
			return false
		}
	}
	return true
}

func (fc *FeedCache) Get(key string) (FeedCacheEntry, bool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	entry, ok := fc.entries[key]
	if !ok {
		return FeedCacheEntry{}, false
	}
	// no-cache/must-revalidate entries are retained for validator-based origin
	// revalidation. The handler must not serve them before revalidation.
	if (!entry.Cacheable && !entry.NoCache) || entry.NoStore || entry.Private {
		fc.deleteLocked(key)
		return FeedCacheEntry{}, false
	}
	entry.Body = append([]byte(nil), entry.Body...)
	entry.VaryRequestHeaders = cloneStringMap(entry.VaryRequestHeaders)
	return entry, true
}

func (fc *FeedCache) Set(key string, entry FeedCacheEntry) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.maxEntries <= 0 || fc.maxBytes <= 0 || entry.NoStore || entry.Private || (!entry.Cacheable && !entry.NoCache) || int64(len(entry.Body)) > fc.maxBytes {
		fc.deleteLocked(key)
		return
	}
	if old, ok := fc.entries[key]; ok {
		fc.bytes -= int64(len(old.Body))
	}
	entry.Body = append([]byte(nil), entry.Body...)
	entry.VaryRequestHeaders = cloneStringMap(entry.VaryRequestHeaders)
	fc.entries[key] = entry
	fc.bytes += int64(len(entry.Body))
	for len(fc.entries) > fc.maxEntries || fc.bytes > fc.maxBytes {
		var oldestKey string
		var oldest time.Time
		for candidate, cached := range fc.entries {
			if oldestKey == "" || cached.ExpiresAt.Before(oldest) {
				oldestKey, oldest = candidate, cached.ExpiresAt
			}
		}
		if oldestKey == "" {
			break
		}
		fc.deleteLocked(oldestKey)
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	copy := make(map[string]string, len(input))
	for key, value := range input {
		copy[key] = value
	}
	return copy
}

func (fc *FeedCache) Delete(key string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.deleteLocked(key)
}

func (fc *FeedCache) deleteLocked(key string) {
	if old, ok := fc.entries[key]; ok {
		fc.bytes -= int64(len(old.Body))
		delete(fc.entries, key)
	}
}

func (fc *FeedCache) Len() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.entries)
}

func feedCacheTTL(cacheControl string) (time.Duration, bool) {
	if cacheControl == "" {
		return 0, false
	}
	var (
		maxAge    time.Duration
		sMaxAge   time.Duration
		hasMaxAge bool
		hasSMax   bool
	)
	noStore := feedCacheDirective(cacheControl, "no-store")
	if noStore {
		return 0, true
	}
	for _, directive := range strings.Split(strings.ToLower(cacheControl), ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		name = strings.TrimSpace(name)
		if name == "private" || name == "no-store" || name == "no-cache" {
			continue
		}
		if !found || (name != "max-age" && name != "s-maxage") {
			continue
		}
		seconds, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64)
		if err != nil || seconds < 0 {
			continue
		}
		maxSeconds := int64(time.Hour / time.Second)
		if seconds > maxSeconds {
			seconds = maxSeconds
		}
		freshness := time.Duration(seconds) * time.Second
		if name == "s-maxage" {
			sMaxAge, hasSMax = freshness, true
		} else {
			maxAge, hasMaxAge = freshness, true
		}
	}
	if hasSMax {
		return sMaxAge, false
	}
	if hasMaxAge {
		return maxAge, false
	}
	return 0, false
}

func feedCacheDirective(cacheControl, directiveName string) bool {
	directiveName = strings.ToLower(strings.TrimSpace(directiveName))
	for _, directive := range strings.Split(strings.ToLower(cacheControl), ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(directive), "=")
		if strings.TrimSpace(name) == directiveName {
			return true
		}
	}
	return false
}

func feedHasExplicitFreshness(cacheControl string) bool {
	for _, directive := range strings.Split(strings.ToLower(cacheControl), ",") {
		name, _, hasValue := strings.Cut(strings.TrimSpace(directive), "=")
		if hasValue && (strings.TrimSpace(name) == "max-age" || strings.TrimSpace(name) == "s-maxage") {
			return true
		}
	}
	return false
}

func cacheControlHasInvalidMaxAge(cacheControl string) bool {
	for _, directive := range strings.Split(strings.ToLower(cacheControl), ",") {
		name, value, hasValue := strings.Cut(strings.TrimSpace(directive), "=")
		name = strings.TrimSpace(name)
		if name != "max-age" && name != "s-maxage" {
			continue
		}
		if !hasValue {
			return true
		}
		seconds, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64)
		if err != nil || seconds < 0 {
			return true
		}
	}
	return false
}

// feedCacheTTLFromHeaders calculates remaining freshness at receipt time.
// Age is subtracted from max-age/s-maxage; Expires/Date is used when the origin
// did not specify an explicit freshness lifetime.
func feedCacheTTLFromHeaders(headers http.Header, now time.Time) (ttl, lifetime, sourceAge time.Duration, noStore bool, private bool, noCache bool) {
	cacheControl := headers.Get("Cache-Control")
	lifetime, noStore = feedCacheTTL(cacheControl)
	private = feedCacheDirective(cacheControl, "private")
	noCache = feedCacheDirective(cacheControl, "no-cache")
	if cacheControlHasInvalidMaxAge(cacheControl) {
		noCache = true
	}

	if !feedHasExplicitFreshness(cacheControl) && !noStore && !private && !noCache {
		if expires, err := http.ParseTime(headers.Get("Expires")); err == nil {
			base := now
			if date, err := http.ParseTime(headers.Get("Date")); err == nil {
				base = date
			}
			lifetime = expires.Sub(base)
			if lifetime < 0 {
				lifetime = 0
			}
		} else if headers.Get("Expires") == "" {
			// HTTP heuristic freshness is at most 10% of the resource age;
			// use it only if the source sent a valid Last-Modified value.
			if modified, err := http.ParseTime(headers.Get("Last-Modified")); err == nil {
				resourceAge := now.Sub(modified)
				if resourceAge > 0 {
					lifetime = resourceAge / 10
					if lifetime > time.Hour {
						lifetime = time.Hour
					}
				}
			}
		}
	}

	var validAge bool
	sourceAge, validAge = feedAgeFromHeaders(headers, now)
	if !validAge {
		noCache = true
	}
	ttl = lifetime - sourceAge
	if noCache || ttl < 0 {
		ttl = 0
		noCache = true
	}
	if ttl <= 0 {
		ttl = 0
		if !noStore {
			noCache = true
		}
	}
	return ttl, lifetime, sourceAge, noStore, private, noCache
}

func feedAgeFromHeaders(headers http.Header, now time.Time) (time.Duration, bool) {
	var ageValue time.Duration
	if rawAge := strings.TrimSpace(headers.Get("Age")); rawAge != "" {
		seconds, err := strconv.ParseInt(rawAge, 10, 64)
		if err != nil || seconds < 0 {
			return 0, false
		}
		maxAgeSeconds := int64((time.Duration(1<<63 - 1)) / time.Second)
		if seconds > maxAgeSeconds {
			ageValue = time.Duration(1<<63 - 1)
		} else {
			ageValue = time.Duration(seconds) * time.Second
		}
	}
	if date, err := http.ParseTime(headers.Get("Date")); err == nil {
		apparentAge := now.Sub(date)
		if apparentAge > ageValue {
			ageValue = apparentAge
		}
	}
	return ageValue, true
}

func currentFeedAge(entry FeedCacheEntry, now time.Time) time.Duration {
	age := entry.SourceAge
	if entry.StoredAt.IsZero() {
		return age
	}
	resident := now.Sub(entry.StoredAt)
	if resident > 0 && resident < time.Duration(1<<63-1)-age {
		age += resident
	}
	return age
}

func mergeVaryHeaders(values ...string) string {
	seen := make(map[string]struct{})
	merged := make([]string, 0, len(values))
	for _, value := range values {
		for _, field := range strings.Split(value, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			if field == "*" {
				return "*"
			}
			key := strings.ToLower(field)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, field)
		}
	}
	return strings.Join(merged, ", ")
}

func feedStaleIfError(cacheControl string) time.Duration {
	for _, directive := range strings.Split(strings.ToLower(cacheControl), ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if strings.TrimSpace(name) != "stale-if-error" || !found {
			continue
		}
		seconds, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64)
		if err != nil || seconds < 0 {
			return 0
		}
		maxSeconds := int64((24 * time.Hour) / time.Second)
		if seconds > maxSeconds {
			seconds = maxSeconds
		}
		return time.Duration(seconds) * time.Second
	}
	return 0
}

func varyHeaderHasStar(vary string) bool {
	for _, field := range strings.Split(vary, ",") {
		if strings.TrimSpace(field) == "*" {
			return true
		}
	}
	return false
}

func feedHasNoCacheDirective(cacheControl string) bool {
	for _, directive := range strings.Split(strings.ToLower(cacheControl), ",") {
		if strings.TrimSpace(directive) == "no-cache" {
			return true
		}
	}
	return false
}
