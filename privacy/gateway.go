package privacy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// GatewayConfig configures the privacy gateway.
type GatewayConfig struct {
	// Store is the mapping database. If nil, an in-memory store is created.
	Store *MappingStore

	// Generator produces fake values. If nil, a default generator is used.
	Generator *FakeGenerator

	// Rules is the local rule engine. If nil, rule-based detection is disabled.
	Rules *RuleEngine

	// ModelDetector is the Privacy Filter model. If nil, only rules are used.
	ModelDetector ModelDetector

	// Policy defines the default action for detected sensitive data.
	// For the pseudonymization gateway, this is typically ActionPseudonymize.
	Policy Action

	// SessionTTL is how long mappings are kept. Zero means no cleanup.
	SessionTTL time.Duration
}

// Gateway is the AoEo privacy interceptor that sits between the user and
// AI providers. It transparently replaces sensitive data with fakes before
// sending requests, and restores originals when responses come back.
type Gateway struct {
	pseudonymizer *Pseudonymizer
	sessionTTL    time.Duration
}

// NewGateway creates a new privacy gateway.
func NewGateway(cfg GatewayConfig) (*Gateway, error) {
	store := cfg.Store
	if store == nil {
		var err error
		store, err = OpenMappingStore(":memory:")
		if err != nil {
			return nil, fmt.Errorf("open default mapping store: %w", err)
		}
	}

	gen := cfg.Generator
	if gen == nil {
		gen = NewFakeGenerator(time.Now().UnixNano())
	}

	var detector Detector
	if cfg.Rules != nil || cfg.ModelDetector != nil {
		detector = NewDefaultDetector(cfg.Rules, cfg.ModelDetector)
	} else {
		// No detection configured; create a no-op detector.
		detector = &noopDetector{}
	}

	return &Gateway{
		pseudonymizer: NewPseudonymizer(store, gen, detector),
		sessionTTL:    cfg.SessionTTL,
	}, nil
}

// Close releases resources held by the gateway.
func (g *Gateway) Close() error {
	if g.pseudonymizer != nil && g.pseudonymizer.store != nil {
		return g.pseudonymizer.store.Close()
	}
	return nil
}

// BeforeRequest implements core.Interceptor. It replaces sensitive values
// in the request with fake equivalents before the request leaves the network.
func (g *Gateway) BeforeRequest(ctx context.Context, req *core.ChatCompletionRequest) error {
	sessionID := extractSessionID(ctx, req)

	newReq, _, err := g.pseudonymizer.PseudonymizeRequest(sessionID, req)
	if err != nil {
		return fmt.Errorf("privacy gateway: %w", err)
	}

	*req = *newReq
	return nil
}

// AfterResponse implements core.Interceptor. It restores fake values in the
// AI response back to their original values.
func (g *Gateway) AfterResponse(ctx context.Context, req core.ChatCompletionRequest, resp *core.ChatCompletionResponse, err error) (*core.ChatCompletionResponse, error) {
	if err != nil || resp == nil {
		return resp, err
	}

	sessionID := extractSessionID(ctx, &req)
	restored, rerr := g.pseudonymizer.RestoreResponse(sessionID, resp)
	if rerr != nil {
		return nil, fmt.Errorf("privacy restore: %w", rerr)
	}
	return restored, nil
}

// AfterStreamChunk implements core.Interceptor. It restores fake values in
// streaming chunks on the fly.
func (g *Gateway) AfterStreamChunk(ctx context.Context, req core.ChatCompletionRequest, chunk *core.StreamChunk) error {
	sessionID := extractSessionID(ctx, &req)

	// Wrap in StreamCompletionResponse for the pseudonymizer.
	wrapped := &core.StreamCompletionResponse{Chunk: *chunk}
	g.pseudonymizer.RestoreStreamChunk(sessionID, wrapped)
	*chunk = wrapped.Chunk

	return nil
}

// AfterStreamDone implements core.Interceptor. It performs cleanup after a
// streaming session ends.
func (g *Gateway) AfterStreamDone(ctx context.Context, req core.ChatCompletionRequest, err error) error {
	// Future: audit logging, session cleanup, etc.
	return nil
}

// ToInterceptor converts the gateway into a core.Interceptor for use with
// AoEo's scheduler options.
func (g *Gateway) ToInterceptor() core.Interceptor {
	return core.Interceptor{
		BeforeRequest:    g.BeforeRequest,
		AfterResponse:    g.AfterResponse,
		AfterStreamChunk: g.AfterStreamChunk,
		AfterStreamDone:  g.AfterStreamDone,
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type noopDetector struct{}

func (n *noopDetector) Detect(text string) DetectResult {
	return DetectResult{}
}

// extractSessionID retrieves a session identifier from the context or request.
// If none is found, a default empty string is used (all requests share
// mappings, which is acceptable for single-user deployments).
func extractSessionID(ctx context.Context, req *core.ChatCompletionRequest) string {
	// Try to read from context first.
	if v := ctx.Value("privacy_session_id"); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}

	// Fall back to request tags.
	for _, tag := range req.Tags {
		if strings.HasPrefix(tag, "session:") {
			return strings.TrimPrefix(tag, "session:")
		}
	}

	// Default: empty session (shared mappings).
	return "default"
}
