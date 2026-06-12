// Package privacy 为 AoEo 提供伪匿名化网关，确保敏感信息（PII、内部 IP、域名等）
// 不会以明文形式到达外部 AI API。网关拦截请求，将敏感值替换为逼真的伪造等价物，
// 并在 AI 响应返回给用户之前还原为原始值。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package privacy

import (
	"fmt"
	"time"
)

// EntityType 对检测到的敏感数据进行分类。
type EntityType string

const (
	EntityIP      EntityType = "ip"
	EntityDomain  EntityType = "domain"
	EntityPerson  EntityType = "person"
	EntityPhone   EntityType = "phone"
	EntityIDCard  EntityType = "idcard"
	EntitySecret  EntityType = "secret"
	EntityAddress EntityType = "address"
	EntityEmail   EntityType = "email"
	EntityURL     EntityType = "url"
	EntityDate    EntityType = "date"
)

// Severity 表示检测结果的严重程度。
type Severity int

const (
	SeverityLow Severity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// Action 定义检测到敏感值后的处理方式。
type Action int

const (
	ActionBlock Action = iota
	ActionMask
	ActionPseudonymize
	ActionAudit
)

func (a Action) String() string {
	switch a {
	case ActionBlock:
		return "block"
	case ActionMask:
		return "mask"
	case ActionPseudonymize:
		return "pseudonymize"
	case ActionAudit:
		return "audit"
	default:
		return "unknown"
	}
}

// Span 表示 Privacy Filter 模型检测到的敏感片段。
type Span struct {
	Start    int
	End      int
	Label    EntityType
	Score    float64
	Original string
}

// MappingEntry 存储可逆的 original-to-fake 映射关系。
type MappingEntry struct {
	ID        int64
	SessionID string
	Original  string
	Fake      string
	Type      EntityType
	CreatedAt time.Time
}

// DetectResult 聚合 AI Privacy Filter 模型的检测结果。
type DetectResult struct {
	Spans []Span
}

// PrivacyViolationError 在检测到敏感数据且配置策略为阻止时返回。
type PrivacyViolationError struct {
	Layer   string // "privacy_filter"
	Spans   []Span // model detections (if any)
	Message string
}

func (e *PrivacyViolationError) Error() string {
	return fmt.Sprintf("privacy violation: %s", e.Message)
}
