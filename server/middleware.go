// Package server 实现 AoEo 的 HTTP 服务端。
//
// middleware.go 实现 HTTP 中间件链，包括 API 密钥认证、请求日志和 CORS 支持。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// APIKeyAuth 返回一个中间件，用于校验 Authorization: Bearer <key> 请求头。
// 如果 apiKey 参数为空字符串，则跳过认证（开发模式）。
//
// Parameters:
//   - apiKey: 期望的 API 密钥，空字符串表示禁用认证
//
// Return:
//   - func(http.Handler) http.Handler: 中间件函数
func APIKeyAuth(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 如果未配置 API Key，跳过认证（开发模式）
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			// 获取 Authorization 请求头
			auth := r.Header.Get("Authorization")
			if auth == "" {
				http.Error(w, `{"error":"missing Authorization header"}`, http.StatusUnauthorized)
				return
			}

			// 校验 Bearer Token 格式
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"invalid Authorization format, expected Bearer token"}`, http.StatusUnauthorized)
				return
			}

			// 提取并校验 Token
			token := strings.TrimPrefix(auth, "Bearer ")
			if token != apiKey {
				http.Error(w, `{"error":"invalid API key"}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequestLogger 返回一个中间件，记录每个请求的方法、路径、状态码和耗时。
// 使用标准库 log 输出日志。
//
// Parameters:
//   - next: 下一个处理器
//
// Return:
//   - http.Handler: 包装后的处理器
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装 ResponseWriter 以捕获状态码
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK, // 默认状态码
		}

		// 执行下一个处理器
		next.ServeHTTP(wrapped, r)

		// 计算耗时并输出日志
		duration := time.Since(start)
		log.Printf("[HTTP] %s %s - %d (%v)", r.Method, r.URL.Path, wrapped.statusCode, duration)
	})
}

// CORS 返回一个中间件，添加宽松的 CORS 响应头（开发模式）。
// 允许所有来源、方法和请求头。
//
// Parameters:
//   - next: 下一个处理器
//
// Return:
//   - http.Handler: 包装后的处理器
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 设置 CORS 响应头
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// 处理 OPTIONS 预检请求
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Chain 按顺序应用中间件：CORS → RequestLogger → APIKeyAuth → handler。
// 中间件按洋葱模型执行，请求从外向内依次经过各层中间件。
//
// Parameters:
//   - handler: 核心业务处理器
//   - apiKey: API 密钥，空字符串表示禁用认证
//
// Return:
//   - http.Handler: 经过所有中间件包装的处理器
func Chain(handler http.Handler, apiKey string) http.Handler {
	// 按逆序包装：最内层先执行，最外层最后包装
	// 执行顺序：CORS → RequestLogger → APIKeyAuth → handler
	return CORS(RequestLogger(APIKeyAuth(apiKey)(handler)))
}

// responseWriter 包装 http.ResponseWriter，用于捕获响应的状态码。
// 在 RequestLogger 中间件中使用，以便在日志中输出实际返回的 HTTP 状态码。
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader 重写以捕获状态码，然后调用原始的 WriteHeader。
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
