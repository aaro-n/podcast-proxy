package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Server 服务器
type Server struct {
	config    *ProxyConfig
	mux       *http.ServeMux
	httpServer *http.Server
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
	// 健康检查
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

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
		if r.URL.Path != "/" {
			handler := NewNotFoundHandler(r)
			handler.Handle(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"service":"podcast-proxy","version":"2.0"}`))
	})

	log.Println("路由注册完成")
}

// StartWithMiddleware 带中间件启动服务器（支持优雅关闭）
func (s *Server) StartWithMiddleware() error {
	addr := fmt.Sprintf(":%s", s.config.Port)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: logMiddleware(s.mux),
	}

	// 启动信号监听（用于优雅关闭）
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("服务启动 - 监听 %s (带中间件)", addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 等待中断信号
	sig := <-quit
	log.Printf("收到信号 %v，正在优雅关闭服务...", sig)

	// 设置5秒超时关闭
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("优雅关闭失败: %w", err)
	}

	log.Println("服务已安全关闭")
	return nil
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
