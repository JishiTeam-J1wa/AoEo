// Package config YAML 配置管理系统，负责从 YAML 文件加载、解析并转换 AoEo 网关配置。
//
// 支持环境变量替换（${VAR} 和 ${VAR:-default}）、配置热重载校验和默认值填充。
// 将 YAML 结构映射到 core.Config，供 SDK 引擎直接使用。
//
// Author: JishiTeam-J1wa
// Created: 2026-06
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/JishiTeam-J1wa/AoEo/core"
	"gopkg.in/yaml.v3"
)

// ServerConfig 保存 HTTP 服务器监听配置。
type ServerConfig struct {
	Addr         string        `yaml:"addr"`          // 监听地址，默认 ":8081"
	APIKey       string        `yaml:"api_key"`       // API 鉴权密钥
	ReadTimeout  time.Duration `yaml:"read_timeout"`  // 读取超时时间（默认 120s）
	WriteTimeout time.Duration `yaml:"write_timeout"` // 写入超时时间（默认 120s）
}

// PricingYAML 保存 Provider 的每千 Token 定价信息（YAML 映射）。
// 最终会转换为 core.Pricing。
type PricingYAML struct {
	PromptPer1K     float64 `yaml:"prompt_per_1k"`     // 每千个 prompt Token 的费用
	CompletionPer1K float64 `yaml:"completion_per_1k"` // 每千个 completion Token 的费用
	Currency        string  `yaml:"currency"`          // 货币类型，如 "CNY"、"USD"
}

// ProviderYAML 保存单个 AI Provider 的配置信息（YAML 映射）。
// 最终会转换为 core.ProviderConfig。
type ProviderYAML struct {
	Name             string        `yaml:"name"`              // Provider 唯一名称
	APIKey           string        `yaml:"api_key"`           // API 密钥
	Endpoint         string        `yaml:"endpoint"`          // API 端点 URL
	Model            string        `yaml:"model"`             // 模型标识符
	MaxConcurrent    int           `yaml:"max_concurrent"`    // 最大并发请求数
	SkipTLSVerify    bool          `yaml:"skip_tls_verify"`   // 是否跳过 TLS 证书验证
	MaxFailures      int           `yaml:"max_failures"`      // 熔断器连续失败阈值
	CooldownDuration time.Duration `yaml:"cooldown"`          // 熔断器冷却时间
	Proxy            string        `yaml:"proxy"`             // HTTP/SOCKS5 代理 URL
	Pricing          PricingYAML   `yaml:"pricing"`           // Token 定价配置
}

// RouterConfig 保存路由策略配置，控制 Provider 选择算法。
type RouterConfig struct {
	Strategy       string `yaml:"strategy"`        // 路由策略："round-robin" | "random" | "weighted" | "primary"
	WeightStrategy string `yaml:"weight_strategy"` // 加权评分策略："latency" | "success_rate" | "combined"
}

// RetryYAML 保存指数退避重试配置（YAML 映射）。
type RetryYAML struct {
	MaxRetries int           `yaml:"max_retries"` // 最大重试次数
	BaseDelay  time.Duration `yaml:"base_delay"`  // 重试间的初始延迟
	MaxDelay   time.Duration `yaml:"max_delay"`   // 重试间的最大延迟
	Multiplier float64       `yaml:"multiplier"`  // 指数退避乘数
}

// PrivacyYAML 保存隐私网关配置（YAML 映射）。
type PrivacyYAML struct {
	Enabled    bool   `yaml:"enabled"`     // 是否启用隐私网关
	Endpoint   string `yaml:"endpoint"`    // 隐私模型服务端点
	LBStrategy string `yaml:"lb_strategy"` // 负载均衡策略
	FailOpen   bool   `yaml:"fail_open"`   // 隐私服务故障时是否放行
	Policy     string `yaml:"policy"`      // 隐私策略名称
}

// StorageYAML 保存持久化存储配置（YAML 映射）。
type StorageYAML struct {
	Driver string `yaml:"driver"` // 存储驱动："sqlite" | "mysql" | "postgres"
	DSN    string `yaml:"dsn"`    // 数据源名称（连接字符串）
}

// HealthCheckYAML 保存健康检查配置（YAML 映射）。
type HealthCheckYAML struct {
	Interval time.Duration `yaml:"interval"` // 健康检查间隔
}

// HistoryYAML 保存历史记录配置（YAML 映射）。
type HistoryYAML struct {
	RingSize int  `yaml:"ring_size"` // 环形缓冲区容量
	Persist  bool `yaml:"persist"`   // 是否持久化到存储后端
}

// AoEoConfig 保存完整的网关顶层配置，对应 YAML 文件的根结构。
type AoEoConfig struct {
	Server      ServerConfig    `yaml:"server"`       // HTTP 服务器配置
	Providers   []ProviderYAML  `yaml:"providers"`    // Provider 列表
	Router      RouterConfig    `yaml:"router"`       // 路由策略配置
	Retry       RetryYAML       `yaml:"retry"`        // 重试策略配置
	Privacy     PrivacyYAML     `yaml:"privacy"`      // 隐私网关配置
	Storage     StorageYAML     `yaml:"storage"`      // 持久化存储配置
	HealthCheck HealthCheckYAML `yaml:"health_check"` // 健康检查配置
	History     HistoryYAML     `yaml:"history"`      // 历史记录配置
}

// envVarRegexp 匹配 ${VAR_NAME} 和 ${VAR_NAME:-default} 两种环境变量模式。
var envVarRegexp = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-((?:[^}\\]|\\.)*))?\}`)

// expandEnvVars 替换 YAML 内容中的 ${VAR_NAME} 和 ${VAR_NAME:-default} 模式。
//
// 当环境变量存在时，使用其值替换占位符；当环境变量不存在且指定了默认值时，
// 使用默认值替换；否则保留空字符串。
//
// Param:
//   - data: []byte - 原始 YAML 内容
//
// Return:
//   - []byte: 替换环境变量后的 YAML 内容
func expandEnvVars(data []byte) []byte {
	return envVarRegexp.ReplaceAllFunc(data, func(match []byte) []byte {
		submatches := envVarRegexp.FindSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		varName := string(submatches[1])
		defaultVal := ""
		if len(submatches) >= 3 {
			defaultVal = string(submatches[2])
		}
		val := os.Getenv(varName)
		if val == "" && defaultVal != "" {
			return []byte(defaultVal)
		}
		return []byte(val)
	})
}

// LoadConfig 从指定路径加载 YAML 配置文件并返回解析后的 AoEoConfig。
//
// 加载流程：读取文件 -> 环境变量替换 -> YAML 解析 -> 填充默认值 -> 校验。
//
// Param:
//   - path: string - YAML 配置文件路径
//
// Return:
//   - *AoEoConfig: 解析完成的配置对象
//   - error: 文件读取、YAML 解析或校验失败时返回
func LoadConfig(path string) (*AoEoConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: 读取配置文件失败: %w", err)
	}

	// 环境变量替换
	data = expandEnvVars(data)

	var cfg AoEoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: 解析 YAML 失败: %w", err)
	}

	// 填充默认值
	cfg.ApplyDefaults()

	// 校验配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: 配置校验失败: %w", err)
	}

	return &cfg, nil
}

// ApplyDefaults 为 AoEoConfig 中未设置的字段填充合理的默认值。
//
// 默认值列表：
//   - Server.Addr = ":8081"
//   - Server.ReadTimeout = 120s
//   - Server.WriteTimeout = 120s
//   - HealthCheck.Interval = 30s
//   - History.RingSize = 1000
//   - Retry.MaxRetries = 2, BaseDelay = 500ms, MaxDelay = 5s, Multiplier = 2.0
func (c *AoEoConfig) ApplyDefaults() {
	// 服务器默认值
	if c.Server.Addr == "" {
		c.Server.Addr = ":8081"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 120 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 120 * time.Second
	}

	// 健康检查默认值
	if c.HealthCheck.Interval == 0 {
		c.HealthCheck.Interval = 30 * time.Second
	}

	// 历史记录默认值
	if c.History.RingSize == 0 {
		c.History.RingSize = 1000
	}

	// 重试策略默认值
	if c.Retry.MaxRetries == 0 {
		c.Retry.MaxRetries = 2
	}
	if c.Retry.BaseDelay == 0 {
		c.Retry.BaseDelay = 500 * time.Millisecond
	}
	if c.Retry.MaxDelay == 0 {
		c.Retry.MaxDelay = 5 * time.Second
	}
	if c.Retry.Multiplier == 0 {
		c.Retry.Multiplier = 2.0
	}
}

// Validate 校验 AoEoConfig 中的关键配置项是否合法。
//
// 检查内容包括：
//   - 至少配置一个 Provider
//   - 每个 Provider 的 Name、APIKey、Endpoint、Model 为必填
//   - Endpoint 必须以 http:// 或 https:// 开头
//   - Server.Addr 不能为空
//
// Return:
//   - error: 校验失败时返回包含所有错误的聚合错误
func (c *AoEoConfig) Validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("config: 至少需要配置一个 provider")
	}

	if c.Server.Addr == "" {
		return fmt.Errorf("config: server.addr 不能为空")
	}

	for i, p := range c.Providers {
		if p.Name == "" {
			return fmt.Errorf("config: providers[%d].name 不能为空", i)
		}
		if p.APIKey == "" {
			return fmt.Errorf("config: providers[%d].api_key 不能为空", i)
		}
		if p.Endpoint == "" {
			return fmt.Errorf("config: providers[%d].endpoint 不能为空", i)
		}
		if p.Model == "" {
			return fmt.Errorf("config: providers[%d].model 不能为空", i)
		}
		// 利用 core.ValidateConfig 做更细粒度的 URL 校验
		coreCfg := c.providerToCore(p)
		if issues := core.ValidateConfig(coreCfg); len(issues) > 0 {
			return fmt.Errorf("config: providers[%d] (%s) 配置错误: %v", i, p.Name, issues)
		}
	}

	return nil
}

// providerToCore 将单个 ProviderYAML 转换为 core.ProviderConfig。
//
// Param:
//   - p: ProviderYAML - YAML 格式的 Provider 配置
//
// Return:
//   - core.ProviderConfig: SDK 核心可使用的 Provider 配置
func (c *AoEoConfig) providerToCore(p ProviderYAML) core.ProviderConfig {
	cfg := core.ProviderConfig{
		Name:             p.Name,
		APIKey:           p.APIKey,
		Endpoint:         p.Endpoint,
		Model:            p.Model,
		MaxConcurrent:    p.MaxConcurrent,
		SkipTLSVerify:    p.SkipTLSVerify,
		MaxFailures:      p.MaxFailures,
		CooldownDuration: p.CooldownDuration,
		Proxy:            p.Proxy,
		Pricing: core.Pricing{
			PromptPer1K:     p.Pricing.PromptPer1K,
			CompletionPer1K: p.Pricing.CompletionPer1K,
			Currency:        p.Pricing.Currency,
		},
	}
	return cfg
}

// ToCoreConfig 将 AoEoConfig 转换为 core.Config，供 SDK 引擎直接使用。
//
// 所有 ProviderYAML 会被逐一映射为 core.ProviderConfig，包括 Pricing 字段。
// AuditEnabled 固定为 true（服务端模式默认启用审计）。
//
// Return:
//   - core.Config: SDK 引擎可消费的全局配置
func (c *AoEoConfig) ToCoreConfig() core.Config {
	providers := make([]core.ProviderConfig, 0, len(c.Providers))
	for _, p := range c.Providers {
		providers = append(providers, c.providerToCore(p))
	}

	return core.Config{
		Providers:    providers,
		AuditEnabled: true,
	}
}

// BuildRouter 根据 RouterConfig.Strategy 构建对应的 Router 实例。
//
// 支持的策略：
//   - "round-robin" -> core.RoundRobinRouter（轮询）
//   - "random" -> core.RandomRouter（随机）
//   - "weighted" -> core.WeightedRouter（加权评分）
//   - "primary" 或其他 -> core.PrimaryRouter（主备，默认）
//
// 当策略为 "weighted" 时，WeightStrategy 控制评分算法：
//   - "latency" -> core.StrategyLatency
//   - "success_rate" -> core.StrategySuccessRate
//   - "combined" 或其他 -> core.StrategyCombined
//
// Return:
//   - core.Router: 构造好的路由器实例
func (c *AoEoConfig) BuildRouter() core.Router {
	switch c.Router.Strategy {
	case "round-robin":
		return &core.RoundRobinRouter{}
	case "random":
		return &core.RandomRouter{}
	case "weighted":
		ws := c.mapWeightStrategy()
		return &core.WeightedRouter{Strategy: ws}
	case "primary":
		return &core.PrimaryRouter{}
	default:
		// 默认使用主备路由
		return &core.PrimaryRouter{}
	}
}

// mapWeightStrategy 将 YAML 中的加权策略字符串映射为 core.WeightStrategy 常量。
//
// Return:
//   - core.WeightStrategy: 对应的加权评分策略枚举值
func (c *AoEoConfig) mapWeightStrategy() core.WeightStrategy {
	switch c.Router.WeightStrategy {
	case "latency":
		return core.StrategyLatency
	case "success_rate":
		return core.StrategySuccessRate
	case "combined":
		return core.StrategyCombined
	default:
		return core.StrategyCombined
	}
}
