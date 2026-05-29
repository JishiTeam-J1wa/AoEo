package aoeo

import (
	"context"
	"fmt"
)

// AuditResult holds the outcome of an audit pass.
type AuditResult struct {
	Primary   *ChatCompletionResponse `json:"primary"`
	Audit     *ChatCompletionResponse `json:"audit"`
	Consensus bool                    `json:"consensus"`
	Adjusted  *ChatCompletionResponse `json:"adjusted"`
}

// Audit performs a secondary completion using a different provider and compares results.
// It requires at least 2 available providers in the scheduler.
func (c *Client) Audit(ctx context.Context, req ChatCompletionRequest) (*AuditResult, error) {
	// Identify primary provider explicitly so we can compare by name later.
	available := c.scheduler.availableProviders()
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

	auditCtx, cancel := context.WithTimeout(ctx, c.scheduler.timeout)
	defer cancel()

	// Get primary result.
	if err := c.scheduler.sem.acquire(auditCtx); err != nil {
		return nil, err
	}
	primary, err := func() (result *ChatCompletionResponse, err error) {
		defer func() {
			if r := recover(); r != nil {
				GetLogger().Error("audit primary panic recovered", "panic", r)
				err = fmt.Errorf("primary provider panic: %v", r)
			}
		}()
		result, err = primaryProvider.ChatComplete(auditCtx, reqCopy)
		return
	}()
	c.scheduler.sem.release()
	if err != nil {
		return nil, fmt.Errorf("primary completion failed: %w", err)
	}

	// Get audit result from a different provider.
	var auditProvider Provider
	for attempt := 0; attempt < len(available)*2 && auditProvider == nil; attempt++ {
		candidate := c.scheduler.PickProviderRoundRobin()
		if candidate != nil && candidate.Name() != primaryProvider.Name() {
			auditProvider = candidate
		}
	}

	var auditResp *ChatCompletionResponse
	if auditProvider != nil {
		auditReqCopy := req.Clone()
		if auditReqCopy.Model == "" {
			auditReqCopy.Model = auditProvider.Config().Model
		}
		if err := c.scheduler.sem.acquire(auditCtx); err != nil {
			return nil, err
		}
		auditResp, err = func() (result *ChatCompletionResponse, err error) {
			defer func() {
				if r := recover(); r != nil {
					GetLogger().Error("audit secondary panic recovered", "panic", r)
					err = fmt.Errorf("audit provider panic: %v", r)
				}
			}()
			result, err = auditProvider.ChatComplete(auditCtx, auditReqCopy)
			return
		}()
		c.scheduler.sem.release()
		if err != nil {
			GetLogger().Warn("audit completion failed", "error", err)
		}
	}

	result := &AuditResult{
		Primary: primary,
		Audit:   auditResp,
	}

	if auditResp != nil {
		result.Consensus = Consensus(primary, auditResp)
		result.Adjusted = MergeChoices(primary, auditResp, result.Consensus)
		if !result.Consensus {
			c.emit(EventAuditDisagree, fmt.Sprintf(
				"audit disagreement: primary=%s vs audit=%s", primary.Model, auditResp.Model,
			))
		}
	} else {
		result.Adjusted = primary
		result.Consensus = true // No audit available, assume primary is correct.
	}

	return result, nil
}
