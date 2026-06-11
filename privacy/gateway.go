package privacy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/privacy/model"
	"github.com/JishiTeam-J1wa/AoEo/privacy/store"
)

// GatewayConfig configures the privacy gateway.
type GatewayConfig struct {
	// Store is the mapping storage backend. If nil, a Pebble in-process store is created.
	Store store.MappingStore

	// Generator produces fake values. If nil, a default generator is used.
	Generator *FakeGenerator

	// ModelEndpoint is the URL of the AI privacy filter sidecar.
	// Supports single endpoint or comma-separated endpoints for load balancing.
	// Example: "http://127.0.0.1:8080"
	// Example: "http://sidecar-1:8080,http://sidecar-2:8080"
	// If empty and Detector is nil, no detection is performed.
	ModelEndpoint string

	// LBStrategy selects the load-balancing strategy when multiple endpoints
	// are configured. Defaults to RoundRobin if zero.
	// Available: RoundRobin, Random, LeastLatency.
	LBStrategy model.Strategy

	// Detector allows direct injection of a Detector for testing or advanced use.
	// If set, ModelEndpoint and LBStrategy are ignored.
	Detector Detector

	// Policy defines the default action for detected sensitive data.
	// For the pseudonymization gateway, this is typically ActionPseudonymize.
	Policy Action

	// SessionTTL is how long mappings are kept. Zero means no cleanup.
	SessionTTL time.Duration

	// FailOpen, when true, passes the request through unchanged if the sidecar
	// is unreachable or returns an error. When false (default), errors are
	// propagated and the request is blocked.
	FailOpen bool
}

// Gateway is the AoEo privacy interceptor that sits between the user and
// AI providers. It transparently replaces sensitive data with fakes before
// sending requests, and restores originals when responses come back.
type Gateway struct {
	pseudonymizer *Pseudonymizer
	sessionTTL    time.Duration
	endpoint      string
	failOpen      bool
	stats         Stats
	modelClient   model.Client // underlying model client (for health checks & close)
}

// Stats holds runtime statistics for the privacy gateway.
type Stats struct {
	RequestsPseudonymized int64
	RequestsRestored      int64
	RequestsFailed        int64
	SpansDetected         int64
}

// NewGateway creates a new privacy gateway.
func NewGateway(cfg GatewayConfig) (*Gateway, error) {
	mappingStore := cfg.Store
	if mappingStore == nil {
		var err error
		mappingStore, err = store.OpenPebble("./privacy_maps")
		if err != nil {
			return nil, fmt.Errorf("open default pebble store: %w", err)
		}
	}

	gen := cfg.Generator
	if gen == nil {
		gen = NewFakeGenerator(time.Now().UnixNano())
	}

	var detector Detector
	var mc model.Client
	if cfg.Detector != nil {
		detector = cfg.Detector
	} else if cfg.ModelEndpoint != "" {
		endpoints := splitEndpoints(cfg.ModelEndpoint)
		strategy := cfg.LBStrategy
		if strategy < 0 || strategy > model.LeastLatency {
			strategy = model.RoundRobin
		}
		if len(endpoints) > 1 {
			mc = model.NewLoadBalancedClient(cfg.ModelEndpoint, strategy)
		} else {
			mc = model.NewHTTPClient(endpoints[0])
		}
		detector = newModelDetectorAdapter(mc)
	} else {
		// No detection configured; create a no-op detector.
		detector = &noopDetector{}
	}

	return &Gateway{
		pseudonymizer: NewPseudonymizer(mappingStore, gen, detector),
		sessionTTL:    cfg.SessionTTL,
		endpoint:      cfg.ModelEndpoint,
		failOpen:      cfg.FailOpen,
		modelClient:   mc,
	}, nil
}

// Close releases resources held by the gateway.
func (g *Gateway) Close() error {
	// Stop background health check goroutines if using LoadBalancedClient.
	if g.modelClient != nil {
		if lb, ok := g.modelClient.(*model.LoadBalancedClient); ok {
			lb.Close()
		}
	}
	if g.pseudonymizer != nil && g.pseudonymizer.store != nil {
		return g.pseudonymizer.store.Close()
	}
	return nil
}

// HealthCheck pings the AI sidecar to verify it is reachable.
// Returns true if at least one backend responds with HTTP 200.
func (g *Gateway) HealthCheck(ctx context.Context) bool {
	if g.modelClient != nil {
		ok, err := g.modelClient.HealthCheck(ctx)
		if err != nil {
			core.GetLogger().Warn("privacy sidecar health check failed",
				"endpoint", g.endpoint,
				"error", err,
			)
			return false
		}
		return ok
	}
	if g.endpoint == "" {
		return false
	}
	mc := model.NewHTTPClient(g.endpoint)
	ok, err := mc.HealthCheck(ctx)
	if err != nil {
		core.GetLogger().Warn("privacy sidecar health check failed",
			"endpoint", g.endpoint,
			"error", err,
		)
		return false
	}
	return ok
}

// Stats returns runtime statistics for the privacy gateway.
func (g *Gateway) Stats() Stats {
	return g.stats
}

// BeforeRequest implements core.Interceptor. It replaces sensitive values
// in the request with fake equivalents before the request leaves the network.
func (g *Gateway) BeforeRequest(ctx context.Context, req *core.ChatCompletionRequest) error {
	sessionID := extractSessionID(ctx, req)

	newReq, mappings, err := g.pseudonymizer.PseudonymizeRequest(ctx, sessionID, req)
	if err != nil {
		g.stats.RequestsFailed++
		core.GetLogger().Error("privacy_before_request failed",
			"session", sessionID,
			"error", err,
		)
		if g.failOpen {
			core.GetLogger().Warn("privacy_fail_open: passing request through despite error", "session", sessionID)
			return nil
		}
		return fmt.Errorf("privacy gateway: %w", err)
	}

	// Pass mappings to AfterResponse via request metadata so only
	// the mappings created in this request are restored.
	if len(mappings) > 0 {
		if newReq.Metadata == nil {
			newReq.Metadata = make(map[string]any)
		}
		newReq.Metadata["privacy_mappings"] = mappings
		g.stats.RequestsPseudonymized++
		g.stats.SpansDetected += int64(len(mappings))
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

	// Use only the mappings created during this request's BeforeRequest.
	// This prevents restoring historical fake values from earlier turns.
	if raw, ok := req.Metadata["privacy_mappings"]; ok {
		if mappings, ok := raw.([]core.PrivacyMapping); ok && len(mappings) > 0 {
			restored, rerr := g.pseudonymizer.RestoreResponseWithMappings(ctx, sessionID, resp, mappings)
			if rerr != nil {
				core.GetLogger().Error("privacy_after_response failed",
					"session", sessionID,
					"error", rerr,
				)
				if g.failOpen {
					return resp, nil
				}
				return nil, fmt.Errorf("privacy restore: %w", rerr)
			}
			g.stats.RequestsRestored++
			return restored, nil
		}
	}

	// Fallback: no mappings in metadata (should not happen in normal flow).
	restored, rerr := g.pseudonymizer.RestoreResponse(ctx, sessionID, resp)
	if rerr != nil {
		core.GetLogger().Error("privacy_after_response failed",
			"session", sessionID,
			"error", rerr,
		)
		if g.failOpen {
			return resp, nil
		}
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
	g.pseudonymizer.RestoreStreamChunk(ctx, sessionID, wrapped)
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

func (n *noopDetector) DetectBatch(texts []string) []DetectResult {
	return make([]DetectResult, len(texts))
}

// splitEndpoints splits a comma-separated endpoint string.
func splitEndpoints(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
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
