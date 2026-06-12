// audit.go 实现双 Provider 审计对比功能，通过二次补全校验主 Provider 结果的可靠性。
//
// Author: JishiTeam-J1wa
// Created: 2026-05
//
// Changelog:
//   2026-06-12 - 注释体系规范化

package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

// Audit 使用两个不同的 Provider 分别执行补全请求，并对比结果以进行审计校验。
//
// 执行流程：
//  1. 检查调度器状态，确认至少有 2 个可用 Provider
//  2. 选择第一个可用 Provider 作为主 Provider，执行补全请求
//  3. 通过 Round-Robin 选择一个不同的 Provider 作为审计 Provider
//  4. 使用审计 Provider 执行相同请求
//  5. 通过 Consensus 函数对比两个结果的一致性
//  6. 通过 MergeChoices 合并结果（一致时取主结果，不一致时拼接对比）
//  7. 记录两次调用的历史记录（带 "audit" 标签）
//
// Param:
//   - ctx: context.Context - 请求上下文，控制整体超时
//   - req: core.ChatCompletionRequest - 聊天补全请求
//
// Return:
//   - *core.AuditResult: 审计结果，包含主结果、审计结果、一致性判断和合并后的响应
//   - error: 调度器已关闭、可用 Provider 不足 2 个、或主 Provider 调用失败时返回错误
//
// Edge Cases:
//   - 可用 Provider 少于 2 个时返回明确错误
//   - 无法选出不同的审计 Provider 时，仅返回主结果并假设 consensus=true
//   - 审计 Provider 调用失败时记录警告日志，不影响主结果的返回
func (s *Scheduler) Audit(ctx context.Context, req core.ChatCompletionRequest) (*core.AuditResult, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	// 显式确定主 Provider，以便后续通过名称对比选择不同的审计 Provider
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, ErrNoAvailableProvider
	}
	if len(available) < 2 {
		return nil, fmt.Errorf("audit requires at least 2 available providers, got %d", len(available))
	}
	primaryProvider := available[0]

	reqCopy := req.Clone()
	if reqCopy.Model == "" {
		reqCopy.Model = primaryProvider.Config().Model
	}
	if pi := s.promptInjector.Load(); pi != nil {
		pi.Inject(primaryProvider.Name(), reqCopy.Model, &reqCopy)
	}

	auditCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeout.Load()))
	defer cancel()

	start := time.Now()

	if err := s.sem.Acquire(auditCtx); err != nil {
		return nil, err
	}
	primary, err := func() (result *core.ChatCompletionResponse, err error) {
		defer func() {
			if r := recover(); r != nil {
				core.GetLogger().Error("audit primary panic recovered", "panic", r)
				err = fmt.Errorf("primary provider panic: %v", r)
			}
		}()
		result, err = primaryProvider.ChatComplete(auditCtx, reqCopy)
		return
	}()
	s.sem.Release()
	if err != nil {
		return nil, fmt.Errorf("primary completion failed: %w", err)
	}

	// 通过 Round-Robin 选择一个与主 Provider 不同的审计 Provider
	var auditProvider providers.Provider
	for attempt := 0; attempt < len(available)*2 && auditProvider == nil; attempt++ {
		candidate := s.PickProviderRoundRobin()
		if candidate != nil && candidate.Name() != primaryProvider.Name() {
			auditProvider = candidate
		}
	}

	var auditResp *core.ChatCompletionResponse
	var auditErr error
	var auditReqCopy core.ChatCompletionRequest
	if auditProvider != nil {
		auditReqCopy = req.Clone()
		if auditReqCopy.Model == "" {
			auditReqCopy.Model = auditProvider.Config().Model
		}
		if pi := s.promptInjector.Load(); pi != nil {
			pi.Inject(auditProvider.Name(), auditReqCopy.Model, &auditReqCopy)
		}
		if err := s.sem.Acquire(auditCtx); err != nil {
			return nil, err
		}
		auditResp, auditErr = func() (result *core.ChatCompletionResponse, err error) {
			defer func() {
				if r := recover(); r != nil {
					core.GetLogger().Error("audit secondary panic recovered", "panic", r)
					err = fmt.Errorf("audit provider panic: %v", r)
				}
			}()
			result, err = auditProvider.ChatComplete(auditCtx, auditReqCopy)
			return
		}()
		s.sem.Release()
		if auditErr != nil {
			core.GetLogger().Warn("audit completion failed", "error", auditErr)
		}
	}

	result := &core.AuditResult{
		Primary: primary,
		Audit:   auditResp,
	}

	if auditResp != nil {
		result.Consensus = Consensus(primary, auditResp)
		result.Adjusted = MergeChoices(primary, auditResp, result.Consensus)
		if !result.Consensus {
			core.GetLogger().Warn("audit disagreement",
				"primary", primary.Model,
				"audit", auditResp.Model)
		}
	} else {
		result.Adjusted = primary
		result.Consensus = true // 无审计结果可用，信任主 Provider
	}

	if s.history != nil {
		s.history.Record(s.buildRecord(primaryProvider, reqCopy, primary, start, nil, append(req.Tags, "audit"), ""))
		if auditProvider != nil {
			s.history.Record(s.buildRecord(auditProvider, auditReqCopy, auditResp, start, auditErr, append(req.Tags, "audit"), ""))
		}
	}

	return result, nil
}
