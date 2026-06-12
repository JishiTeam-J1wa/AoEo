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

// WithPrivacyFilter 返回一个 SchedulerOption，通过环境变量配置启用隐私网关。
// 无需任何手动配置，完全通过环境变量驱动。
//
// 支持的环境变量：
//   - AOEO_PRIVACY_ENABLED     - 设为 "true" 启用（默认: false）
//   - AOEO_PRIVACY_ENDPOINT    - OPF/sidecar URL，支持逗号分隔的集群地址（默认: http://localhost:8080）
//   - AOEO_PRIVACY_POLICY      - block|mask|pseudonymize|audit（默认: pseudonymize）
//   - AOEO_PRIVACY_FAILOPEN    - 设为 "true" 在 sidecar 故障时原样放行请求（默认: false）
//   - AOEO_PRIVACY_LB_STRATEGY - roundrobin|random|leastlatency（默认: leastlatency，适用于多端点集群）
//
// 集群配置示例：
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

		// 根据环境变量解析隐私策略类型
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
			// 安全修复：使用 slog 结构化日志格式，用键值对代替 fmt.Printf 风格的格式化字符串
			core.GetLogger().Warn("privacy gateway init failed", "error", err)
			return
		}
		engine.WithInterceptors(gw.ToInterceptor())(s)
	}
}

// WithPrivacyModel 返回一个 SchedulerOption，通过显式指定 AI sidecar 端点来启用隐私网关。
// 适用于需要程序化控制端点地址的场景，无需设置环境变量。
//
// 使用示例：
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
			// 安全修复：使用 slog 结构化日志格式，用键值对代替 fmt.Printf 风格的格式化字符串
			core.GetLogger().Warn("privacy gateway init failed", "error", err)
			return
		}
		engine.WithInterceptors(gw.ToInterceptor())(s)
	}
}

// envString 从环境变量中读取字符串值，如果未设置或为空则返回默认值。
func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBool 从环境变量中读取布尔值，如果未设置则返回默认值。
// 使用 strconv.ParseBool 解析，支持 "1", "t", "T", "TRUE", "true" 等值。
func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, _ := strconv.ParseBool(v)
		return b
	}
	return def
}

// parseLBStrategy 将环境变量字符串转换为负载均衡策略枚举值。
// 默认为 LeastLatency，适用于多端点集群部署场景，
// 因为 OPF 推理工作负载的延迟在不同实例间会有差异。
func parseLBStrategy(s string) model.Strategy {
	switch strings.ToLower(s) {
	case "roundrobin":
		return model.RoundRobin
	case "random":
		return model.Random
	case "leastlatency":
		return model.LeastLatency
	default:
		// 默认策略：集群部署使用 LeastLatency，单端点使用 RoundRobin
		return model.LeastLatency
	}
}
