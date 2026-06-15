// result.go 提供 AI 响应结果的提取、合并与一致性校验功能。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

var (
	// reMarkdownFence 匹配 Markdown 代码块中的 JSON 内容（支持 ```json 和 ``` 两种格式）
	reMarkdownFence = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")

	// fieldRegexCache 缓存已编译的正则表达式，避免 ExtractField 每次调用都重新编译。
	// 设计决策：使用全局缓存 + RWMutex 而非 per-instance 缓存，是因为 ExtractField 是
	// 包级函数，多个 Scheduler 实例共享同一缓存可以减少内存占用和编译开销。
	fieldRegexCache   = make(map[string]*regexp.Regexp)
	fieldRegexCacheMu sync.RWMutex

	// fieldRegexCacheMax 限制缓存条目上限，防止长期运行时缓存无限增长。
	// 达到上限后清空整个缓存（简单策略，适合字段名种类有限的场景）。
	fieldRegexCacheMax = 100
)

// ExtractJSON 从 AI 响应的文本内容中提取 JSON 对象，采用多级回退策略：
//  1. 直接解析：尝试将整个内容作为 JSON 解析
//  2. 代码块提取：匹配 Markdown 代码块（```json ... ```）中的 JSON
//  3. 首对象提取：扫描文本定位第一个完整的 JSON 对象（通过花括号深度匹配）
//
// Param:
//   - content: string - AI 响应的原始文本内容，可能包含 Markdown 格式
//   - v: any - JSON 反序列化的目标对象（通常为指针类型）
//
// Return:
//   - nil: 成功提取并反序列化 JSON
//   - error: 所有策略均失败时返回错误
//
// Edge Cases:
//   - content 为纯文本不含任何 JSON 时返回 error
//   - JSON 内嵌在 Markdown 代码块中时可正确提取
//   - 文本中存在多个 JSON 对象时仅提取第一个
func ExtractJSON(content string, v any) error {
	trimmed := strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(trimmed), v); err == nil {
		return nil
	}

	matches := reMarkdownFence.FindStringSubmatch(content)
	if len(matches) >= 2 {
		trimmed := strings.TrimSpace(matches[1])
		if err := json.Unmarshal([]byte(trimmed), v); err == nil {
			return nil
		}
	}

	jsonStr := findFirstJSONObject(content)
	if jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), v); err == nil {
			return nil
		}
	}

	// 尝试提取顶层 JSON 数组
	jsonArr := findFirstJSONArray(content)
	if jsonArr != "" {
		if err := json.Unmarshal([]byte(jsonArr), v); err == nil {
			return nil
		}
	}

	return fmt.Errorf("failed to extract JSON from content")
}

// findFirstJSONObject 扫描文本定位第一个完整的 JSON 对象。
// 通过花括号深度计数 + 字符串内转义处理，确保提取的 JSON 边界正确。
func findFirstJSONObject(content string) string {
	start := strings.Index(content, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	end := -1
	inString := false
	escapeNext := false
	for i := start; i < len(content); i++ {
		c := content[i]
		if escapeNext {
			escapeNext = false
			continue
		}
		if c == '\\' && inString {
			escapeNext = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}
	if end < 0 {
		return ""
	}
	return content[start:end]
}

// findFirstJSONArray 扫描文本定位第一个完整的 JSON 数组。
// 通过方括号深度计数 + 字符串内转义处理，确保提取的 JSON 数组边界正确。
func findFirstJSONArray(content string) string {
	start := strings.Index(content, "[")
	if start < 0 {
		return ""
	}
	depth := 0
	end := -1
	inString := false
	escapeNext := false
	for i := start; i < len(content); i++ {
		c := content[i]
		if escapeNext {
			escapeNext = false
			continue
		}
		if c == '\\' && inString {
			escapeNext = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '[' {
			depth++
		} else if c == ']' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}
	if end < 0 {
		return ""
	}
	return content[start:end]
}

// ExtractField 使用正则表达式从文本中提取指定字段的字符串值。
// 内部使用全局正则缓存（fieldRegexCache），首次查询某字段名时编译正则并缓存，
// 后续查询直接复用，缓存上限由 fieldRegexCacheMax 控制。
//
// Param:
//   - content: string - 待搜索的文本内容
//   - fieldName: string - 要提取的 JSON 字段名
//
// Return:
//   - string: 字段值（未找到时返回空字符串）
func ExtractField(content, fieldName string) string {
	fieldRegexCacheMu.RLock()
	re, ok := fieldRegexCache[fieldName]
	fieldRegexCacheMu.RUnlock()

	if !ok {
		re = regexp.MustCompile(`(?i)"` + regexp.QuoteMeta(fieldName) + `"\s*:\s*"([^"]*)"`)
		fieldRegexCacheMu.Lock()
		if len(fieldRegexCache) >= fieldRegexCacheMax {
			count := 0
			for k := range fieldRegexCache {
				delete(fieldRegexCache, k)
				count++
				if count >= fieldRegexCacheMax/2 {
					break
				}
			}
		}
		fieldRegexCache[fieldName] = re
		fieldRegexCacheMu.Unlock()
	}

	// 先尝试匹配字符串值
	matches := re.FindStringSubmatch(content)
	if len(matches) >= 2 {
		return matches[1]
	}

	// 再尝试匹配非字符串值（数字、布尔、null）
	reNonStr := regexp.MustCompile(`(?i)"` + regexp.QuoteMeta(fieldName) + `"\s*:\s*([^,\s}\]]+)`)
	if m := reNonStr.FindStringSubmatch(content); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}

	return ""
}

// MergeChoices 合并两个补全响应的内容。
// 当两个 Provider 的结果一致（consensus=true）时直接返回 r1 的内容；
// 不一致时将两个 Provider 的内容拼接为 "[Provider 1]\n...\n\n[Provider 2]\n..." 格式。
// Usage 信息始终累加（无论是否达成共识）。
//
// Param:
//   - r1: *core.ChatCompletionResponse - 第一个 Provider 的响应
//   - r2: *core.ChatCompletionResponse - 第二个 Provider 的响应
//   - consensus: bool - 两个响应是否内容一致（由 Consensus 函数判断）
//
// Return:
//   - *core.ChatCompletionResponse: 合并后的响应
//
// Edge Cases:
//   - r1 和 r2 均为 nil 时返回 nil
//   - 其中一个为 nil 时返回另一个
func MergeChoices(r1, r2 *core.ChatCompletionResponse, consensus bool) *core.ChatCompletionResponse {
	if r1 == nil && r2 == nil {
		return nil
	}
	if r1 == nil {
		return r2
	}
	if r2 == nil {
		return r1
	}

	merged := &core.ChatCompletionResponse{
		ID:    r1.ID,
		Model: r1.Model,
		Usage: core.Usage{
			PromptTokens:     r1.Usage.PromptTokens + r2.Usage.PromptTokens,
			CompletionTokens: r1.Usage.CompletionTokens + r2.Usage.CompletionTokens,
			TotalTokens:      r1.Usage.TotalTokens + r2.Usage.TotalTokens,
		},
	}

	if consensus {
		merged.Choices = r1.Choices
		return merged
	}

	content1 := extractContent(r1)
	content2 := extractContent(r2)
	combined := fmt.Sprintf("[Provider 1]\n%s\n\n[Provider 2]\n%s", content1, content2)

	merged.Choices = []core.Choice{{
		Index: 0,
		Message: core.Message{
			Role:    "assistant",
			Content: combined,
		},
		FinishReason: "stop",
	}}
	return merged
}

// Consensus 检查两个响应的文本内容是否一致（忽略大小写和多余空白）。
//
// Param:
//   - r1: *core.ChatCompletionResponse - 第一个响应
//   - r2: *core.ChatCompletionResponse - 第二个响应
//
// Return:
//   - bool: 内容一致返回 true，不一致或任一为 nil 返回 false
func Consensus(r1, r2 *core.ChatCompletionResponse) bool {
	if r1 == nil || r2 == nil {
		return false
	}
	return normalizeContent(extractContent(r1)) == normalizeContent(extractContent(r2))
}

// normalizeContent 对文本进行标准化处理：转小写 + 去除首尾空白 + 合并连续空白。
func normalizeContent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// extractContent 从补全响应中提取第一条 Choice 的文本内容。
func extractContent(r *core.ChatCompletionResponse) string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}
