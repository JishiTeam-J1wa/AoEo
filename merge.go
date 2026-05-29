package aoeo

import (
	"fmt"
	"strings"
)

// DualResult holds results from dual-provider completion.
type DualResult struct {
	Result1   *ChatCompletionResponse `json:"result1"`
	Result2   *ChatCompletionResponse `json:"result2"`
	Consensus bool                    `json:"consensus"`
}

// MergeChoices concatenates the content from two completion responses.
// If consensus is true, it returns the first response with averaged usage.
// If consensus is false, it returns both responses combined with a disagreement marker.
func MergeChoices(r1, r2 *ChatCompletionResponse, consensus bool) *ChatCompletionResponse {
	if r1 == nil && r2 == nil {
		return nil
	}
	if r1 == nil {
		return r2
	}
	if r2 == nil {
		return r1
	}

	merged := &ChatCompletionResponse{
		ID:    r1.ID,
		Model: r1.Model,
		Usage: Usage{
			PromptTokens:     r1.Usage.PromptTokens + r2.Usage.PromptTokens,
			CompletionTokens: r1.Usage.CompletionTokens + r2.Usage.CompletionTokens,
			TotalTokens:      r1.Usage.TotalTokens + r2.Usage.TotalTokens,
		},
	}

	if consensus {
		merged.Choices = r1.Choices
		return merged
	}

	// Disagreement: combine both outputs.
	content1 := extractContent(r1)
	content2 := extractContent(r2)
	combined := fmt.Sprintf("[Provider 1]\n%s\n\n[Provider 2]\n%s", content1, content2)

	merged.Choices = []Choice{{
		Index: 0,
		Message: Message{
			Role:    "assistant",
			Content: combined,
		},
		FinishReason: "stop",
	}}
	return merged
}

// Consensus checks if two responses have the same content.
// It normalizes whitespace and ignores case to provide a practical match
// for natural-language outputs from different models.
func Consensus(r1, r2 *ChatCompletionResponse) bool {
	if r1 == nil || r2 == nil {
		return false
	}
	return normalizeContent(extractContent(r1)) == normalizeContent(extractContent(r2))
}

// normalizeContent collapses whitespace and lowercases text for comparison.
func normalizeContent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func extractContent(r *ChatCompletionResponse) string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}
