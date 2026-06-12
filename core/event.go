// Package core 事件回调系统，定义事件发射器接口和标准事件主题常量。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化
package core

// EventEmitter 是可选的事件回调接口，用于接收调度器运行过程中的事件通知。
//
// 实现可挂载到 Client 以接收进度通知，例如 Provider 故障、降级触发等。
type EventEmitter interface {
	// Emit 发送一个事件，包含主题和可变数据载荷。
	//
	// 实现必须是并发安全的，且应快速返回（不要在 Emit 内执行阻塞 I/O）。
	//
	// Param:
	//   - topic: string - 事件主题标识
	//   - data: ...any - 事件数据载荷
	Emit(topic string, data ...any)
}

// NopEmitter 是空操作的 EventEmitter，适用于测试或无头环境。
type NopEmitter struct{}

func (NopEmitter) Emit(string, ...any) {}

// 调度器事件主题常量。
const (
	EventProviderFail    = "provider:fail"
	EventProviderOpen    = "provider:open"
	EventProviderRecover = "provider:recover"
	EventFallbackTrigger = "scheduler:fallback"
	EventAuditDisagree   = "audit:disagree"
	EventDualComplete    = "scheduler:dual"
)
