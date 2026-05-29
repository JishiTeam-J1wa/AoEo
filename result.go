package aoeo

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	reMarkdownFence = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?```")
	fieldRegexCache sync.Map // map[string]*regexp.Regexp
)

// ExtractJSON extracts a JSON object from the content using multiple strategies:
// 1. Direct JSON parse
// 2. Markdown code fence extraction
// 3. First JSON object in text
func ExtractJSON(content string, v any) error {
	// Strategy 1: Direct parse.
	trimmed := strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(trimmed), v); err == nil {
		return nil
	}

	// Strategy 2: Markdown fence.
	matches := reMarkdownFence.FindStringSubmatch(content)
	if len(matches) >= 2 {
		trimmed := strings.TrimSpace(matches[1])
		if err := json.Unmarshal([]byte(trimmed), v); err == nil {
			return nil
		}
	}

	// Strategy 3: Find first JSON object.
	jsonStr := findFirstJSONObject(content)
	if jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), v); err == nil {
			return nil
		}
	}

	return fmt.Errorf("failed to extract JSON from content")
}

// findFirstJSONObject finds the first { ... } block, handling nested braces.
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

// ExtractField extracts a string field using regex fallback.
func ExtractField(content, fieldName string) string {
	re, ok := fieldRegexCache.Load(fieldName)
	if !ok {
		re = regexp.MustCompile(`(?i)"` + regexp.QuoteMeta(fieldName) + `"\s*:\s*"(.*?)(?:"|,|\s*})`)
		fieldRegexCache.Store(fieldName, re)
	}
	matches := re.(*regexp.Regexp).FindStringSubmatch(content)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
