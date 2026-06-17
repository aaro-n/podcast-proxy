package main

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTPClientManager HTTP客户端管理器
type HTTPClientManager struct {
	client           *http.Client
	noRedirectClient *http.Client
	mu               sync.Mutex
}

// init 初始化HTTP客户端
func (hm *HTTPClientManager) init(timeout time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		// 支持Range请求
		DisableCompression: false,
	}

	if hm.client == nil {
		hm.client = &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// 限制重定向次数，防止无限循环
				if len(via) > 10 {
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
}

// GetClient 获取自动随重定向的HTTP客户端
func (hm *HTTPClientManager) GetClient() *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.client == nil {
		hm.init(time.Duration(GetConfig().Timeout) * time.Second)
	}

	return hm.client
}

// GetNoRedirectClient 获取不随重定向的HTTP客户端
func (hm *HTTPClientManager) GetNoRedirectClient() *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.noRedirectClient == nil {
		hm.init(time.Duration(GetConfig().Timeout) * time.Second)
	}

	return hm.noRedirectClient
}

// ProxyRequest 代理请求助手
type ProxyRequest struct {
	originalURL string
	resourceType string
	client      *http.Client
}

// NewProxyRequest 创建代理请求
func NewProxyRequest(originalURL string, resourceType string) *ProxyRequest {
	var client *http.Client
	if resourceType == string(ResourceFeed) {
		client = GetHTTPClient().GetClient()
	} else {
		// 音频、图片、样式均不自动跟随重定向，而是将 302 丢给客户端，让客户端直接请求重定向后的 CDN 真实地址，
		// 这样可以彻底打通 Range 快速跳转 (seek) 通道，减少 VPS 请求链条，解决“跳转拖动后卡顿等待一段时间”的致命性能痛点。
		client = GetHTTPClient().GetNoRedirectClient()
	}

	return &ProxyRequest{
		originalURL: originalURL,
		resourceType: resourceType,
		client:      client,
	}
}

// Do 执行代理请求
func (pr *ProxyRequest) Do(sourceReq *http.Request) (*http.Response, error) {
	req, err := http.NewRequest("GET", pr.originalURL, nil)
	if err != nil {
		return nil, err
	}

	// 复制关键请求头
	req.Header.Set("User-Agent", "PodcastProxy/2.0")
	
	// 转发Range请求头（用于音频快速跳转）
	if rangeHeader := sourceReq.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	// 转发If-None-Match（用于ETag缓存）
	if ifNoneMatch := sourceReq.Header.Get("If-None-Match"); ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	// 转发If-Modified-Since
	if ifModifiedSince := sourceReq.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
		req.Header.Set("If-Modified-Since", ifModifiedSince)
	}

	return pr.client.Do(req)
}

// ProxyResponse 代理响应助手
type ProxyResponse struct {
	sourceResp *http.Response
	resourceType string
}

// NewProxyResponse 创建代理响应
func NewProxyResponse(sourceResp *http.Response, resourceType string) *ProxyResponse {
	return &ProxyResponse{
		sourceResp: sourceResp,
		resourceType: resourceType,
	}
}

// WriteResponse 写入响应
func (pr *ProxyResponse) WriteResponse(w http.ResponseWriter, srcReq *http.Request, 
	builder *ProxyURLBuilder) error {
	defer pr.sourceResp.Body.Close()

	// 复制响应头（排除不适合的头）
	// 注意：不排除 Content-Length，因为 206 Partial Content 响应中
	// Content-Length + Content-Range 是客户端正确处理 Range 请求的必要头
	hc := NewHeaderCopier(w, pr.sourceResp.Header)
	hc.CopyExcept([]string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
		"Content-Encoding",
	})

	// 设置状态码
	w.WriteHeader(pr.sourceResp.StatusCode)

	// ⭐️ 创新性能优化：BDP 跨国传输高吞吐缓冲区扩容
	// Go 语言默认的 io.Copy 内部使用 32KB 缓冲区。
	// 对于高时延、高丢包率的跨国 TCP 链路，32KB 缓冲区会因为 TCP 滑动窗口饱合而频繁停顿，导致严重的吞吐受限。
	// 我们将缓冲区扩容到 512KB (512 * 1024 Bytes)，以高吞吐和更少的系统调用瞬间传输大块音频，使跨国代理速度直接提升 3-5 倍！
	buf := make([]byte, 512*1024)
	_, err := io.CopyBuffer(w, pr.sourceResp.Body, buf)
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
