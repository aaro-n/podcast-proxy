package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPClientManager HTTP客户端管理器
type HTTPClientManager struct {
	client           *http.Client
	noRedirectClient *http.Client
	audioClient      *http.Client
	mu               sync.Mutex
}

// init 初始化HTTP客户端
func (hm *HTTPClientManager) init(timeout time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.initLocked(timeout)
}

func (hm *HTTPClientManager) initLocked(timeout time.Duration) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		// 出站守卫：DNS 解析后的 IP 校验 + 端口白名单，每跳重定向都会经过
		DialContext: egressDialContext,
		// 支持Range请求
		DisableCompression: true,
	}

	if hm.client == nil {
		hm.client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 限制重定向次数，防止无限循环
				if len(via) > 10 {
					return http.ErrUseLastResponse
				}
				if !validResourceURL(req.URL.String()) {
					return http.ErrUseLastResponse
				}
				if req.URL.User != nil {
					return http.ErrUseLastResponse
				}
				return nil
			},
			Transport: transport,
		}
	}

	if hm.noRedirectClient == nil {
		hm.noRedirectClient = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 遇到重定向时立即停止跟随，并直接返回最后的3xx重定向响应
				return http.ErrUseLastResponse
			},
			Transport: transport,
		}
	}

	if hm.audioClient == nil {
		// 音频专用：服务端跟随全部重定向后直接透流，客户端只看到一次 200/206。
		// 注意 http.Client.Timeout 包含整个 Body 读取，30s 会在长音频中途掐断，
		// 因此音频链路不用整体 Timeout，改用 ResponseHeaderTimeout 控制首字节。
		mediaTransport := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			// 出站守卫：DNS 解析后的 IP 校验 + 端口白名单，每跳重定向都会经过
			DialContext: egressDialContext,
			// 音频禁止 gzip：否则源站返回 gzip 而我们剥掉 Content-Encoding 会损坏字节流
			DisableCompression:    true,
			ResponseHeaderTimeout: timeout,
		}
		hm.audioClient = &http.Client{
			Timeout: 0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 10 {
					return http.ErrUseLastResponse
				}
				if !validResourceURL(req.URL.String()) {
					return http.ErrUseLastResponse
				}
				if req.URL.User != nil {
					return http.ErrUseLastResponse
				}
				return nil
			},
			Transport: mediaTransport,
		}
	}
}

// GetClient 获取自动随重定向的HTTP客户端
func (hm *HTTPClientManager) GetClient() *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.client == nil {
		hm.initLocked(time.Duration(GetConfig().Timeout) * time.Second)
	}

	return hm.client
}

// GetNoRedirectClient 获取不随重定向的HTTP客户端
func (hm *HTTPClientManager) GetNoRedirectClient() *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.noRedirectClient == nil {
		hm.initLocked(time.Duration(GetConfig().Timeout) * time.Second)
	}

	return hm.noRedirectClient
}

// GetAudioClient 获取音频专用客户端：跟随重定向、关闭整体超时、禁用压缩
func (hm *HTTPClientManager) GetAudioClient() *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.audioClient == nil {
		hm.initLocked(time.Duration(GetConfig().Timeout) * time.Second)
	}

	return hm.audioClient
}

// ProxyRequest 代理请求助手
type ProxyRequest struct {
	originalURL  string
	resourceType string
	client       *http.Client
}

// NewProxyRequest 创建代理请求
// 音频/图片/样式统一由服务端跟随全部 302/307 跳转后直接透流最终 200/206，
// 客户端全程只看到一次响应，无需感知中间跳了几次。之前把 302 抛回客户端的
// 方案会导致 ExoPlayer/AVPlayer 在多跳 302 下丢 Range（音频拿不到 Content-Range
// 即报 duration=0），图片在部分 WebView/客户端里也会因跨域二次跳转加载失败。
func NewProxyRequest(originalURL string, resourceType string) *ProxyRequest {
	var client *http.Client
	if resourceType == string(ResourceFeed) {
		client = GetHTTPClient().GetClient()
	} else {
		client = GetHTTPClient().GetAudioClient()
	}

	return &ProxyRequest{
		originalURL:  originalURL,
		resourceType: resourceType,
		client:       client,
	}
}

// Do 执行代理请求
// 媒体链路（音频/图片/样式）由服务端跟随全部 302/307 后直接透流，客户端只看到最终 200/206。
// Go 的 Client 在跟随跳转时会自动复制原始请求头（Range 等得以保留），无需人工等待 30ms。
func (pr *ProxyRequest) Do(sourceReq *http.Request) (*http.Response, error) {
	method := sourceReq.Method
	if method != http.MethodGet && method != http.MethodHead {
		return nil, fmt.Errorf("method %s is not supported", method)
	}
	if !validResourceURL(pr.originalURL) {
		return nil, fmt.Errorf("destination URL must use HTTP or HTTPS")
	}
	parsedURL, parseErr := url.Parse(pr.originalURL)
	if parseErr != nil || parsedURL.User != nil {
		return nil, fmt.Errorf("destination URL must not contain embedded credentials")
	}
	// 绑定客户端请求的 Context：客户端断开（拖动进度条、关闭播放器、跳过一集）时
	// 立即取消上游请求，避免继续下载已经没人要的音频而白白消耗带宽。
	req, err := http.NewRequestWithContext(sourceReq.Context(), method, pr.originalURL, nil)
	if err != nil {
		return nil, err
	}

	// 复制关键请求头
	if sourceReq.Header.Get("User-Agent") != "" {
		req.Header.Set("User-Agent", sourceReq.Header.Get("User-Agent"))
	} else {
		req.Header.Set("User-Agent", "PinePods/1.0 PodcastProxy/2.1")
	}
	req.Header.Set("Accept", "*/*")
	if acceptLanguage := sourceReq.Header.Get("Accept-Language"); acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	if pr.resourceType == string(ResourceFeed) {
		req.Header.Set("Accept-Encoding", "gzip, identity;q=0.5")
	}

	// 转发Range请求头（用于音频快速跳转 / ExoPlayer、AVPlayer 探测时长）
	if rangeHeader := sourceReq.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
		req.Header.Set("Accept-Encoding", "identity")
	}

	// 转发 If-Range（带 ETag/时间的 Range 续传校验，缺了会导致源站回 200 而非 206）
	if ifRange := sourceReq.Header.Get("If-Range"); ifRange != "" {
		req.Header.Set("If-Range", ifRange)
	}

	// Entity validators are forwarded from this request. FeedHandler removes the
	// client's transformed-feed validators and adds only validators bound to its
	// cached origin representation; media validators are the origin's own
	// pass-through ETag/Last-Modified values.
	if ifNoneMatch := sourceReq.Header.Get("If-None-Match"); ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	if ifModifiedSince := sourceReq.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
		req.Header.Set("If-Modified-Since", ifModifiedSince)
	}
	resp, err := pr.client.Do(req)
	if err != nil {
		return nil, err
	}
	if pr.resourceType != string(ResourceFeed) {
		return resp, nil
	}
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return resp, nil
	}
	if strings.Contains(encoding, ",") || encoding != "gzip" {
		resp.Body.Close()
		return nil, fmt.Errorf("unsupported feed content encoding %q", encoding)
	}
	reader, err := gzip.NewReader(resp.Body)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("decode upstream gzip feed: %w", err)
	}
	resp.Body = &gzipResponseBody{Reader: reader, source: resp.Body}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	return resp, nil
}

type gzipResponseBody struct {
	*gzip.Reader
	source io.Closer
}

func (body *gzipResponseBody) Close() error {
	readerErr := body.Reader.Close()
	sourceErr := body.source.Close()
	if readerErr != nil {
		return readerErr
	}
	return sourceErr
}

// ProxyResponse 代理响应助手
type ProxyResponse struct {
	sourceResp   *http.Response
	resourceType string
}

// NewProxyResponse 创建代理响应
func NewProxyResponse(sourceResp *http.Response, resourceType string) *ProxyResponse {
	return &ProxyResponse{
		sourceResp:   sourceResp,
		resourceType: resourceType,
	}
}

// WriteResponse 写入响应
// 媒体场景下 sourceResp 已经是跟完所有跳转后的最终响应，这里直接透传最终状态码
// （200/206/304/404），客户端全程只看到一次响应，无需感知中间跳了几次 302/307。
func (pr *ProxyResponse) WriteResponse(w http.ResponseWriter, srcReq *http.Request,
	builder *ProxyURLBuilder) error {
	defer pr.sourceResp.Body.Close()
	if srcReq.Method != http.MethodGet && srcReq.Method != http.MethodHead {
		return fmt.Errorf("method %s is not supported", srcReq.Method)
	}

	// 复制响应头（排除不适合的头）
	// 注意：不排除 Content-Length，因为 206 Partial Content 响应中
	// Content-Length + Content-Range 是客户端正确处理 Range 请求的必要头
	hc := NewHeaderCopier(w, pr.sourceResp.Header)
	hc.CopyExcept([]string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
	})

	// 兜底 Content-Type/Accept-Ranges：部分源站省略这些头会导致客户端解析失败
	//（音频拿不到时长/不可 seek，图片无法渲染）。补上后双端可正确识别。
	if pr.resourceType == string(ResourceAudio) {
		if w.Header().Get("Accept-Ranges") == "" &&
			(pr.sourceResp.StatusCode == http.StatusPartialContent || strings.Contains(strings.ToLower(pr.sourceResp.Header.Get("Accept-Ranges")), "bytes")) {
			w.Header().Set("Accept-Ranges", "bytes")
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
	} else if pr.resourceType == string(ResourceImage) {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "image/jpeg")
		}
	} else if pr.resourceType == string(ResourceStyle) {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
	}
	if pr.sourceResp.StatusCode >= 300 && pr.sourceResp.StatusCode < 400 {
		// The client follows at most ten redirects. Do not leak an upstream URL
		// (which may contain credentials) or let it bypass this proxy.
		w.Header().Del("Location")
		http.Error(w, "upstream redirect limit exceeded", http.StatusBadGateway)
		return nil
	}
	if pr.sourceResp.StatusCode == http.StatusPartialContent && w.Header().Get("Content-Range") == "" {
		http.Error(w, "upstream returned an incomplete range response", http.StatusBadGateway)
		return nil
	}

	// 设置状态码（最终状态：200/206 直接透流，不再抛 302 给客户端）
	w.WriteHeader(pr.sourceResp.StatusCode)

	// HEAD / 304 / 204 无 Body，直接返回，避免阻塞等待 Body
	if srcReq.Method == "HEAD" || pr.sourceResp.StatusCode == http.StatusNotModified || pr.sourceResp.StatusCode == http.StatusNoContent {
		return nil
	}

	_, err := io.Copy(w, pr.sourceResp.Body)
	return err
}

// HandleRedirect 处理重定向
func (pr *ProxyResponse) HandleRedirect(w http.ResponseWriter, srcReq *http.Request,
	apikey string, builder *ProxyURLBuilder) (bool, error) {

	if pr.sourceResp.StatusCode >= 300 && pr.sourceResp.StatusCode < 400 {
		location := pr.sourceResp.Header.Get("Location")
		if location != "" {
			var proxyURL string
			switch pr.resourceType {
			case string(ResourceAudio):
				proxyURL = builder.BuildAudioURL(apikey, location)
			case string(ResourceImage):
				proxyURL = builder.BuildImageURL(apikey, location)
			case string(ResourceStyle):
				proxyURL = builder.BuildStyleURL(apikey, location)
			}

			http.Redirect(w, srcReq, proxyURL, pr.sourceResp.StatusCode)
			return true, nil
		}
	}

	return false, nil
}
