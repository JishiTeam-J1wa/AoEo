// handler_admin.go 实现管理 API 端点，包括 Provider 状态、统计信息、
// 配置热重载和 Provider 连通性测试。
//
// 所有管理接口均以 /admin 为前缀，建议在生产环境中通过中间件限制访问。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
)

// ProviderStatusHandler 处理 GET /admin/providers 请求。
//
// 返回所有 Provider 的运行时状态 JSON 数组，包括名称、可用性、模型和健康指标。
//
// 响应示例：
//
//	[
//	  {"name": "deepseek", "available": true, "model": "deepseek-chat", "health": {...}},
//	  ...
//	]
func (s *Server) ProviderStatusHandler(w http.ResponseWriter, r *http.Request) {
	statuses := s.Client.ProviderStatus()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(statuses)
}

// StatsHandler 处理 GET /admin/stats 请求。
//
// 返回按 Provider 聚合的统计信息 JSON，包括调用次数、失败次数、延迟和成本等。
//
// 响应示例：
//
//	{
//	  "deepseek": {"provider": "deepseek", "total_calls": 100, "failed_calls": 2, ...},
//	  ...
//	}
//
// 未配置历史记录时返回空 JSON 对象 {}。
func (s *Server) StatsHandler(w http.ResponseWriter, r *http.Request) {
	stats := s.Client.Stats()

	if stats == nil {
		stats = map[string]engine.ProviderStats{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(stats)
}

// ReloadHandler 创建 PUT /admin/config/reload 处理器。
//
// 接受一个回调函数，调用时触发配置热重载。适用于运行时更新 Provider 列表、
// 路由策略等配置而无需重启服务。
//
// Param:
//   - reloadFn: func() error - 配置重载回调函数
//
// Return:
//   - http.HandlerFunc: 可直接注册到路由的 HTTP 处理器
func ReloadHandler(reloadFn func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := reloadFn(); err != nil {
			writeError(w, http.StatusInternalServerError, "config reload failed: "+err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"message": "configuration reloaded successfully",
		})
	}
}

// TestProviderHandler 处理 POST /admin/providers/{name}/test 请求。
//
// 对指定名称的 Provider 执行连通性测试（发送简单请求验证可用性）。
//
// URL 路径参数：
//   - {name}: Provider 名称（从 URL 路径中手动解析）
//
// 响应：
//   - 成功：{"status": "ok", "provider": "<name>"}
//   - 失败：OpenAI 格式错误响应
func (s *Server) TestProviderHandler(w http.ResponseWriter, r *http.Request) {
	// 从 URL 路径中提取 Provider 名称
	// 预期格式：/admin/providers/{name}/test
	name := extractProviderName(r.URL.Path)
	if name == "" {
		writeError(w, http.StatusBadRequest, "missing provider name in URL path")
		return
	}

	if err := s.Client.TestProvider(r.Context(), name); err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider test failed: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":   "ok",
		"provider": name,
	})
}

// extractProviderName 从 URL 路径中提取 Provider 名称。
//
// 预期路径格式：/admin/providers/{name}/test
//
// Param:
//   - path: string - 请求 URL 路径
//
// Return:
//   - string: Provider 名称，解析失败时返回空字符串
func extractProviderName(path string) string {
	// 移除前缀 /admin/providers/
	const prefix = "/admin/providers/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)

	// 移除后缀 /test
	const suffix = "/test"
	if !strings.HasSuffix(rest, suffix) {
		return ""
	}
	name := strings.TrimSuffix(rest, suffix)

	// 名称不应包含路径分隔符（防止注入）
	if strings.Contains(name, "/") {
		return ""
	}

	return name
}
