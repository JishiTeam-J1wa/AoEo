package privacy

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"github.com/JishiTeam-J1wa/AoEo/internal/engine"
)

// WithPrivacyFilter returns a SchedulerOption that enables the privacy gateway
// using configuration from environment variables. Zero manual configuration required.
//
// Environment variables:
//   - AOEO_PRIVACY_ENABLED  - "true" to enable (default: false)
//   - AOEO_PRIVACY_ENDPOINT - sidecar URL (default: http://localhost:8080)
//   - AOEO_PRIVACY_POLICY   - block|mask|pseudonymize|audit (default: pseudonymize)
//   - AOEO_PRIVACY_FAILOPEN - "true" to pass through on sidecar failure (default: false)
//
// Example:
//
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
