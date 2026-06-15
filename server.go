package main

import (
	"fmt"
	"log"
	"net/http"
)

// Server 服务器
type Server struct {
	config    *ProxyConfig
	mux       *http.ServeMux
}

// NewServer 创建服务器
func NewServer(cfg *ProxyConfig) *Server {
	return &Server{
		config: cfg,
		mux:    http.NewServeMux(),
	}
}

// RegisterRoutes 注册路由
func (s *Server) RegisterRoutes() {
	// 饲送处理
	s.mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		handler := NewFeedHandler(r)
		handler.Handle(w, r)
	})

	// 音频处理（支持Range请求用于快速跳转）
	s.mux.HandleFunc("/audio/", func(w http.ResponseWriter, r *http.Request) {
		handler := NewAudioHandler(r)
		handler.Handle(w, r)
	})
	s.mux.HandleFunc("/audio", func(w http.ResponseWriter, r *http.Request) {
		handler := NewAudioHandler(r)
		handler.Handle(w, r)
	})

	// 图片处理
	s.mux.HandleFunc("/image/", func(w http.ResponseWriter, r *http.Request) {
		handler := NewImageHandler(r)
		handler.Handle(w, r)
	})
	s.mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		handler := NewImageHandler(r)
		handler.Handle(w, r)
	})

	// 样式处理
	s.mux.HandleFunc("/style/", func(w http.ResponseWriter, r *http.Request) {
		handler := NewStyleHandler(r)
		handler.Handle(w, r)
	})
	s.mux.HandleFunc("/style", func(w http.ResponseWriter, r *http.Request) {
		handler := NewStyleHandler(r)
		handler.Handle(w, r)
	})

	// 根路由 - 所有请求返回404
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handler := NewNotFoundHandler(r)
		handler.Handle(w, r)
	})

	log.Println("路由注册完成")
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%s", s.config.Port)
	log.Printf("服务启动 - 监听 %s", addr)
	return http.ListenAndServe(addr, s.mux)
}

// StartWithMiddleware 带中间件启动服务器
func (s *Server) StartWithMiddleware() error {
	addr := fmt.Sprintf(":%s", s.config.Port)
	log.Printf("服务启动 - 监听 %s (带中间件)", addr)
	return http.ListenAndServe(addr, logMiddleware(s.mux))
}

// logMiddleware 日志中间件
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger := NewLoggerHelper(r)
		logger.LogStart()

		// 创建响应写入器包装器来捕获状态码
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		logger.LogComplete(wrapped.statusCode)
	})
}

// responseWriter 响应写入器包装器
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	if !rw.written {
		rw.statusCode = statusCode
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.statusCode = http.StatusOK
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// CORSMiddleware CORS中间件（可选）
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// CompressionMiddleware 压缩中间件（可选）
func CompressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 这里可以添加gzip压缩逻辑
		next.ServeHTTP(w, r)
	})
}
