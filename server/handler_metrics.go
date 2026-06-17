// Package server 实现 AoEo 的 HTTP 服务端。
//
// handler_metrics.go 实现 Prometheus 兼容的指标导出端点。
// 使用纯文本格式输出，无需依赖 prometheus 客户端库。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// MetricsHandler 处理 GET /metrics 请求，导出 Prometheus 文本格式的指标数据。
//
// 导出的指标包括：
//   - aoeo_provider_available{name="xxx"}: Provider 可用性（0 或 1）
//   - aoeo_provider_health_success_rate{name="xxx"}: 请求成功率（0.0~1.0）
//   - aoeo_provider_health_avg_latency_ms{name="xxx"}: 平均延迟（毫秒）
//   - aoeo_provider_health_consecutive_fails{name="xxx"}: 连续失败次数
//   - aoeo_info{version="0.1.0"}: 版本信息（固定为 1）
//
// Parameters:
//   - getStatus: 获取当前所有 Provider 状态的函数
//
// Return:
//   - http.HandlerFunc: 处理指标导出的 HTTP 处理器
func MetricsHandler(getStatus func() []core.ProviderStatus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 设置响应头为 Prometheus 文本格式
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// 获取所有 Provider 的状态
		statuses := getStatus()

		// 构建 Prometheus 格式的指标文本
		var sb strings.Builder

		// 版本信息指标
		sb.WriteString("# HELP aoeo_info AoEo 版本信息\n")
		sb.WriteString("# TYPE aoeo_info gauge\n")
		sb.WriteString(`aoeo_info{version="0.1.0"} 1`)
		sb.WriteString("\n\n")

		// Provider 可用性指标
		sb.WriteString("# HELP aoeo_provider_available Provider 可用性状态（0=不可用，1=可用）\n")
		sb.WriteString("# TYPE aoeo_provider_available gauge\n")
		for _, s := range statuses {
			available := 0
			if s.Available {
				available = 1
			}
			sb.WriteString(fmt.Sprintf(`aoeo_provider_available{name="%s"} %d`, s.Name, available))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")

		// Provider 健康指标：成功率
		sb.WriteString("# HELP aoeo_provider_health_success_rate Provider 近期请求成功率（0.0~1.0）\n")
		sb.WriteString("# TYPE aoeo_provider_health_success_rate gauge\n")
		for _, s := range statuses {
			sb.WriteString(fmt.Sprintf(`aoeo_provider_health_success_rate{name="%s"} %.2f`, s.Name, s.Health.SuccessRate))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")

		// Provider 健康指标：平均延迟
		sb.WriteString("# HELP aoeo_provider_health_avg_latency_ms Provider 平均延迟（毫秒）\n")
		sb.WriteString("# TYPE aoeo_provider_health_avg_latency_ms gauge\n")
		for _, s := range statuses {
			sb.WriteString(fmt.Sprintf(`aoeo_provider_health_avg_latency_ms{name="%s"} %d`, s.Name, s.Health.AvgLatencyMs))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")

		// Provider 健康指标：连续失败次数
		sb.WriteString("# HELP aoeo_provider_health_consecutive_fails Provider 连续失败次数\n")
		sb.WriteString("# TYPE aoeo_provider_health_consecutive_fails gauge\n")
		for _, s := range statuses {
			sb.WriteString(fmt.Sprintf(`aoeo_provider_health_consecutive_fails{name="%s"} %d`, s.Name, s.Health.ConsecutiveFails))
			sb.WriteString("\n")
		}

		// 输出指标文本
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sb.String()))
	}
}
