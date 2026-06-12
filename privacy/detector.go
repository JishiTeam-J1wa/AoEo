// detector.go 定义 PII / 敏感数据检测的抽象接口。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

// Detector 是 PII / 敏感数据检测的抽象接口。
// 实现包括 modelDetectorAdapter（基于 OpenAI Privacy Filter 模型）
// 和 noopDetector（空操作回退）。
type Detector interface {
	// Detect 检测单段文本中的敏感信息片段。
	//
	// Param:
	//   - text: string - 待检测的文本内容
	//
	// Return:
	//   - DetectResult: 包含所有检测到的敏感片段的结果
	Detect(text string) DetectResult

	// DetectBatch 批量检测多段文本中的敏感信息片段。
	// 返回的切片长度与输入文本列表一致。
	//
	// Param:
	//   - texts: []string - 待检测的文本列表
	//
	// Return:
	//   - []DetectResult: 每段文本对应的检测结果，顺序与输入一致
	DetectBatch(texts []string) []DetectResult
}
