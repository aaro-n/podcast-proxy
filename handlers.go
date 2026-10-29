package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type generateFeedLinkRequest struct {
	URL string `json:"url"`
}

type generateFeedLinkResponse struct {
	URL string `json:"url"`
}

func handleGenerateFeedLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !verifyBearerMasterKey(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request generateFeedLinkRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "request must contain a single JSON object", http.StatusBadRequest)
		return
	}
	validator := &FeedValidator{}
	if !validator.ValidateFeedURL(request.URL) {
		http.Error(w, "invalid feed URL", http.StatusBadRequest)
		return
	}
	builder := NewProxyURLBuilder(r, request.URL)
	if builder.host == "" {
		http.Error(w, "PUBLIC_HOST must be configured", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(generateFeedLinkResponse{URL: builder.BuildFeedURL(GetConfig().APIKey)})
}

func verifyBearerMasterKey(r *http.Request) bool {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	return hmac.Equal([]byte(provided), []byte(GetConfig().APIKey))
}

// 媒体回源并发限制
//
// 公网服务 + 可代理任意 URL = 可能被人当成免费出口/下载器。
// 限制同时进行中的回源请求数，可以避免几十个并发长连接把带宽和 goroutine 吃满。
var (
	mediaSlotsOnce sync.Once
	mediaSlots     chan struct{}
	feedSlotsOnce  sync.Once
	feedSlots      chan struct{}
	feedCacheOnce  sync.Once
	feedCache      *FeedCache
	feedLocksMu    sync.Mutex
	feedLocks      = make(map[string]*feedRequestLock)
)

type feedRequestLock struct {
	semaphore chan struct{}
	refs      int
}

// acquireMediaSlot 尝试获取一个媒体回源名额，成功返回释放函数
func acquireMediaSlotForRequest(r *http.Request) (func(), bool) {
	mediaSlotsOnce.Do(func() {
		n := GetConfig().MediaMaxConcurrency
		if n <= 0 {
			n = 32
		}
		mediaSlots = make(chan struct{}, n)
	})
	if r.Context().Err() != nil {
		return nil, false
	}
	select {
	case <-r.Context().Done():
		return nil, false
	default:
	}
	select {
	case mediaSlots <- struct{}{}:
		return func() { <-mediaSlots }, true
	case <-r.Context().Done():
		return nil, false
	default:
		return nil, false
	}
}

func acquireFeedSlotForRequest(r *http.Request) (func(), bool) {
	feedSlotsOnce.Do(func() {
		n := GetConfig().FeedMaxConcurrency
		if n <= 0 {
			n = 16
		}
		feedSlots = make(chan struct{}, n)
	})
	if r.Context().Err() != nil {
		return nil, false
	}
	select {
	case <-r.Context().Done():
		return nil, false
	default:
	}
	select {
	case feedSlots <- struct{}{}:
		return func() { <-feedSlots }, true
	case <-r.Context().Done():
		return nil, false
	default:
		return nil, false
	}
}

func getFeedCache() *FeedCache {
	feedCacheOnce.Do(func() {
		feedCache = NewFeedCache(128, 128<<20)
	})
	return feedCache
}

func acquireFeedLock(key string, ctxDone <-chan struct{}) (func(), bool) {
	feedLocksMu.Lock()
	lock, exists := feedLocks[key]
	if !exists {
		lock = &feedRequestLock{semaphore: make(chan struct{}, 1)}
		feedLocks[key] = lock
	}
	lock.refs++
	feedLocksMu.Unlock()
	select {
	case lock.semaphore <- struct{}{}:
		return func() {
			<-lock.semaphore
			feedLocksMu.Lock()
			lock.refs--
			if lock.refs == 0 && feedLocks[key] == lock {
				delete(feedLocks, key)
			}
			feedLocksMu.Unlock()
		}, true
	case <-ctxDone:
		feedLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 && len(lock.semaphore) == 0 && feedLocks[key] == lock {
			delete(feedLocks, key)
		}
		feedLocksMu.Unlock()
		return nil, false
	}
}

// HandlerBase 处理器基类
type HandlerBase struct {
	auth   *AuthManager
	logger *LoggerHelper
}

// newHandlerBase 创建处理器基类
func newHandlerBase(r *http.Request) *HandlerBase {
	return &HandlerBase{
		auth:   NewAuthManager(),
		logger: NewLoggerHelper(r),
	}
}

// FeedHandler 饲送处理器
type FeedHandler struct {
	*HandlerBase
	validator   *FeedValidator
	transformer *FeedTransformer
}

// NewFeedHandler 创建饲送处理器
func NewFeedHandler(r *http.Request) *FeedHandler {
	return &FeedHandler{
		HandlerBase: newHandlerBase(r),
		validator:   &FeedValidator{},
		transformer: NewFeedTransformer(),
	}
}

// Handle 处理饲送请求
func (fh *FeedHandler) Handle(w http.ResponseWriter, r *http.Request) {
	fh.logger.LogStart()
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		fh.logger.LogComplete(http.StatusMethodNotAllowed)
		return
	}

	// 验证API Key (若非法直接静默断开，防止被扫描)
	apikey, valid := fh.auth.VerifyRequest(r, "")
	if !valid || apikey == "" {
		RejectRequest(w)
		fh.logger.LogComplete(http.StatusUnauthorized)
		return
	}
	display := wantsHTMLPresentation(r)

	// 获取源URL
	feedURL := r.URL.Query().Get("url")
	feedToken := r.URL.Query().Get("feed_token")
	if !fh.validator.ValidateFeedURL(feedURL) {
		http.Error(w, "invalid feed url", http.StatusBadRequest)
		fh.logger.LogComplete(http.StatusBadRequest)
		return
	}
	// Accept a scoped feed token for PinePods subscriptions; the master API key
	// remains supported for the proxy owner's feed-generation requests.
	if feedToken != "" && !fh.auth.VerifyScopedFeedURL(feedURL, feedToken) {
		http.Error(w, "invalid feed token", http.StatusUnauthorized)
		fh.logger.LogComplete(http.StatusUnauthorized)
		return
	}
	clientIfNoneMatch := append([]string(nil), r.Header.Values("If-None-Match")...)
	builder := NewProxyURLBuilder(r, feedURL)
	if display && builder.host == "" {
		http.Error(w, "PUBLIC_HOST must be configured for browser presentation", http.StatusInternalServerError)
		fh.logger.LogComplete(http.StatusInternalServerError)
		return
	}
	upstreamUserAgent := r.Header.Get("User-Agent")
	if upstreamUserAgent == "" {
		upstreamUserAgent = "PinePods/1.0 PodcastProxy/2.1"
	}
	upstreamVaryHeaders := make(http.Header)
	upstreamVaryHeaders.Set("User-Agent", upstreamUserAgent)
	upstreamVaryHeaders.Set("Accept", "*/*")
	if acceptLanguage := r.Header.Get("Accept-Language"); acceptLanguage != "" {
		upstreamVaryHeaders.Set("Accept-Language", acceptLanguage)
	}
	upstreamVaryHeaders.Set("Accept-Encoding", "gzip, identity;q=0.5")
	if rangedFeedRequest(r) {
		upstreamVaryHeaders.Set("Accept-Encoding", "identity")
	}
	variantURL := feedURL + "\ndisplay=" + strconv.FormatBool(display) + "\nfeed_token=" + feedToken + "\nupstream-headers=" + requestVariantFingerprint(upstreamVaryHeaders)
	cacheKey := FeedCacheKey(variantURL, builder.scheme, builder.host, apikey)
	cache := getFeedCache()
	cached, hasCached := FeedCacheEntry{}, false
	// The original feed URL and scoped token are part of the key. Cache query
	// feeds too; their query string is retained as origin identity and validator
	// values are only reused for that exact URL.
	cacheableFeedURL := true
	cached, hasCached = cache.Get(cacheKey)
	if hasCached && !rangedFeedRequest(r) && !shouldForwardConditionalToOrigin(r, display, cached) && cached.Cacheable && !cached.NoCache && time.Now().Before(cached.ExpiresAt) {
		serveCachedFeed(w, r, clientIfNoneMatch, cached)
		fh.logger.LogComplete(statusForCachedFeed(r, clientIfNoneMatch, cached))
		return
	}
	unlock, ok := acquireFeedLock(cacheKey, r.Context().Done())
	if !ok {
		fh.logger.LogComplete(http.StatusRequestTimeout)
		return
	}
	defer unlock()
	cached, hasCached = cache.Get(cacheKey)
	if hasCached && !rangedFeedRequest(r) && !shouldForwardConditionalToOrigin(r, display, cached) && cached.Cacheable && !cached.NoCache && time.Now().Before(cached.ExpiresAt) {
		serveCachedFeed(w, r, clientIfNoneMatch, cached)
		fh.logger.LogComplete(statusForCachedFeed(r, clientIfNoneMatch, cached))
		return
	}
	if builder.host == "" {
		http.Error(w, "PUBLIC_HOST must be configured for proxy feeds", http.StatusInternalServerError)
		fh.logger.LogComplete(http.StatusInternalServerError)
		return
	}

	// Never send the client's transformed-representation validator upstream.
	// Validate the cached source body with the origin's validators only.
	upstreamRequest := r.Clone(r.Context())
	upstreamRequest.Header = r.Header.Clone()
	upstreamRequest.Header.Set("User-Agent", upstreamUserAgent)
	upstreamRequest.Header.Set("Accept", "*/*")
	upstreamRequest.Header.Set("Accept-Encoding", upstreamVaryHeaders.Get("Accept-Encoding"))
	upstreamRequest.Header.Del("Range")
	upstreamRequest.Header.Del("If-Range")
	upstreamRequest.Header.Del("If-None-Match")
	upstreamRequest.Header.Del("If-Modified-Since")
	clientConditionalForwarded := false
	if !rangedFeedRequest(r) {
		clientIfModifiedSince := r.Header.Get("If-Modified-Since")
		if !display && len(clientIfNoneMatch) > 0 && (!hasCached || cached.SourceETag != "") {
			// For RSS/Atom, the origin ETag is the public ETag by design. Forward
			// the subscriber's validator unchanged so the origin decides 304 vs 200.
			upstreamRequest.Header[http.CanonicalHeaderKey("If-None-Match")] = append([]string(nil), clientIfNoneMatch...)
			clientConditionalForwarded = true
		} else if !display && len(clientIfNoneMatch) == 0 && clientIfModifiedSince != "" && (!hasCached || cached.SourceLastModified != "") {
			upstreamRequest.Header.Set("If-Modified-Since", clientIfModifiedSince)
			clientConditionalForwarded = true
		} else if hasCached && cacheableFeedURL {
			// Browser HTML uses a locally generated representation ETag, so its
			// validator must not be sent upstream. For an unconditional refresh,
			// use the validators belonging to the cached source document.
			if cached.SourceETag != "" {
				upstreamRequest.Header.Set("If-None-Match", cached.SourceETag)
			}
			if cached.SourceLastModified != "" && upstreamRequest.Header.Get("If-None-Match") == "" {
				upstreamRequest.Header.Set("If-Modified-Since", cached.SourceLastModified)
			}
		}
	}
	release, ok := acquireFeedSlotForRequest(r)
	if !ok {
		if r.Context().Err() != nil {
			fh.logger.LogComplete(http.StatusRequestTimeout)
			return
		}
		w.Header().Set("Retry-After", "5")
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		fh.logger.LogComplete(http.StatusServiceUnavailable)
		return
	}
	defer release()

	// 获取源RSS
	requestURL := feedURL
	requestUsername, requestPassword := LookupFeedCredentials(feedURL)
	if requestUsername != "" || requestPassword != "" {
		requestURL = addURLCredentials(feedURL, requestUsername, requestPassword)
	}
	proxyReq := NewProxyRequest(requestURL, string(ResourceFeed))
	resp, err := proxyReq.Do(upstreamRequest)
	if err != nil {
		if hasCached && cacheableFeedURL && cached.matchesRequestVary(upstreamVaryHeaders) && canServeStaleOnError(cached, time.Now()) && !rangedFeedRequest(r) {
			log.Printf("[WARN] 回源失败，使用 stale-if-error feed 缓存: url=%s", normalizeUpstreamURL(feedURL))
			cached.Stale = true
			w.Header().Set("Warning", `110 - "Response is stale"`)
			serveCachedFeed(w, r, clientIfNoneMatch, cached)
			fh.logger.LogComplete(statusForCachedFeed(r, clientIfNoneMatch, cached))
			return
		}
		log.Printf("[ERROR] 回源获取 feed 失败: url=%s err=%v", normalizeUpstreamURL(feedURL), err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}
	username, password := extractFeedBasicAuth(resp.Request.URL)
	if username != "" || password != "" {
		RegisterFeedCredentials(feedURL, username, password)
	}
	defer resp.Body.Close()
	if encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" && encoding != "gzip" {
		http.Error(w, "unsupported upstream content encoding", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// ⭐️ 如果源站返回304 Not Modified，直接转发给客户端（仅针对源站自带ETag的场景）
	if resp.StatusCode == http.StatusNotModified {
		if !hasCached {
			if len(clientIfNoneMatch) > 0 || r.Header.Get("If-Modified-Since") != "" {
				NewHeaderCopier(w, resp.Header).Copy()
				w.WriteHeader(http.StatusNotModified)
				fh.logger.LogComplete(http.StatusNotModified)
				return
			}
			http.Error(w, "upstream returned 304 without a local cache entry", http.StatusBadGateway)
			fh.logger.LogComplete(http.StatusBadGateway)
			return
		}
		cached = refreshCachedFeedHeaders(cached, resp.Header, upstreamVaryHeaders)
		if cached.NoStore || cached.Private || varyHeaderHasStar(cached.SourceVary) || (!cached.Cacheable && !cached.NoCache) {
			cache.Delete(cacheKey)
		} else {
			cache.Set(cacheKey, cached)
		}
		if clientConditionalForwarded {
			setFeedResponseHeaders(w, cached)
			w.WriteHeader(http.StatusNotModified)
			fh.logger.LogComplete(http.StatusNotModified)
			return
		}
		serveCachedFeed(w, r, clientIfNoneMatch, cached)
		fh.logger.LogComplete(statusForCachedFeed(r, clientIfNoneMatch, cached))
		return
	}

	if resp.StatusCode >= 400 {
		if hasCached && cacheableFeedURL && cached.matchesRequestVary(upstreamVaryHeaders) && resp.StatusCode >= http.StatusInternalServerError && canServeStaleOnError(cached, time.Now()) && !rangedFeedRequest(r) {
			log.Printf("[WARN] 源站返回 %d，使用 stale-if-error feed 缓存: url=%s", resp.StatusCode, normalizeUpstreamURL(feedURL))
			cached.Stale = true
			w.Header().Set("Warning", `110 - "Response is stale"`)
			serveCachedFeed(w, r, clientIfNoneMatch, cached)
			fh.logger.LogComplete(statusForCachedFeed(r, clientIfNoneMatch, cached))
			return
		}
		status := resp.StatusCode
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			status = http.StatusBadGateway
		}
		http.Error(w, http.StatusText(status), status)
		fh.logger.LogComplete(status)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "upstream returned an unexpected status", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 读取RSS内容
	maxBytes := GetConfig().FeedMaxBytes
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		log.Printf("[ERROR] 读取 feed 内容失败: %v", err)
		http.Error(w, "failed to read upstream feed", http.StatusInternalServerError)
		fh.logger.LogComplete(http.StatusInternalServerError)
		return
	}
	if int64(len(bodyBytes)) > maxBytes {
		http.Error(w, "upstream feed exceeds size limit", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 验证内容
	content := string(bodyBytes)
	content, err = normalizeFeedXML(content)
	if err != nil {
		http.Error(w, "unsupported or invalid feed encoding", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}
	if !fh.validator.IsFeedContent(content) {
		http.Error(w, "invalid feed content", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// Keep every feed response as RSS/Atom XML. Browsers that explicitly request
	// HTML get a separate server-rendered presentation below; no XSLT processing
	// instruction is added to the subscribed feed.
	rewriteResult, err := fh.transformer.TransformWithRoutes(content, builder, apikey, false, username, password)
	if err != nil {
		log.Printf("[ERROR] 转换 feed 失败: %v", err)
		http.Error(w, "failed to transform feed", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}
	transformed := rewriteResult.Content

	// 验证转换后的RSS是否为有效XML
	if err := fh.validator.ValidateTransformedFeed(transformed); err != nil {
		log.Printf("[ERROR] 转换后的RSS XML验证失败: %v", err)
		http.Error(w, "failed to transform feed safely", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}
	if display {
		page, err := renderPodcastPage(transformed, forceXMLFeedURL(builder.BuildFeedURL(apikey)))
		if err != nil {
			log.Printf("[ERROR] 渲染 feed 展示页失败: %v", err)
			http.Error(w, "failed to render feed presentation", http.StatusBadGateway)
			fh.logger.LogComplete(http.StatusBadGateway)
			return
		}
		transformed = string(page)
	}

	// Browser presentation is opt-in; PinePods/client subscriptions receive only XML.
	contentType := "application/rss+xml; charset=utf-8"
	if rootElementName(content) == "feed" {
		contentType = "application/atom+xml; charset=utf-8"
	}
	if display {
		contentType = "text/html; charset=utf-8"
	}
	feedETag := transformedETag(transformed)
	if !display && resp.Header.Get("ETag") != "" {
		feedETag = resp.Header.Get("ETag")
	}
	cacheControl := resp.Header.Get("Cache-Control")
	now := time.Now()
	ttl, lifetime, sourceAge, noStore, private, noCache := feedCacheTTLFromHeaders(resp.Header, now)
	sourceVary := resp.Header.Get("Vary")
	varyRequestHeaders, varySupported := feedCacheVariantHeaders(sourceVary, upstreamVaryHeaders)
	cacheable := !noStore && !private && ttl > 0
	if noStore || noCache || ttl <= 0 {
		cacheable = false
	}
	if private || !varySupported {
		cacheable = false
	}
	representationLastModified := resp.Header.Get("Last-Modified")
	entry := FeedCacheEntry{
		Body:               []byte(transformed),
		ETag:               feedETag,
		SourceETag:         resp.Header.Get("ETag"),
		SourceLastModified: resp.Header.Get("Last-Modified"),
		SourceCacheControl: cacheControl,
		SourceExpires:      resp.Header.Get("Expires"),
		SourceDate:         resp.Header.Get("Date"),
		SourceVary:         sourceVary,
		StaleIfError:       feedStaleIfError(cacheControl),
		Vary:               mergeVaryHeaders(sourceVary, "Accept", "Accept-Encoding"),
		VaryRequestHeaders: varyRequestHeaders,
		LastModified:       representationLastModified,
		CacheControl:       responseFeedCacheControl(cacheControl, noStore, private, noCache || ttl <= 0),
		ContentType:        contentType,
		StoredAt:           now,
		FreshnessLifetime:  lifetime,
		ExpiresAt:          now.Add(ttl),
		NoCache:            noCache || ttl <= 0,
		NoStore:            noStore,
		Private:            private,
		Cacheable:          cacheable,
		SourceAge:          sourceAge,
		SourceAgePresent:   resp.Header.Get("Age") != "" || resp.Header.Get("Date") != "",
	}
	if (cacheable || entry.NoCache) && cacheableFeedURL && !private && !noStore && varySupported && (cacheable || entry.SourceETag != "" || entry.SourceLastModified != "") {
		// Responses without freshness are retained only for origin validator-based
		// revalidation; cache hits are never served without a fresh origin check.
		if !cacheable && (entry.SourceETag == "" && entry.SourceLastModified == "") {
			// Without origin validators, retaining this body could only reuse stale
			// data, so serve it once without caching.
			cache.Delete(cacheKey)
		} else {
			cache.Set(cacheKey, entry)
		}
	}
	if private || noStore || !varySupported || !cacheable {
		entry.NoStore = noStore
		entry.Private = private
		entry.Vary = mergeVaryHeaders(sourceVary, "Accept", "Accept-Encoding")
		if noStore {
			entry.CacheControl = "no-store"
		}
	}
	if noCache {
		entry.Cacheable = false
	}
	if clientConditionalForwarded {
		setFeedResponseHeaders(w, entry)
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(entry.Body)
		}
		fh.logger.LogComplete(http.StatusOK)
		return
	}
	serveCachedFeed(w, r, clientIfNoneMatch, entry)
	fh.logger.LogComplete(statusForCachedFeed(r, clientIfNoneMatch, entry))
}

func shouldForwardConditionalToOrigin(r *http.Request, display bool, entry FeedCacheEntry) bool {
	if display {
		return false
	}
	if len(r.Header.Values("If-None-Match")) > 0 {
		return entry.SourceETag != ""
	}
	return r.Header.Get("If-Modified-Since") != "" && entry.SourceLastModified != ""
}

func ifNoneMatchMatches(values []string, current string) bool {
	currentTag, currentOK := normalizedEntityTag(current)
	if !currentOK {
		return false
	}
	for _, value := range values {
		for _, candidate := range splitEntityTagList(value) {
			if candidate == "*" {
				return true
			}
			candidateTag, ok := normalizedEntityTag(candidate)
			if ok && candidateTag == currentTag {
				return true
			}
		}
	}
	return false
}

func splitEntityTagList(value string) []string {
	var tags []string
	start := 0
	inQuotes := false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			inQuotes = !inQuotes
		case ',':
			if !inQuotes {
				tags = append(tags, strings.TrimSpace(value[start:i]))
				start = i + 1
			}
		}
	}
	if start <= len(value) {
		tags = append(tags, strings.TrimSpace(value[start:]))
	}
	return tags
}

func normalizedEntityTag(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimSpace(value[2:])
	}
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", false
	}
	for i := 1; i < len(value)-1; i++ {
		if value[i] < 0x21 || value[i] == '"' || value[i] == 0x7f {
			return "", false
		}
	}
	return value, true
}

// responseFeedCacheControl preserves the source policy. If parsing identified
// an invalid max-age, fail closed by requiring revalidation rather than
// inventing freshness or silently dropping the origin's policy.
func responseFeedCacheControl(source string, noStore, private, noCache bool) string {
	source = strings.TrimSpace(source)
	if source != "" {
		if cacheControlHasInvalidMaxAge(source) {
			if private || feedCacheDirective(source, "private") {
				return "private, no-cache"
			}
			return "no-cache"
		}
		return source
	}
	if noStore {
		return "no-store"
	}
	if private {
		return "private, no-cache"
	}
	if noCache {
		return "no-cache"
	}
	return ""
}

func refreshCachedFeedHeaders(entry FeedCacheEntry, headers http.Header, requestVary http.Header) FeedCacheEntry {
	previousSourceETag := entry.SourceETag
	if cacheControl := headers.Get("Cache-Control"); cacheControl != "" {
		entry.SourceCacheControl = cacheControl
	}
	if expires := headers.Get("Expires"); expires != "" {
		entry.SourceExpires = expires
	}
	if date := headers.Get("Date"); date != "" {
		entry.SourceDate = date
	}
	if vary := headers.Get("Vary"); vary != "" {
		entry.SourceVary = vary
	}
	if cacheControl := headers.Get("Cache-Control"); cacheControl != "" {
		entry.StaleIfError = feedStaleIfError(cacheControl)
	}
	metadata := make(http.Header)
	if entry.SourceCacheControl != "" {
		metadata.Set("Cache-Control", entry.SourceCacheControl)
	}
	if entry.SourceExpires != "" {
		metadata.Set("Expires", entry.SourceExpires)
	}
	if entry.SourceDate != "" {
		metadata.Set("Date", entry.SourceDate)
	}
	if headers.Get("Cache-Control") == "" && headers.Get("Expires") == "" {
		metadata.Set("Cache-Control", entry.SourceCacheControl)
	}
	metadataAge := headers.Get("Age")
	if metadataAge == "" && entry.SourceAgePresent {
		metadataAge = strconv.FormatInt(int64(currentFeedAge(entry, time.Now())/time.Second), 10)
	}
	metadata.Set("Age", metadataAge)
	if headers.Get("Date") == "" && !entry.StoredAt.IsZero() {
		metadata.Set("Date", entry.StoredAt.UTC().Format(http.TimeFormat))
	}
	now := time.Now()
	ttl, lifetime, sourceAge, noStore, private, noCache := feedCacheTTLFromHeaders(metadata, now)
	cacheable := !noStore && !noCache && ttl > 0
	varyRequestHeaders, varySupported := feedCacheVariantHeaders(entry.SourceVary, requestVary)
	if !varySupported {
		cacheable = false
	}
	if etag := headers.Get("ETag"); etag != "" {
		entry.SourceETag = etag
		if entry.ETag == previousSourceETag {
			entry.ETag = etag
		}
	}
	if modified := headers.Get("Last-Modified"); modified != "" {
		entry.SourceLastModified = modified
	}
	entry.LastModified = entry.SourceLastModified
	entry.CacheControl = responseFeedCacheControl(entry.SourceCacheControl, noStore, private, noCache || ttl <= 0)
	entry.StoredAt = now
	entry.FreshnessLifetime = lifetime
	entry.SourceAge = sourceAge
	// "Age: 0" is present; track field presence independently of its value.
	entry.SourceAgePresent = metadataAge != ""
	entry.ExpiresAt = entry.StoredAt.Add(ttl)
	entry.NoStore = noStore
	entry.Private = private
	entry.NoCache = noCache || ttl <= 0
	entry.Cacheable = cacheable
	entry.Vary = mergeVaryHeaders(entry.SourceVary, "Accept", "Accept-Encoding")
	entry.VaryRequestHeaders = varyRequestHeaders
	entry.Stale = false
	return entry
}

func setFeedResponseHeaders(w http.ResponseWriter, entry FeedCacheEntry) {
	w.Header().Set("Content-Type", entry.ContentType)
	if entry.Stale {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Warning", `110 - "Response is stale"`)
		w.Header().Add("Warning", `111 - "Revalidation failed"`)
	} else if entry.NoStore {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("Warning")
	} else if entry.Private {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Del("Warning")
	} else if entry.NoCache || !entry.Cacheable {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		w.Header().Del("Warning")
	} else {
		w.Header().Set("Cache-Control", entry.CacheControl)
		w.Header().Del("Warning")
	}
	w.Header().Set("ETag", entry.ETag)
	w.Header().Set("Vary", entry.Vary)
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	if entry.SourceExpires != "" {
		w.Header().Set("Expires", entry.SourceExpires)
	} else {
		w.Header().Del("Expires")
	}
	if entry.SourceAgePresent || entry.SourceAge > 0 || !entry.StoredAt.IsZero() {
		age := currentFeedAge(entry, time.Now())
		w.Header().Set("Age", strconv.FormatInt(int64(age/time.Second), 10))
	}
	if entry.LastModified != "" {
		w.Header().Set("Last-Modified", entry.LastModified)
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(entry.Body)))
}

func serveCachedFeed(w http.ResponseWriter, r *http.Request, clientETags []string, entry FeedCacheEntry) {
	setFeedResponseHeaders(w, entry)
	writeFeedCacheStatus(w, r, clientETags, entry)
}

func statusForCachedFeed(r *http.Request, clientETags []string, entry FeedCacheEntry) int {
	status := http.StatusOK
	if r.Header.Get("Range") == "" {
		if len(clientETags) > 0 && ifNoneMatchMatches(clientETags, entry.ETag) {
			status = http.StatusNotModified
		} else if len(clientETags) == 0 && ifModifiedSinceMatches(r.Header.Get("If-Modified-Since"), entry.LastModified) {
			status = http.StatusNotModified
		}
	}
	return status
}

func rangedFeedRequest(r *http.Request) bool {
	return r.Header.Get("Range") != "" || r.Header.Get("If-Range") != ""
}

func canServeStaleOnError(entry FeedCacheEntry, now time.Time) bool {
	if entry.StaleIfError <= 0 || entry.NoStore || entry.Private || varyHeaderHasStar(entry.SourceVary) {
		return false
	}
	return now.Before(entry.ExpiresAt.Add(entry.StaleIfError))
}

func writeFeedCacheStatus(w http.ResponseWriter, r *http.Request, clientETags []string, entry FeedCacheEntry) int {
	status := http.StatusOK
	// Feed XML is not served as a partial representation; ignore Range and
	// return the complete cached document without evaluating conditional tags.
	if r.Header.Get("Range") == "" && len(clientETags) > 0 {
		if ifNoneMatchMatches(clientETags, entry.ETag) {
			status = http.StatusNotModified
		}
	} else if r.Header.Get("Range") == "" && ifModifiedSinceMatches(r.Header.Get("If-Modified-Since"), entry.LastModified) {
		status = http.StatusNotModified
	}
	if status == http.StatusNotModified {
		w.Header().Del("Content-Length")
	}
	w.WriteHeader(status)
	if status == http.StatusOK && r.Method != http.MethodHead {
		_, _ = w.Write(entry.Body)
	}
	return status
}

func ifModifiedSinceMatches(value, lastModified string) bool {
	if value == "" || lastModified == "" {
		return false
	}
	since, err := http.ParseTime(value)
	if err != nil {
		return false
	}
	modified, err := http.ParseTime(lastModified)
	return err == nil && !modified.Truncate(time.Second).After(since)
}

func normalizeUpstreamURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	parsed.User = nil
	parsed.RawQuery = "<redacted>"
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func transformedETag(body string) string {
	digest := sha256.Sum256([]byte(body))
	return fmt.Sprintf(`W/"pp-sha256-%x"`, digest[:])
}

func rootElementName(body string) string {
	decoder := xml.NewDecoder(strings.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		if root, ok := token.(xml.StartElement); ok {
			return root.Name.Local
		}
	}
}

func validResourceURL(raw string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func hasURLCredentials(raw string) bool {
	parsed, err := url.Parse(raw)
	return err != nil || parsed.User != nil
}

func extractFeedBasicAuth(feedURL *url.URL) (string, string) {
	if feedURL == nil || feedURL.User == nil {
		return "", ""
	}
	username := feedURL.User.Username()
	password, _ := feedURL.User.Password()
	if feedURL.User.String() != "" {
		log.Printf("[WARN] Feed URL contains embedded userinfo; avoid sharing this URL. Proxy media access will forward credentials only in the Authorization header.")
	}
	return username, password
}

func addURLCredentials(rawURL, username, password string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String()
}

// AudioHandler 音频处理器
type AudioHandler struct {
	*HandlerBase
	sh *StringHelper
}

// NewAudioHandler 创建音频处理器
func NewAudioHandler(r *http.Request) *AudioHandler {
	return &AudioHandler{
		HandlerBase: newHandlerBase(r),
		sh:          &StringHelper{},
	}
}

// Handle 处理音频请求
// 音频由服务端跟完所有 302/307 后直接透流最终 200/206，客户端只看到最终音频。
func (ah *AudioHandler) Handle(w http.ResponseWriter, r *http.Request) {
	ah.logger.LogStart()

	// 验证API Key (若非法直接静默断开，防止被扫描)
	_, valid := ah.auth.VerifyRequest(r, "/audio/")
	if !valid {
		RejectRequest(w)
		ah.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	token := mediaTokenFromRequest(r.URL, "/audio/")
	origURL := ResolveResourceRoute(ResourceAudio, token, r.URL.Query().Get("url"))
	if origURL == "" || !validResourceURL(origURL) {
		http.Error(w, "url parameter required", http.StatusBadRequest)
		ah.logger.LogComplete(http.StatusBadRequest)
		return
	}
	origURL = ah.sh.DecodeAmpersand(origURL)
	if !validResourceURL(origURL) || hasURLCredentials(origURL) {
		http.Error(w, "invalid resource URL", http.StatusBadRequest)
		ah.logger.LogComplete(http.StatusBadRequest)
		return
	}

	// ⭐️ 创新性能优化：直连模式 (Bypass Proxy via Direct Redirect)
	// ⭐️ 默认必须进行音频中转代理，这是核心机制。
	// 仅当管理员显式在环境变量中配置了 MEDIA_DIRECT_REDIRECT=true（开启旁路放行），
	// 或者在特殊调试场景下 URL 显式指定了 &direct=1 时，才允许直接重定向以降低 VPS 带宽开销。
	// 否则，所有音频一律无条件通过本代理服务器进行 512KB 高带宽时延积缓存中转。
	if GetConfig().MediaDirectRedirect || r.URL.Query().Get("direct") == "1" {
		http.Redirect(w, r, origURL, http.StatusTemporaryRedirect)
		ah.logger.LogComplete(http.StatusTemporaryRedirect)
		return
	}

	// 执行代理请求（音频客户端已跟随全部跳转，resp 即最终音频响应）
	release, ok := acquireMediaSlotForRequest(r)
	if !ok {
		if r.Context().Err() != nil {
			ah.logger.LogComplete(http.StatusRequestTimeout)
			return
		}
		w.Header().Set("Retry-After", "5")
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		ah.logger.LogComplete(http.StatusServiceUnavailable)
		return
	}
	defer release()

	proxyReq := NewProxyRequest(origURL, string(ResourceAudio))
	resp, err := proxyReq.Do(r)
	if err != nil {
		log.Printf("[ERROR] 回源获取音频失败: url=%s err=%v", origURL, err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		ah.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 直接透流最终音频：不再调用 HandleRedirect 抛二次 302 给客户端。
	// 客户端全程只看到一次 200/206，无需感知中间跳了几次 302/307。
	builder := NewProxyURLBuilder(r)
	proxyResp := NewProxyResponse(resp, string(ResourceAudio))

	// 复制响应
	if err := proxyResp.WriteResponse(w, r, builder); err != nil {
		ah.logger.LogComplete(http.StatusInternalServerError)
		return
	}

	ah.logger.LogComplete(resp.StatusCode)
}

// ImageHandler 图片处理器
type ImageHandler struct {
	*HandlerBase
	sh *StringHelper
}

// NewImageHandler 创建图片处理器
func NewImageHandler(r *http.Request) *ImageHandler {
	return &ImageHandler{
		HandlerBase: newHandlerBase(r),
		sh:          &StringHelper{},
	}
}

// Handle 处理图片请求
func (ih *ImageHandler) Handle(w http.ResponseWriter, r *http.Request) {
	ih.logger.LogStart()

	// 验证API Key (若非法直接静默断开，防止被扫描)
	_, valid := ih.auth.VerifyRequest(r, "/image/")
	if !valid {
		RejectRequest(w)
		ih.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	token := mediaTokenFromRequest(r.URL, "/image/")
	origURL := ResolveResourceRoute(ResourceImage, token, r.URL.Query().Get("url"))
	if origURL == "" || !validResourceURL(origURL) {
		http.Error(w, "url parameter required", http.StatusBadRequest)
		ih.logger.LogComplete(http.StatusBadRequest)
		return
	}
	origURL = ih.sh.DecodeAmpersand(origURL)
	if !validResourceURL(origURL) || hasURLCredentials(origURL) {
		http.Error(w, "invalid resource URL", http.StatusBadRequest)
		ih.logger.LogComplete(http.StatusBadRequest)
		return
	}

	// ⭐️ 创新性能优化：图片直连模式
	if GetConfig().MediaDirectRedirect || r.URL.Query().Get("direct") == "1" {
		http.Redirect(w, r, origURL, http.StatusTemporaryRedirect)
		ih.logger.LogComplete(http.StatusTemporaryRedirect)
		return
	}

	// 执行代理请求（服务端已跟随全部跳转，resp 即最终图片响应，直接透流）
	release, ok := acquireMediaSlotForRequest(r)
	if !ok {
		if r.Context().Err() != nil {
			ih.logger.LogComplete(http.StatusRequestTimeout)
			return
		}
		w.Header().Set("Retry-After", "5")
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		ih.logger.LogComplete(http.StatusServiceUnavailable)
		return
	}
	defer release()

	proxyReq := NewProxyRequest(origURL, string(ResourceImage))
	resp, err := proxyReq.Do(r)
	if err != nil {
		log.Printf("[ERROR] 回源获取图片失败: url=%s err=%v", origURL, err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		ih.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 直接透流最终图片：不再抛二次 302 给客户端，避免多跳导致加载失败。
	builder := NewProxyURLBuilder(r)
	proxyResp := NewProxyResponse(resp, string(ResourceImage))

	// 复制响应
	if err := proxyResp.WriteResponse(w, r, builder); err != nil {
		ih.logger.LogComplete(http.StatusInternalServerError)
		return
	}

	ih.logger.LogComplete(resp.StatusCode)
}

// StyleHandler 样式处理器
type StyleHandler struct {
	*HandlerBase
	sh *StringHelper
}

// NewStyleHandler 创建样式处理器
func NewStyleHandler(r *http.Request) *StyleHandler {
	return &StyleHandler{
		HandlerBase: newHandlerBase(r),
		sh:          &StringHelper{},
	}
}

// Handle 处理样式请求
func (sh *StyleHandler) Handle(w http.ResponseWriter, r *http.Request) {
	sh.logger.LogStart()

	// 验证API Key (若非法直接静默断开，防止被扫描)
	_, valid := sh.auth.VerifyRequest(r, "/style/")
	if !valid {
		RejectRequest(w)
		sh.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	token := mediaTokenFromRequest(r.URL, "/style/")
	origURL := ResolveResourceRoute(ResourceStyle, token, r.URL.Query().Get("url"))
	if origURL == "" || !validResourceURL(origURL) {
		http.Error(w, "url parameter required", http.StatusBadRequest)
		sh.logger.LogComplete(http.StatusBadRequest)
		return
	}
	origURL = sh.sh.DecodeAmpersand(origURL)
	if !validResourceURL(origURL) || hasURLCredentials(origURL) {
		http.Error(w, "invalid resource URL", http.StatusBadRequest)
		sh.logger.LogComplete(http.StatusBadRequest)
		return
	}

	// 执行代理请求（服务端已跟随全部跳转，resp 即最终样式响应，直接透流）
	proxyReq := NewProxyRequest(origURL, string(ResourceStyle))
	resp, err := proxyReq.Do(r)
	if err != nil {
		log.Printf("[ERROR] 回源获取样式失败: url=%s err=%v", origURL, err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		sh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 直接透流最终样式：不再抛二次 302 给客户端。
	builder := NewProxyURLBuilder(r)
	proxyResp := NewProxyResponse(resp, string(ResourceStyle))

	// 复制响应
	if err := proxyResp.WriteResponse(w, r, builder); err != nil {
		sh.logger.LogComplete(http.StatusInternalServerError)
		return
	}

	sh.logger.LogComplete(resp.StatusCode)
}

// NotFoundHandler 404处理器
type NotFoundHandler struct {
	*HandlerBase
}

// NewNotFoundHandler 创建404处理器
func NewNotFoundHandler(r *http.Request) *NotFoundHandler {
	return &NotFoundHandler{
		HandlerBase: newHandlerBase(r),
	}
}

// Handle 处理404请求
func (nfh *NotFoundHandler) Handle(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

// PodcastXSLHandler 专门处理我们自己播客美化样式的处理器
type PodcastXSLHandler struct {
	*HandlerBase
}

// NewPodcastXSLHandler 创建播客美化样式处理器
func NewPodcastXSLHandler(r *http.Request) *PodcastXSLHandler {
	return &PodcastXSLHandler{
		HandlerBase: newHandlerBase(r),
	}
}

// Handle 处理播客美化样式请求
func (pxh *PodcastXSLHandler) Handle(w http.ResponseWriter, r *http.Request) {
	pxh.logger.LogStart()

	// 验证API Key (若非法直接静默断开，防止被扫描)
	apikey, valid := pxh.auth.VerifyRequest(r, "/podcast.xsl/")
	if !valid || apikey == "" {
		RejectRequest(w)
		pxh.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/xsl; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=604800") // 缓存1周
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(GetXSLTemplate()))
	pxh.logger.LogComplete(http.StatusOK)
}

// 全局缓存管理器
var _cacheManager *CacheManager

func getCacheManager() *CacheManager {
	if _cacheManager == nil {
		_cacheManager = NewCacheManager(24 * time.Hour)
	}
	return _cacheManager
}
