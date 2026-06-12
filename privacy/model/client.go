// Package model 提供基于 AI 模型的 PII 检测客户端。
// 支持通过 HTTP/JSON 协议连接到 OpenAI Privacy Filter (OPF) sidecar
// 或兼容的代理服务。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package model

import "context"

// Span 是 AI 模型返回的敏感信息检测结果。
type Span struct {
	Label       string  `json:"label"`       // 实体类型，如 "person"、"phone"、"email"、"secret"
	Text        string  `json:"text"`        // 检测到的原始文本
	Start       int     `json:"start"`       // 在原始文本中的起始索引
	End         int     `json:"end"`         // 在原始文本中的结束索引
	Score       float64 `json:"score"`       // 置信度，范围 0.0~1.0
	Placeholder string  `json:"placeholder"` // OPF 替换占位符，如 "[NAME]"
}

// Client 是 AI 隐私过滤 sidecar 的抽象客户端接口。
type Client interface {
	// Detect 发送单段文本到模型进行检测，返回检测到的敏感信息片段。
	Detect(ctx context.Context, text string) ([]Span, error)

	// DetectBatch 在单次请求中发送多段文本进行批量检测，
	// 返回每段文本对应的检测结果，减少多消息请求的往返次数。
	DetectBatch(ctx context.Context, texts []string) ([][]Span, error)

	// HealthCheck 检查 sidecar 是否已就绪，就绪时返回 true。
	HealthCheck(ctx context.Context) (bool, error)
}
