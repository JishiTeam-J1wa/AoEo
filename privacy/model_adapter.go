// model_adapter.go 将远程模型客户端适配为 Detector 接口，封装超时控制和错误回退逻辑。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

import (
	"context"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/model"
)

// modelDetectorAdapter 将 model.Client 适配为 Detector 接口。
// Detect 超时为 15 秒，DetectBatch 超时为 30 秒；
// 检测失败时返回空结果（不回传错误），由网关层根据 FailOpen 策略决定是否放行。
type modelDetectorAdapter struct {
	client model.Client
}

// newModelDetectorAdapter 从模型客户端创建检测器适配器。
//
// Param:
//   - client: model.Client - 底层 AI 隐私过滤客户端
//
// Return:
//   - Detector: 适配后的检测器实例
func newModelDetectorAdapter(client model.Client) Detector {
	return &modelDetectorAdapter{client: client}
}

// Detect 发送单段文本到模型进行检测，超时 15 秒。
// 失败时记录警告日志并返回空结果，不向上传播错误。
//
// Param:
//   - text: string - 待检测的文本内容
//
// Return:
//   - DetectResult: 检测结果；模型调用失败时返回空结果
func (a *modelDetectorAdapter) Detect(text string) DetectResult {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spans, err := a.client.Detect(ctx, text)
	if err != nil {
		core.GetLogger().Warn("privacy model detect failed", "error", err)
		return DetectResult{}
	}
	result := make([]Span, 0, len(spans))
	for _, s := range spans {
		result = append(result, Span{
			Label:    EntityType(s.Label),
			Original: s.Text,
			Start:    s.Start,
			End:      s.End,
			Score:    s.Score,
		})
	}
	return DetectResult{Spans: result}
}

// DetectBatch 批量发送文本到模型进行检测，超时 30 秒。
// 批量调用失败时自动回退为逐条 Detect 调用，确保不因单次批量请求失败导致检测完全不可用。
//
// Param:
//   - texts: []string - 待检测的文本列表
//
// Return:
//   - []DetectResult: 每段文本对应的检测结果；批量失败时回退为逐条检测的结果
func (a *modelDetectorAdapter) DetectBatch(texts []string) []DetectResult {
	if len(texts) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	results, err := a.client.DetectBatch(ctx, texts)
	if err != nil {
		core.GetLogger().Warn("privacy model batch detect failed, falling back to individual calls", "error", err)
		out := make([]DetectResult, len(texts))
		for i, t := range texts {
			out[i] = a.Detect(t)
		}
		return out
	}
	out := make([]DetectResult, 0, len(results))
	for _, spans := range results {
		result := make([]Span, 0, len(spans))
		for _, s := range spans {
			result = append(result, Span{
				Label:    EntityType(s.Label),
				Original: s.Text,
				Start:    s.Start,
				End:      s.End,
				Score:    s.Score,
			})
		}
		out = append(out, DetectResult{Spans: result})
	}
	return out
}
