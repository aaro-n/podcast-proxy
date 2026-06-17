package main

import (
	"io"
	"log"
	"net/http"
	"time"
)

// HandlerBase 处理器基类
type HandlerBase struct {
	auth       *AuthManager
	logger     *LoggerHelper
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

	// 验证API Key
	apikey, valid := fh.auth.VerifyRequest(r, "")
	if !valid || apikey == "" {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		fh.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	feedURL := r.URL.Query().Get("url")
	if !fh.validator.ValidateFeedURL(feedURL) {
		http.Error(w, "invalid feed url", http.StatusBadRequest)
		fh.logger.LogComplete(http.StatusBadRequest)
		return
	}

	// 获取源RSS
	proxyReq := NewProxyRequest(feedURL, string(ResourceFeed))
	resp, err := proxyReq.Do(r)
	if err != nil {
		http.Error(w, "failed to fetch feed: "+err.Error(), http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// ⭐️ 如果源站返回304 Not Modified，直接转发给客户端
	if resp.StatusCode == http.StatusNotModified {
		// 复制源站的响应头
		hc := NewHeaderCopier(w, resp.Header)
		hc.Copy()
		w.WriteHeader(http.StatusNotModified)
		fh.logger.LogComplete(http.StatusNotModified)
		return
	}

	if resp.StatusCode >= 400 {
		hc := NewHeaderCopier(w, resp.Header)
		hc.Copy()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		fh.logger.LogComplete(resp.StatusCode)
		return
	}

	// 读取RSS内容
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read feed: "+err.Error(), http.StatusInternalServerError)
		fh.logger.LogComplete(http.StatusInternalServerError)
		return
	}

	// 验证内容
	content := string(bodyBytes)
	if !fh.validator.IsFeedContent(content) {
		http.Error(w, "invalid feed content", http.StatusBadGateway)
		fh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 转换RSS源
	builder := NewProxyURLBuilder(r)
	transformed := fh.transformer.Transform(content, builder, apikey)

	// 验证转换后的RSS是否为有效XML
	if err := fh.validator.ValidateTransformedFeed(transformed); err != nil {
		// 验证失败时记录警告，但仍返回内容（保证可用性优先）
		log.Printf("[WARN] 转换后的RSS XML验证失败: %v", err)
	}

	// 设置响应头
	contentType := "application/rss+xml; charset=utf-8"
	if r.URL.Query().Get("display") == "1" {
		contentType = "text/xml; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)

	// ⭐️ 直接使用源站的ETag - 由源站决定缓存有效性
	// 这样做的好处:
	// 1. 源站控制缓存策略
	// 2. 客户端下次请求时带上 If-None-Match: sourceETag
	// 3. 我们转发给源站，源站返回 304 → 直接转发 304 (上面已处理)
	// 4. 源站返回 200 → 我们重新生成转换后的 RSS
	if sourceETag := resp.Header.Get("ETag"); sourceETag != "" {
		w.Header().Set("ETag", sourceETag)
	}

	w.Header().Set("Cache-Control", "public, max-age=86400") // 允许缓存1天（由ETag决定更新）
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(transformed))

	fh.logger.LogComplete(http.StatusOK)
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
func (ah *AudioHandler) Handle(w http.ResponseWriter, r *http.Request) {
	ah.logger.LogStart()

	// 验证API Key
	apikey, valid := ah.auth.VerifyRequest(r, "/audio/")
	if !valid || apikey == "" {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		ah.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url parameter required", http.StatusBadRequest)
		ah.logger.LogComplete(http.StatusBadRequest)
		return
	}
	origURL = ah.sh.DecodeAmpersand(origURL)

	// 执行代理请求
	proxyReq := NewProxyRequest(origURL, string(ResourceAudio))
	resp, err := proxyReq.Do(r)
	if err != nil {
		http.Error(w, "failed to fetch audio: "+err.Error(), http.StatusBadGateway)
		ah.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 处理重定向
	builder := NewProxyURLBuilder(r)
	proxyResp := NewProxyResponse(resp, string(ResourceAudio))
	if redirected, _ := proxyResp.HandleRedirect(w, r, apikey, builder); redirected {
		ah.logger.LogComplete(http.StatusFound)
		return
	}

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

	// 验证API Key
	apikey, valid := ih.auth.VerifyRequest(r, "/image/")
	if !valid || apikey == "" {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		ih.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url parameter required", http.StatusBadRequest)
		ih.logger.LogComplete(http.StatusBadRequest)
		return
	}
	origURL = ih.sh.DecodeAmpersand(origURL)

	// 执行代理请求
	proxyReq := NewProxyRequest(origURL, string(ResourceImage))
	resp, err := proxyReq.Do(r)
	if err != nil {
		http.Error(w, "failed to fetch image: "+err.Error(), http.StatusBadGateway)
		ih.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 处理重定向
	builder := NewProxyURLBuilder(r)
	proxyResp := NewProxyResponse(resp, string(ResourceImage))
	if redirected, _ := proxyResp.HandleRedirect(w, r, apikey, builder); redirected {
		ih.logger.LogComplete(http.StatusFound)
		return
	}

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
		sh: &StringHelper{},
	}
}

// Handle 处理样式请求
func (sh *StyleHandler) Handle(w http.ResponseWriter, r *http.Request) {
	sh.logger.LogStart()

	// 验证API Key
	apikey, valid := sh.auth.VerifyRequest(r, "/style/")
	if !valid || apikey == "" {
		http.Error(w, "unauthorized: invalid apikey", http.StatusUnauthorized)
		sh.logger.LogComplete(http.StatusUnauthorized)
		return
	}

	// 获取源URL
	origURL := r.URL.Query().Get("url")
	if origURL == "" {
		http.Error(w, "url parameter required", http.StatusBadRequest)
		sh.logger.LogComplete(http.StatusBadRequest)
		return
	}
	origURL = sh.sh.DecodeAmpersand(origURL)

	// 执行代理请求
	proxyReq := NewProxyRequest(origURL, string(ResourceStyle))
	resp, err := proxyReq.Do(r)
	if err != nil {
		http.Error(w, "failed to fetch style: "+err.Error(), http.StatusBadGateway)
		sh.logger.LogComplete(http.StatusBadGateway)
		return
	}

	// 处理重定向
	builder := NewProxyURLBuilder(r)
	proxyResp := NewProxyResponse(resp, string(ResourceStyle))
	if redirected, _ := proxyResp.HandleRedirect(w, r, apikey, builder); redirected {
		sh.logger.LogComplete(http.StatusFound)
		return
	}

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
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
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
