// Package server 实现 AoEo 的 HTTP 服务端。
//
// handler_health.go 实现 Kubernetes 健康探针端点，用于容器编排系统的存活和就绪检查。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"encoding/json"
	"net/http"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// HealthHandler 处理 GET /healthz 请求，作为 Kubernetes 存活探针（liveness probe）。
// 只要服务器进程正常运行，就返回 200 OK。
//
// 该端点用于检测服务器进程是否存活。如果进程崩溃或死锁，Kubernetes
// 会通过该探针发现问题并重启容器。
//
// Return:
//   - http.HandlerFunc: 处理存活检查的 HTTP 处理器
func HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 设置响应头
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// 返回简单的 JSON 响应
		response := map[string]string{
			"status": "ok",
		}
		json.NewEncoder(w).Encode(response)
	}
}

// ReadyHandler 处理 GET /readyz 请求，作为 Kubernetes 就绪探针（readiness probe）。
// 当至少有一个 Provider 可用时返回 200 OK，否则返回 503 Service Unavailable。
//
// 该端点用于检测服务器是否准备好接收流量。如果所有 Provider 都不可用
// （例如配置错误或连接失败），Kubernetes 会将该 Pod 从服务负载均衡中移除。
//
// Parameters:
//   - getStatus: 获取当前所有 Provider 状态的函数
//
// Return:
//   - http.HandlerFunc: 处理就绪检查的 HTTP 处理器
func ReadyHandler(getStatus func() []core.ProviderStatus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 设置响应头
		w.Header().Set("Content-Type", "application/json")

		// 获取所有 Provider 的状态
		statuses := getStatus()

		// 检查是否至少有一个 Provider 可用
		hasAvailable := false
		for _, s := range statuses {
			if s.Available {
				hasAvailable = true
				break
			}
		}

		// 构建响应
		response := map[string]any{
			"status":    "ready",
			"providers": len(statuses),
		}

		if hasAvailable {
			w.WriteHeader(http.StatusOK)
			response["status"] = "ready"
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			response["status"] = "not_ready"
			response["reason"] = "no available providers"
		}

		json.NewEncoder(w).Encode(response)
	}
}
