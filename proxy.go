package main

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTPClientManager HTTP客户端管理器
type HTTPClientManager struct {
	client *http.Client
	mu     sync.Mutex
}

// init 初始化HTTP客户端
func (hm *HTTPClientManager) init(timeout time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

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
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				// 支持Range请求
				DisableCompression: false,
			},
		}
	}
}

// GetClient 获取HTTP客户端
func (hm *HTTPClientManager) GetClient() *http.Client {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.client == nil {
		hm.init(time.Duration(GetConfig().Timeout) * time.Second)
	}

	return hm.client
}

// ProxyRequest 代理请求助手
type ProxyRequest struct {
	originalURL string
	resourceType string
	client      *http.Client
}

// NewProxyRequest 创建代理请求
func NewProxyRequest(originalURL string, resourceType string) *ProxyRequest {
	return &ProxyRequest{
		originalURL: originalURL,
		resourceType: resourceType,
		client: GetHTTPClient().GetClient(),
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
	hc := NewHeaderCopier(w, pr.sourceResp.Header)
	hc.CopyExcept([]string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailers", "Transfer-Encoding", "Upgrade",
		"Content-Length", "Content-Encoding",
	})

	// 设置状态码
	w.WriteHeader(pr.sourceResp.StatusCode)

	// 写入响应体
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

// StreamRequest 流式请求助手（用于处理Range和大文件）
type StreamRequest struct {
	client *http.Client
}

// NewStreamRequest 创建流式请求
func NewStreamRequest() *StreamRequest {
	return &StreamRequest{
		client: GetHTTPClient().GetClient(),
	}
}

// Stream 执行流式请求
// 支持Range请求用于快速跳转和节省流量
func (sr *StreamRequest) Stream(originalURL string, sourceReq *http.Request,
	onChunk func(chunk []byte) error) error {
	
	req, err := http.NewRequest("GET", originalURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "PodcastProxy/2.0")

	// 转发Range请求头
	if rangeHeader := sourceReq.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := sr.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 分块读取和处理数据
	chunk := make([]byte, 8*1024) // 8KB缓冲区
	for {
		n, err := resp.Body.Read(chunk)
		if n > 0 {
			if err := onChunk(chunk[:n]); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}
