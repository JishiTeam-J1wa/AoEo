package model

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
)

// LoadBalancedClient distributes Detect calls across multiple sidecar backends.
// It implements model.Client with round-robin scheduling, health checks, and
// automatic failover.
type LoadBalancedClient struct {
	backends   []*backend
	strategy   Strategy
	idx        atomic.Uint64
	hcInterval time.Duration
	autoHC     bool
	stopHC     chan struct{}
	hcOnce     sync.Once
}

// Strategy defines how requests are distributed across backends.
type Strategy int

const (
	// RoundRobin cycles through backends in order.
	RoundRobin Strategy = iota
	// Random picks a backend at random.
	Random
	// LeastLatency routes to the backend with the lowest EWMA latency.
	LeastLatency
)

// backend wraps a single sidecar instance.
type backend struct {
	client    *HTTPClient
	endpoint  string
	healthy   atomic.Bool
	lastErr   atomic.Pointer[string]
	lastCheck atomic.Int64 // unix nano

	// EWMA latency tracking (nanoseconds). Updated after every successful Detect.
	latencyNs atomic.Int64
}

// ewmaAlpha is the smoothing factor for EWMA latency updates.
const ewmaAlpha = 0.3

// LoadBalancedClientOption configures a LoadBalancedClient.
type LoadBalancedClientOption func(*LoadBalancedClient)

// WithHCInterval sets the health check interval (default 10s).
func WithHCInterval(d time.Duration) LoadBalancedClientOption {
	return func(lb *LoadBalancedClient) {
		lb.hcInterval = d
	}
}

// WithAutoHealthCheck controls whether the background health checker starts
// automatically (default true). Set to false for tests that manage health
// checks manually.
func WithAutoHealthCheck(auto bool) LoadBalancedClientOption {
	return func(lb *LoadBalancedClient) {
		lb.autoHC = auto
	}
}

// NewLoadBalancedClient creates a client that balances across the given endpoints.
// Endpoints may be comma-separated.
func NewLoadBalancedClient(endpoints string, strategy Strategy, opts ...HTTPClientOption) *LoadBalancedClient {
	return NewLoadBalancedClientWithOptions(endpoints, strategy, opts, nil)
}

// NewLoadBalancedClientWithOptions creates a client with full control over options.
func NewLoadBalancedClientWithOptions(endpoints string, strategy Strategy, httpOpts []HTTPClientOption, lbOpts []LoadBalancedClientOption) *LoadBalancedClient {
	parts := splitEndpoints(endpoints)
	if len(parts) == 0 {
		return &LoadBalancedClient{}
	}

	backends := make([]*backend, 0, len(parts))
	for _, ep := range parts {
		ep = strings.TrimSpace(ep)
		if ep == "" {
			continue
		}
		b := &backend{
			client:   NewHTTPClient(ep, httpOpts...),
			endpoint: ep,
		}
		b.healthy.Store(true) // optimistic until first health check
		backends = append(backends, b)
	}

	lb := &LoadBalancedClient{
		backends:   backends,
		strategy:   strategy,
		hcInterval: 10 * time.Second,
		autoHC:     true,
		stopHC:     make(chan struct{}),
	}
	for _, opt := range lbOpts {
		opt(lb)
	}
	if lb.autoHC {
		lb.startHealthChecks()
	}
	return lb
}

// Detect sends text to a healthy backend. If the chosen backend fails,
// it falls back to the next healthy one.
func (lb *LoadBalancedClient) Detect(ctx context.Context, text string) ([]Span, error) {
	order := lb.pickOrder()
	var lastErr error

	for _, b := range order {
		if !b.healthy.Load() {
			continue
		}
		spans, err := lb.detectOn(b, ctx, text)
		if err == nil {
			return spans, nil
		}
		lastErr = err
		b.healthy.Store(false)
		s := err.Error()
		b.lastErr.Store(&s)
		core.GetLogger().Warn("privacy backend failed, trying next",
			"endpoint", b.endpoint,
			"error", err,
		)
	}

	return nil, fmt.Errorf("all privacy backends failed (last: %w)", lastErr)
}

// DetectBatch distributes a batch of texts across backends.
// For simplicity, all texts are sent to the same backend (chosen by strategy).
// If that backend fails, it falls back to the next healthy one.
func (lb *LoadBalancedClient) DetectBatch(ctx context.Context, texts []string) ([][]Span, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	order := lb.pickOrder()
	var lastErr error

	for _, b := range order {
		if !b.healthy.Load() {
			continue
		}
		start := time.Now()
		results, err := b.client.DetectBatch(ctx, texts)
		if err == nil {
			b.updateLatency(time.Since(start))
			return results, nil
		}
		lastErr = err
		b.healthy.Store(false)
		s := err.Error()
		b.lastErr.Store(&s)
		core.GetLogger().Warn("privacy backend batch failed, trying next",
			"endpoint", b.endpoint,
			"error", err,
		)
	}

	return nil, fmt.Errorf("all privacy backends failed batch (last: %w)", lastErr)
}

// detectOn wraps a Detect call with EWMA latency tracking.
func (lb *LoadBalancedClient) detectOn(b *backend, ctx context.Context, text string) ([]Span, error) {
	start := time.Now()
	spans, err := b.client.Detect(ctx, text)
	if err == nil {
		b.updateLatency(time.Since(start))
	}
	return spans, err
}

// updateLatency updates the backend's EWMA latency.
func (b *backend) updateLatency(d time.Duration) {
	oldNs := b.latencyNs.Load()
	newNs := int64(float64(oldNs)*(1-ewmaAlpha) + float64(d.Nanoseconds())*ewmaAlpha)
	b.latencyNs.Store(newNs)
}

// HealthCheck returns true if at least one backend is healthy.
func (lb *LoadBalancedClient) HealthCheck(ctx context.Context) (bool, error) {
	for _, b := range lb.backends {
		if b.healthy.Load() {
			return true, nil
		}
	}
	return false, nil
}

// Close stops the background health checker.
func (lb *LoadBalancedClient) Close() {
	close(lb.stopHC)
}

// Stats returns per-backend health status.
func (lb *LoadBalancedClient) Stats() []BackendStats {
	out := make([]BackendStats, 0, len(lb.backends))
	for _, b := range lb.backends {
		st := BackendStats{
			Endpoint:  b.endpoint,
			Healthy:   b.healthy.Load(),
			LastCheck: time.Unix(0, b.lastCheck.Load()),
			LatencyMs: time.Duration(b.latencyNs.Load()).Milliseconds(),
		}
		if p := b.lastErr.Load(); p != nil {
			st.LastError = *p
		}
		out = append(out, st)
	}
	return out
}

// BackendStats describes the health of a single backend.
type BackendStats struct {
	Endpoint  string
	Healthy   bool
	LastCheck time.Time
	LastError string
	LatencyMs int64
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (lb *LoadBalancedClient) pickOrder() []*backend {
	n := len(lb.backends)
	if n == 0 {
		return nil
	}

	switch lb.strategy {
	case Random:
		shuffled := make([]*backend, n)
		copy(shuffled, lb.backends)
		for i := n - 1; i > 0; i-- {
			j := rand.Intn(i + 1)
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}
		return shuffled

	case LeastLatency:
		// Sort by EWMA latency ascending, but filter out unhealthy backends first.
		healthy := make([]*backend, 0, n)
		for _, b := range lb.backends {
			if b.healthy.Load() {
				healthy = append(healthy, b)
			}
		}
		if len(healthy) == 0 {
			return lb.backends // fallback to all
		}
		sortBackendsByLatency(healthy)
		return healthy

	default: // RoundRobin
		start := int(lb.idx.Add(1)) % n
		if start < 0 {
			start = 0
		}
		ordered := make([]*backend, 0, n)
		ordered = append(ordered, lb.backends[start:]...)
		ordered = append(ordered, lb.backends[:start]...)
		return ordered
	}
}

func sortBackendsByLatency(bes []*backend) {
	for i := 0; i < len(bes); i++ {
		for j := i + 1; j < len(bes); j++ {
			if bes[i].latencyNs.Load() > bes[j].latencyNs.Load() {
				bes[i], bes[j] = bes[j], bes[i]
			}
		}
	}
}

func (lb *LoadBalancedClient) startHealthChecks() {
	lb.hcOnce.Do(func() {
		go lb.healthCheckLoop()
	})
}

func (lb *LoadBalancedClient) healthCheckLoop() {
	// Initial check immediately (with warm-up).
	lb.runHealthChecks(true)

	ticker := time.NewTicker(lb.hcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lb.runHealthChecks(false)
		case <-lb.stopHC:
			return
		}
	}
}

func (lb *LoadBalancedClient) runHealthChecks(warm bool) {
	for _, b := range lb.backends {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ok, err := b.client.HealthCheck(ctx)
		cancel()

		b.lastCheck.Store(time.Now().UnixNano())
		wasHealthy := b.healthy.Load()
		b.healthy.Store(ok)

		if !ok && err != nil {
			s := err.Error()
			b.lastErr.Store(&s)
		}

		if wasHealthy && !ok {
			core.GetLogger().Error("privacy backend unhealthy",
				"endpoint", b.endpoint,
				"error", err,
			)
		} else if !wasHealthy && ok {
			core.GetLogger().Info("privacy backend recovered", "endpoint", b.endpoint)
			b.lastErr.Store(nil)
		}

		// Warm-up: if healthy, send a trivial detect to establish TCP / HTTP/2 connection.
		if warm && ok {
			go func(bk *backend) {
				wCtx, wCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer wCancel()
				// Send empty text to warm the connection (sidecar should return empty spans).
				start := time.Now()
				_, _ = bk.client.Detect(wCtx, "privacy_warmup")
				// Don't care about the result; we just want the connection established.
				core.GetLogger().Debug("privacy backend warmed up",
					"endpoint", bk.endpoint,
					"latency_ms", time.Since(start).Milliseconds(),
				)
			}(b)
		}
	}
}

func splitEndpoints(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
