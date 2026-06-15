// Package core Token 计价引擎，根据 Token 用量和定价计算请求成本。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

import (
	"fmt"
	"math"
)

// Pricing 保存 Provider 的每千 Token 定价信息。
type Pricing struct {
	PromptPer1K     float64 `json:"promptPer1K"`     // 每千个 prompt Token 的费用
	CompletionPer1K float64 `json:"completionPer1K"` // 每千个 completion Token 的费用
	Currency        string  `json:"currency"`        // 货币类型，如 "CNY"、"USD"
}

// Cost 根据定价计算本次 Token 消耗的货币成本。
//
// Param:
//   - p: Pricing - Token 定价配置
//
// Return:
//   - float64: 总费用（prompt 费用 + completion 费用），定价均为 0 时返回 0
func (u Usage) Cost(p Pricing) float64 {
	if p.PromptPer1K == 0 && p.CompletionPer1K == 0 {
		return 0
	}
	// 转换为微单位（price * 1e6），使用整数运算避免浮点漂移
	promptCostMicro := int64(u.PromptTokens) * int64(math.Round(p.PromptPer1K*1e6)) / 1000
	completionCostMicro := int64(u.CompletionTokens) * int64(math.Round(p.CompletionPer1K*1e6)) / 1000
	return float64(promptCostMicro+completionCostMicro) / 1e6
}

// CostString 返回人类可读的费用字符串（如 "0.003500 CNY"）。
//
// Param:
//   - p: Pricing - Token 定价配置
//
// Return:
//   - string: 格式化的费用字符串，Currency 为空时默认使用 "CNY"
func (u Usage) CostString(p Pricing) string {
	currency := p.Currency
	if currency == "" {
		currency = "CNY"
	}
	return fmt.Sprintf("%.6f %s", u.Cost(p), currency)
}

// DefaultPricing 返回已知 Provider/模型的内置定价。
//
// 价格仅为近似值，可通过 ProviderConfig.Pricing 覆盖以获得精确值。
//
// Param:
//   - name: string - Provider 名称（如 "deepseek"、"kimi"）
//   - model: string - 模型标识符
//
// Return:
//   - Pricing: 匹配的定价配置，未知 Provider 返回零费用配置
func DefaultPricing(name, model string) Pricing {
	switch name {
	case "deepseek":
		if model == "deepseek-v4-pro" {
			return Pricing{PromptPer1K: 2.0, CompletionPer1K: 8.0, Currency: "CNY"}
		}
		// deepseek-v4-flash 及其他模型
		return Pricing{PromptPer1K: 1.0, CompletionPer1K: 2.0, Currency: "CNY"}
	case "kimi":
		return Pricing{PromptPer1K: 3.0, CompletionPer1K: 12.0, Currency: "CNY"}
	case "glm":
		return Pricing{PromptPer1K: 5.0, CompletionPer1K: 5.0, Currency: "CNY"}
	case "qwen":
		return Pricing{PromptPer1K: 5.0, CompletionPer1K: 10.0, Currency: "CNY"}
	default:
		return Pricing{Currency: "CNY"}
	}
}
