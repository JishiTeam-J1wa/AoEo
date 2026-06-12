package privacy

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
	"github.com/JishiTeam-J1wa/AoEo/privacy/model"
)

// WithPrivacyFilter returns a SchedulerOption that enables the privacy gateway
// using configuration from environment variables. Zero manual configuration required.
//
// Environment variables:
//   - AOEO_PRIVACY_ENABLED    - "true" to enable (default: false)
//   - AOEO_PRIVACY_ENDPOINT   - OPF/sidecar URL, supports comma-separated for cluster (default: http://localhost:8080)
//   - AOEO_PRIVACY_POLICY     - block|mask|pseudonymize|audit (default: pseudonymize)
//   - AOEO_PRIVACY_FAILOPEN   - "true" to pass through on sidecar failure (default: false)
//   - AOEO_PRIVACY_LB_STRATEGY - roundrobin|random|leastlatency (default: leastlatency for multi-endpoint)
//
// Cluster example:
//
//	export AOEO_PRIVACY_ENDPOINT=http://opf-1:8000,http://opf-2:8000,http://opf-3:8000
//	export AOEO_PRIVACY_LB_STRATEGY=leastlatency
//	client, err := aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
func WithPrivacyFilter() engine.SchedulerOption {
	return func(s *engine.Scheduler) {
		if !envBool("AOEO_PRIVACY_ENABLED", false) {
			return
		}

		cfg := GatewayConfig{
			ModelEndpoint: envString("AOEO_PRIVACY_ENDPOINT", "http://localhost:8080"),
			SessionTTL:    7 * 24 * time.Hour,
			FailOpen:      envBool("AOEO_PRIVACY_FAILOPEN", false),
			LBStrategy:    parseLBStrategy(envString("AOEO_PRIVACY_LB_STRATEGY", "")),
		}

		policy := envString("AOEO_PRIVACY_POLICY", "pseudonymize")
		switch strings.ToLower(policy) {
		case "block":
			cfg.Policy = ActionBlock
		case "mask":
			cfg.Policy = ActionMask
		case "audit":
			cfg.Policy = ActionAudit
		default:
			cfg.Policy = ActionPseudonymize
		}

		gw, err := NewGateway(cfg)
		if err != nil {
			core.GetLogger().Warn("privacy gateway init failed: %v", err)
			return
		}
		engine.WithInterceptors(gw.ToInterceptor())(s)
	}
}

// WithPrivacyModel returns a SchedulerOption that enables the privacy gateway
// with an explicit AI sidecar endpoint. Use this when you need programmatic control.
//
// Example:
//
//	client, err := aoeo.NewClient(cfg, privacy.WithPrivacyModel("http://localhost:8080"))
func WithPrivacyModel(endpoint string) engine.SchedulerOption {
	return func(s *engine.Scheduler) {
		gw, err := NewGateway(GatewayConfig{
			ModelEndpoint: endpoint,
			Policy:        ActionPseudonymize,
			SessionTTL:    7 * 24 * time.Hour,
		})
		if err != nil {
			core.GetLogger().Warn("privacy gateway init failed: %v", err)
			return
		}
		engine.WithInterceptors(gw.ToInterceptor())(s)
	}
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, _ := strconv.ParseBool(v)
		return b
	}
	return def
}

// parseLBStrategy converts an environment variable string to a model.Strategy.
// Defaults to LeastLatency for multi-endpoint cluster deployments, which is
// optimal for OPF inference workloads where latency varies per instance.
func parseLBStrategy(s string) model.Strategy {
	switch strings.ToLower(s) {
	case "roundrobin":
		return model.RoundRobin
	case "random":
		return model.Random
	case "leastlatency":
		return model.LeastLatency
	default:
		// Default: LeastLatency for cluster, RoundRobin for single endpoint.
		return model.LeastLatency
	}
}
