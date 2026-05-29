package engine

import (
	"strings"
	"sync"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// PromptTemplate defines a prompt injection template matched by provider/model.
type PromptTemplate struct {
	Provider string            `json:"provider"` // "deepseek" or "*" for all
	Model    string            `json:"model"`    // "deepseek-v4-pro" or "*" for all
	Position string            `json:"position"` // "system", "prepend_user", "append_user"
	Content  string            `json:"content"`  // Template content with {{var}} placeholders
	Vars     map[string]string `json:"vars"`     // Variable substitution table
}

// PromptInjector manages prompt templates and injects them into requests.
type PromptInjector struct {
	mu        sync.RWMutex
	templates []PromptTemplate
}

// NewPromptInjector creates an empty injector.
func NewPromptInjector() *PromptInjector {
	return &PromptInjector{}
}

// AddTemplate registers a new prompt template.
func (pi *PromptInjector) AddTemplate(tmpl PromptTemplate) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.templates = append(pi.templates, tmpl)
}

// SetTemplates replaces all templates.
func (pi *PromptInjector) SetTemplates(tmpls []PromptTemplate) {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.templates = append([]PromptTemplate(nil), tmpls...)
}

// Clear removes all templates.
func (pi *PromptInjector) Clear() {
	pi.mu.Lock()
	defer pi.mu.Unlock()
	pi.templates = pi.templates[:0]
}

// Templates returns a deep copy of registered templates.
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

// Inject applies matching templates to the request.
func (pi *PromptInjector) Inject(providerName, model string, req *core.ChatCompletionRequest) {
	pi.mu.RLock()
	templates := pi.templates
	pi.mu.RUnlock()

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

func matchWildcard(pattern, value string) bool {
	return pattern == "" || pattern == "*" || pattern == value
}

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

func injectSystem(req *core.ChatCompletionRequest, content string) {
	for i := range req.Messages {
		if req.Messages[i].Role == "system" {
			req.Messages[i].Content = content
			return
		}
	}
	req.Messages = append([]core.Message{{Role: "system", Content: content}}, req.Messages...)
}

func injectPrependUser(req *core.ChatCompletionRequest, content string) {
	for i := range req.Messages {
		if req.Messages[i].Role == "user" {
			req.Messages[i].Content = content + "\n\n" + req.Messages[i].Content
			return
		}
	}
	req.Messages = append(req.Messages, core.Message{Role: "user", Content: content})
}

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

// WithPromptInjector returns a SchedulerOption that attaches a PromptInjector.
func WithPromptInjector(pi *PromptInjector) SchedulerOption {
	return func(s *Scheduler) {
		s.promptInjector.Store(pi)
	}
}

// InjectPrompts is a convenience helper to build a PromptInjector from templates.
func InjectPrompts(templates ...PromptTemplate) SchedulerOption {
	pi := NewPromptInjector()
	for _, t := range templates {
		pi.AddTemplate(t)
	}
	return WithPromptInjector(pi)
}

// WithSystemPromptInjector injects a system prompt for all providers/models.
func WithSystemPromptInjector(content string, vars map[string]string) SchedulerOption {
	return InjectPrompts(PromptTemplate{
		Provider: "*",
		Model:    "*",
		Position: "system",
		Content:  content,
		Vars:     vars,
	})
}
