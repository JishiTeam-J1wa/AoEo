package engine

import (
	"context"
	"fmt"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/providers"
)

// Audit performs a secondary completion using a different provider and compares results.
// It requires at least 2 available providers in the scheduler.
func (s *Scheduler) Audit(ctx context.Context, req core.ChatCompletionRequest) (*core.AuditResult, error) {
	// Identify primary provider explicitly so we can compare by name later.
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, fmt.Errorf("no available provider")
	}
	if len(available) < 2 {
		return nil, fmt.Errorf("audit requires at least 2 available providers, got %d", len(available))
	}
	primaryProvider := available[0]

	reqCopy := req.Clone()
	if reqCopy.Model == "" {
		reqCopy.Model = primaryProvider.Config().Model
	}

	auditCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	// Get primary result.
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

	// Get audit result from a different provider.
	var auditProvider providers.Provider
	for attempt := 0; attempt < len(available)*2 && auditProvider == nil; attempt++ {
		candidate := s.PickProviderRoundRobin()
		if candidate != nil && candidate.Name() != primaryProvider.Name() {
			auditProvider = candidate
		}
	}

	var auditResp *core.ChatCompletionResponse
	if auditProvider != nil {
		auditReqCopy := req.Clone()
		if auditReqCopy.Model == "" {
			auditReqCopy.Model = auditProvider.Config().Model
		}
		if err := s.sem.Acquire(auditCtx); err != nil {
			return nil, err
		}
		auditResp, err = func() (result *core.ChatCompletionResponse, err error) {
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
		if err != nil {
			core.GetLogger().Warn("audit completion failed", "error", err)
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
		result.Consensus = true // No audit available, assume primary is correct.
	}

	return result, nil
}
