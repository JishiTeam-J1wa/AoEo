// prompt.go 实现 Prompt 注入机制，支持按 Provider/Model 匹配模板并注入到请求中。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

package engine

import (
	"strings"
	"sync"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// PromptTemplate 定义一个 Prompt 注入模板，通过 Provider 和 Model 进行匹配。
type PromptTemplate struct {
	Provider string            `json:"provider"` // "deepseek" 或 "*" 匹配所有
	Model    string            `json:"model"`    // "deepseek-v4-pro" 或 "*" 匹配所有
	Position string            `json:"position"` // 注入位置："system"、"prepend_user"、"append_user"
	Content  string            `json:"content"`  // 模板内容，支持 {{var}} 占位符
	Vars     map[string]string `json:"vars"`     // 变量替换表
}

// PromptInjector 管理 Prompt 模板并在请求发送前将其注入到对应的请求中。
// 线程安全，支持并发读写。
type PromptInjector struct {
	mu        sync.RWMutex
	templates []PromptTemplate
}

// NewPromptInjector 创建一个空的 PromptInjector。
//
// Return:
//   - *PromptInjector: 新创建的注入器实例
func NewPromptInjector() *PromptInjector {
	return &PromptInjector{}
}

// AddTemplate 注册一个新的 Prompt 模板。
//
// Param:
//   - tmpl: PromptTemplate - 待注册的模板
func (pi *PromptInjector) AddTemplate(tmpl PromptTemplate) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.templates = append(pi.templates, tmpl)
}

// SetTemplates 替换所有已注册的模板。
//
// Param:
//   - tmpls: []PromptTemplate - 新的模板列表（内部会创建副本）
func (pi *PromptInjector) SetTemplates(tmpls []PromptTemplate) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.templates = append([]PromptTemplate(nil), tmpls...)
}

// Clear 移除所有已注册的模板。
func (pi *PromptInjector) Clear() {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	// nil out elements to help GC
	for i := range pi.templates {
		pi.templates[i] = PromptTemplate{}
	}
	pi.templates = pi.templates[:0]
}

// Templates 返回所有已注册模板的深拷贝。
//
// Return:
//   - []PromptTemplate: 模板列表的深拷贝（包括 Vars map 的拷贝）
func (pi *PromptInjector) Templates() []PromptTemplate {
	pi.mu.RLock()
	defer pi.mu.RUnlock()
	out := make([]PromptTemplate, len(pi.templates))
	for i, t := range pi.templates {
		out[i] = t
		if t.Vars != nil {
			out[i].Vars = make(map[string]string, len(t.Vars))
			for k, v := range t.Vars {
				out[i].Vars[k] = v
			}
		}
	}
	return out
}

// Inject 将匹配当前 Provider 和 Model 的模板注入到请求中。
// 遍历所有已注册的模板，对每个匹配的模板根据其 Position 字段执行不同的注入策略：
//   - "system"：替换或新增系统消息
//   - "prepend_user"：在用户消息前追加内容
//   - "append_user"：在用户消息后追加内容
//   - 其他值：默认按 "system" 处理
//
// Param:
//   - providerName: string - 当前 Provider 的名称，用于模板匹配
//   - model: string - 当前使用的模型名称，用于模板匹配
//   - req: *core.ChatCompletionRequest - 待注入的请求（会被直接修改）
func (pi *PromptInjector) Inject(providerName, model string, req *core.ChatCompletionRequest) {
	pi.mu.RLock()
	templates := make([]PromptTemplate, len(pi.templates))
	copy(templates, pi.templates)
	pi.mu.RUnlock()

	// 在锁外使用副本进行匹配和注入
	for _, tmpl := range templates {
		if !matchWildcard(tmpl.Provider, providerName) || !matchWildcard(tmpl.Model, model) {
			continue
		}
		content := replaceVars(tmpl.Content, tmpl.Vars)
		switch tmpl.Position {
		case "system":
			injectSystem(req, content)
		case "prepend_user":
			injectPrependUser(req, content)
		case "append_user":
			injectAppendUser(req, content)
		default:
			injectSystem(req, content)
		}
	}
}

// matchWildcard 进行简单的通配符匹配：空字符串或 "*" 匹配所有值，否则精确匹配。
func matchWildcard(pattern, value string) bool {
	return pattern == "" || pattern == "*" || pattern == value
}

// replaceVars 将模板中的 {{var}} 占位符替换为 vars 表中对应的值。
func replaceVars(template string, vars map[string]string) string {
	if len(vars) == 0 {
		return template
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

// injectSystem 替换已有的系统消息内容，或在消息列表头部插入新的系统消息。
func injectSystem(req *core.ChatCompletionRequest, content string) {
	for i := range req.Messages {
		if req.Messages[i].Role == "system" {
			req.Messages[i].Content = content + "\n\n" + req.Messages[i].Content
			return
		}
	}
	req.Messages = append([]core.Message{{Role: "system", Content: content}}, req.Messages...)
}

// injectPrependUser 在第一条用户消息前追加内容，或在消息列表末尾追加新用户消息。
func injectPrependUser(req *core.ChatCompletionRequest, content string) {
	for i := range req.Messages {
		if req.Messages[i].Role == "user" {
			req.Messages[i].Content = content + "\n\n" + req.Messages[i].Content
			return
		}
	}
	req.Messages = append(req.Messages, core.Message{Role: "user", Content: content})
}

// injectAppendUser 在最后一条用户消息后追加内容，或在消息列表末尾追加新用户消息。
func injectAppendUser(req *core.ChatCompletionRequest, content string) {
	lastUser := -1
	for i := range req.Messages {
		if req.Messages[i].Role == "user" {
			lastUser = i
		}
	}
	if lastUser >= 0 {
		req.Messages[lastUser].Content = req.Messages[lastUser].Content + "\n\n" + content
	} else {
		req.Messages = append(req.Messages, core.Message{Role: "user", Content: content})
	}
}

// WithPromptInjector 返回一个 SchedulerOption，将 PromptInjector 挂载到调度器。
//
// Param:
//   - pi: *PromptInjector - 要挂载的注入器实例
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithPromptInjector(pi *PromptInjector) SchedulerOption {
	return func(s *Scheduler) {
		s.promptInjector.Store(pi)
	}
}

// InjectPrompts 是一个便捷函数，从模板列表构建 PromptInjector 并返回对应的 SchedulerOption。
//
// Param:
//   - templates: ...PromptTemplate - 要注册的模板列表
//
// Return:
//   - SchedulerOption: 调度器配置选项
func InjectPrompts(templates ...PromptTemplate) SchedulerOption {
	pi := NewPromptInjector()
	for _, t := range templates {
		pi.AddTemplate(t)
	}
	return WithPromptInjector(pi)
}

// WithSystemPromptInjector 为所有 Provider/Model 注入统一的系统 Prompt。
//
// Param:
//   - content: string - 系统 Prompt 内容，支持 {{var}} 占位符
//   - vars: map[string]string - 变量替换表
//
// Return:
//   - SchedulerOption: 调度器配置选项
func WithSystemPromptInjector(content string, vars map[string]string) SchedulerOption {
	return InjectPrompts(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "system",
		Content:  content,
		Vars:     vars,
	})
}
