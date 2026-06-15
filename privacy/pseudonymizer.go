// pseudonymizer.go 实现请求伪匿名化和响应还原的核心流程。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

var privacyLog = core.GetLogger()

// Pseudonymizer 是可逆隐私网关的核心组件。
// 它负责检测敏感值（PII），将其替换为逼真的伪造值，
// 并能在 AI 响应返回后从映射存储中还原原始值。
// 整个流程分为：检测 -> 替换 -> 存储映射 -> 还原。
type Pseudonymizer struct {
	store     store.MappingStore  // 映射存储后端，保存 fake<->original 的双向映射
	generator *FakeGenerator      // 伪造值生成器，根据标签类型生成逼真的替换值
	detector  Detector            // 敏感信息检测器，可以是本地模型或远程 sidecar
}

// NewPseudonymizer 创建一个新的匿名化处理器。
// 需要传入三个依赖：映射存储、伪造值生成器和敏感信息检测器。
//
// Param:
//   - store: store.MappingStore - 映射存储后端，保存 fake<->original 的双向映射
//   - generator: *FakeGenerator - 伪造值生成器，根据标签类型生成逼真的替换值
//   - detector: Detector - 敏感信息检测器，可以是本地模型或远程 sidecar
//
// Return:
//   - *Pseudonymizer: 初始化完成的匿名化处理器
func NewPseudonymizer(store store.MappingStore, generator *FakeGenerator, detector Detector) *Pseudonymizer {
	return &Pseudonymizer{
		store:     store,
		generator: generator,
		detector:  detector,
	}
}

// PseudonymizeRequest 处理即将发送给 AI 提供商的请求。
// 流程：
//  1. 批量检测所有消息中的敏感信息片段（Span）
//  2. 按长度降序排列，避免短字符串干扰长字符串的替换
//  3. 为每个敏感值生成伪造替换值，优先复用已有映射，同时避免碰撞
//  4. 将映射持久化到存储后端
//  5. 在所有消息内容中执行替换
//
// Param:
//   - ctx: context.Context - 请求上下文，用于存储后端操作
//   - sessionID: string - 会话标识符，用于隔离不同会话的映射
//   - req: *core.ChatCompletionRequest - 待处理的聊天请求
//
// Return:
//   - *core.ChatCompletionRequest: 替换后的新请求
//   - []core.PrivacyMapping: 本次创建的映射列表
//   - error: 存储写入失败时返回错误
func (p *Pseudonymizer) PseudonymizeRequest(ctx context.Context, sessionID string, req *core.ChatCompletionRequest) (*core.ChatCompletionRequest, []core.PrivacyMapping, error) {
	// 第一步：使用批量检测 API 扫描所有消息中的敏感信息片段
	parts := make([]string, len(req.Messages))
	for i, m := range req.Messages {
		parts[i] = m.Content
	}
	batchResults := p.detector.DetectBatch(parts)

	// 汇总所有消息中检测到的敏感片段（偏移量按每条消息独立计算）
	var spans []Span
	for _, dr := range batchResults {
		spans = append(spans, dr.Spans...)
	}

	totalLen := 0
	for _, p := range parts {
		totalLen += len(p)
	}
	privacyLog.Info("privacy_detect",
		"session", sessionID,
		"spans_found", len(spans),
		"text_len", totalLen,
	)

	if len(spans) == 0 {
		return req, nil, nil
	}

	// 第二步：按原始值长度降序排列，避免短字符串先被替换导致长字符串匹配失败
	sort.Slice(spans, func(i, j int) bool {
		return len(spans[i].Original) > len(spans[j].Original)
	})

	// 第三步：构建替换映射表，优先复用已有映射，减少不必要的生成
	replacements := make(map[string]string) // original -> fake 的映射表
	var mappings []core.PrivacyMapping

	for _, span := range spans {
		original := span.Original
		if _, ok := replacements[original]; ok {
			continue // 同一个原始值已处理过，跳过
		}

		// 检查该会话中是否已存在该原始值的映射（复用历史映射）
		if fake, ok, _ := p.store.GetFake(ctx, sessionID, original); ok {
			replacements[original] = fake
			continue
		}

		// 生成新的伪造值
		fake := p.generator.Generate(span.Label, original)

		// 碰撞检测：确保该会话中不存在相同伪造值的映射
		for {
			_, exists, _ := p.store.GetOriginal(ctx, sessionID, fake)
			if !exists {
				break
			}
			fake = p.generator.Generate(span.Label, original)
		}

		if err := p.store.Set(ctx, sessionID, fake, original, string(span.Label)); err != nil {
			return nil, nil, fmt.Errorf("create mapping: %w", err)
		}
		m := core.PrivacyMapping{
			SessionID: sessionID,
			Original:  original,
			Fake:      fake,
			Type:      string(span.Label),
			CreatedAt: time.Now(),
		}

		replacements[original] = fake
		mappings = append(mappings, m)

		// 安全修复：日志中不再记录原始 PII 值和伪造值，仅记录类型信息，防止敏感信息泄露到日志
		privacyLog.Info("privacy_replace",
			"session", sessionID,
			"type", span.Label,
		)
	}

	// 第四步：对所有消息内容逐一执行替换操作
	// 按 spans 已排序的顺序（长度降序）替换，确保长值优先于短值，防止子串干扰
	cloned := req.Clone()
	for i := range cloned.Messages {
		for _, span := range spans {
			if fake, ok := replacements[span.Original]; ok {
				cloned.Messages[i].Content = strings.ReplaceAll(cloned.Messages[i].Content, span.Original, fake)
			}
		}
	}

	privacyLog.Info("privacy_pseudonymized",
		"session", sessionID,
		"replacements", len(replacements),
	)

	return &cloned, mappings, nil
}

// RestoreResponse 处理 AI 响应，将响应中的伪造值还原为原始值。
// 从存储后端加载该会话的全部映射关系进行还原。
// 适用于无法传入精确映射的场景（如历史会话回放）。
//
// Param:
//   - ctx: context.Context - 请求上下文，用于存储后端操作
//   - sessionID: string - 会话标识符
//   - resp: *core.ChatCompletionResponse - 待还原的 AI 响应
//
// Return:
//   - *core.ChatCompletionResponse: 还原后的响应
//   - error: 加载映射失败时返回错误
func (p *Pseudonymizer) RestoreResponse(ctx context.Context, sessionID string, resp *core.ChatCompletionResponse) (*core.ChatCompletionResponse, error) {
	if resp == nil {
		return nil, nil
	}

	entries, err := p.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load mappings: %w", err)
	}
	return p.restoreWithEntries(ctx, sessionID, resp, entries)
}

// RestoreResponseWithMappings 使用指定的映射列表还原 AI 响应中的伪造值。
// 这是首选的还原路径，因为调用者明确知道当前请求创建了哪些映射，
// 可以避免误还原历史会话中遗留的伪造值（防止"过度还原"问题）。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - sessionID: string - 会话标识符
//   - resp: *core.ChatCompletionResponse - 待还原的 AI 响应
//   - mappings: []core.PrivacyMapping - 本次请求创建的精确映射列表
//
// Return:
//   - *core.ChatCompletionResponse: 还原后的响应
//   - error: 还原失败时返回错误
func (p *Pseudonymizer) RestoreResponseWithMappings(ctx context.Context, sessionID string, resp *core.ChatCompletionResponse, mappings []core.PrivacyMapping) (*core.ChatCompletionResponse, error) {
	if resp == nil {
		return nil, nil
	}
	if len(mappings) == 0 {
		return resp, nil
	}

	// 将 core.PrivacyMapping 转换为 store.Entry 以复用内部还原逻辑
	entries := make([]store.Entry, len(mappings))
	for i, m := range mappings {
		entries[i] = store.Entry{
			SessionID: m.SessionID,
			Original:  m.Original,
			Fake:      m.Fake,
		}
	}
	return p.restoreWithEntries(ctx, sessionID, resp, entries)
}

// restoreWithEntries 是内部还原核心函数，使用给定的映射条目列表还原响应内容。
// 流程：
//  1. 按伪造值长度降序排列，避免部分匹配导致的错误还原
//  2. 遍历所有响应选项，逐一执行替换
//  3. 泄漏检测：扫描响应中是否仍有未还原的伪造值
func (p *Pseudonymizer) restoreWithEntries(ctx context.Context, sessionID string, resp *core.ChatCompletionResponse, entries []store.Entry) (*core.ChatCompletionResponse, error) {
	if len(entries) == 0 {
		return resp, nil
	}

	// 按伪造值长度降序排列，防止短伪造值先被替换导致长伪造值无法匹配
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].Fake) > len(entries[j].Fake)
	})

	restoredCount := 0
	for i := range resp.Choices {
		text := resp.Choices[i].Message.Content
		for _, e := range entries {
			if strings.Contains(text, e.Fake) {
				restoredCount++
			}
			text = replaceFake(text, e.Fake, e.Original)
		}
		resp.Choices[i].Message.Content = text
	}

	// 泄漏检测：扫描所有响应选项，检查是否仍有残留的伪造值
	leaks := p.detectLeaks(resp, entries)
	if len(leaks) > 0 {
		privacyLog.Warn("privacy_restore_leak",
			"session", sessionID,
			"leaks", leaks,
		)
	}

	privacyLog.Info("privacy_restore",
		"session", sessionID,
		"mappings_loaded", len(entries),
		"restored_count", restoredCount,
		"leaks", len(leaks),
	)

	return resp, nil
}

// RestoreStreamChunk 在流式响应传输过程中实时还原伪造值。
// 从存储后端加载该会话的全部映射，对每个流式数据块的内容进行替换。
// 由于流式场景对性能敏感，如果加载映射失败或为空则直接跳过。
//
// Param:
//   - ctx: context.Context - 请求上下文
//   - sessionID: string - 会话标识符
//   - chunk: *core.StreamCompletionResponse - 当前流式数据块（会被原地替换）
func (p *Pseudonymizer) RestoreStreamChunk(ctx context.Context, sessionID string, chunk *core.StreamCompletionResponse) {
	if chunk == nil || chunk.Err != nil {
		return
	}

	entries, err := p.store.GetSession(ctx, sessionID)
	if err != nil || len(entries) == 0 {
		return
	}

	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].Fake) > len(entries[j].Fake)
	})

	restored := false
	for _, e := range entries {
		before := chunk.Chunk.Delta.Content
		after := replaceFake(before, e.Fake, e.Original)
		if after != before {
			restored = true
		}
		chunk.Chunk.Delta.Content = after
	}

	if restored {
		privacyLog.Debug("privacy_restore_stream", "session", sessionID)
	}
}

// ---------------------------------------------------------------------------
// 还原辅助函数
// ---------------------------------------------------------------------------

// replaceFake 将文本中的伪造值替换回原始值。
// 先尝试精确匹配替换，再尝试处理 AI 常见的标点符号边界情况
// （AI 模型经常在生成的值后面添加标点，如句号、逗号等）。
func replaceFake(text, fake, original string) string {
	text = strings.ReplaceAll(text, fake, original)

	// 模糊匹配：伪造值后面紧跟常见标点符号的情况
	puncts := []string{".", ",", "!", "?", ";", ":", ")", "]", "}"}
	for _, p := range puncts {
		text = strings.ReplaceAll(text, fake+p, original+p)
	}
	// 模糊匹配：伪造值前面有左括号等开标点的情况
	opens := []string{"(", "[", "{"}
	for _, p := range opens {
		text = strings.ReplaceAll(text, p+fake, p+original)
	}

	return text
}

// detectLeaks 扫描响应的所有选项，检测是否仍有残留的伪造值未被还原。
// 返回去重后的泄漏伪造值列表，用于告警和日志记录。
func (p *Pseudonymizer) detectLeaks(resp *core.ChatCompletionResponse, entries []store.Entry) []string {
	var leaks []string
	seen := make(map[string]bool) // 用于去重，同一个伪造值只报告一次
	for i := range resp.Choices {
		text := resp.Choices[i].Message.Content
		for _, e := range entries {
			if strings.Contains(text, e.Fake) && !seen[e.Fake] {
				seen[e.Fake] = true
				leaks = append(leaks, e.Fake)
			}
		}
	}
	return leaks
}
