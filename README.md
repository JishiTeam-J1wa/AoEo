# AoEo

> **一个用于聚合调度多 AI API 的 Go SDK**

AoEo 让你在代码中用统一的接口调用多个大模型 API（DeepSeek、Kimi、GPT、GLM、Qwen 等），并自动处理故障转移、并发限流、熔断降级、成本统计和 Prompt 注入。

无需为每个平台写不同的调用代码，也无需自己实现重试和容灾逻辑。

---

## 用 AoEo 可以做什么

| 场景 | 你能做的 |
|---|---|
| **多模型统一调用** | 一条 `ChatComplete` 请求，自动路由到可用 Provider |
| **可插拔路由策略** | 内置 Primary / Round-Robin / Random 路由，支持自定义策略 |
| **故障自动转移** | 主 API 失败时，自动切到备用 API，业务无感知 |
| **双模型交叉验证** | 同一请求并发发给两个模型，对比结果一致性 |
| **流式响应** | SSE 逐字返回，支持流式拦截器实时处理 chunk |
| **Prompt 统一管理** | 按 Provider/模型通配注入系统提示，无需改动业务代码 |
| **成本透明化** | 每次调用自动计算 Token 成本，按 Provider 聚合统计 |
| **后台健康检查** | 定期探测 Provider 可用性，自动触发熔断/恢复 |
| **隐私安全网关** | 本地 PII 检测 + 规则过滤，敏感数据出网前自动替换伪造值 |
| **生产级稳定性** | 熔断器 + 自适应限流 + Panic 恢复 + 优雅关闭 |

---

## 快速开始

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    aoeo "github.com/JishiTeam-J1wa/AoEo"
)

func main() {
    client, err := aoeo.NewClient(aoeo.Config{
        Providers: []aoeo.ProviderConfig{
            {
                Name:     "deepseek",
                APIKey:   os.Getenv("DEEPSEEK_API_KEY"),
                Endpoint: "https://api.deepseek.com",
                Model:    "deepseek-v4-pro",
            },
            {
                Name:     "kimi",
                APIKey:   os.Getenv("KIMI_API_KEY"),
                Endpoint: "https://api.moonshot.cn/v1",
                Model:    "kimi-k2.6",
            },
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // 基础调用：使用第一个可用 Provider
    resp, err := client.ChatComplete(context.Background(),
        aoeo.BuildRequest(
            []aoeo.Message{{Role: "user", Content: "Hello, world!"}},
            aoeo.WithTemperature(0.7),
        ),
    )
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(resp.Content())
}
```

---

## 安装

```bash
go get github.com/JishiTeam-J1wa/AoEo
```

Go 版本要求：≥ 1.22

---

## 核心能力详解

### 1. 多 Provider 聚合调度

配置多个 Provider，AoEo 自动管理它们的生命周期：

```go
cfg := aoeo.Config{
    Providers: []aoeo.ProviderConfig{
        {Name: "deepseek", APIKey: "sk-xxx", Endpoint: "...", Model: "...", MaxConcurrent: 5},
        {Name: "kimi",     APIKey: "sk-xxx", Endpoint: "...", Model: "...", MaxConcurrent: 5},
        {Name: "gpt",      APIKey: "sk-xxx", Endpoint: "...", Model: "...", MaxConcurrent: 5},
    },
}
```

- **Primary 模式**：始终使用第一个可用 Provider
- **Round-Robin**：Dual / Audit 模式下自动负载均衡
- **自适应并发限流**：总容量 = Σ(MaxConcurrent)，FIFO 队列防止 goroutine 爆炸

### 2. 自动故障转移（Fallback）

主 Provider 失败后自动尝试下一个，直到成功或全部失败：

```go
resp, err := client.ChatCompleteWithFallback(ctx, req)
// resp.Content() 安全访问第一个 Choice 的内容（自动处理 nil / 空 Choices）
```

每次尝试前获取信号量许可，失败时释放。适合生产环境的高可用场景。

### 3. 双模型并发验证（Dual）

同一请求并发发给两个不同 Provider，对比结果一致性：

```go
dual, err := client.ChatCompleteDual(ctx, req)
fmt.Printf("Consensus: %v\n", dual.Consensus)
// dual.Result1.Content() 和 dual.Result2.Content() 安全访问内容
```

### 4. 审计模式（Audit）

串行调用两个 Provider 进行交叉验证，适合对结果可信度要求极高的场景：

```go
result, err := client.Audit(ctx, req)
if !result.Consensus {
    fmt.Println("⚠️ 结果不一致，建议人工复核")
}
// result.Primary.Content() / result.Audit.Content() 安全访问
```

### 5. SSE 流式响应

```go
stream, err := client.ChatCompleteStream(ctx, req)
for chunk := range stream {
    if chunk.Err != nil {
        log.Fatal(chunk.Err)
    }
    if chunk.Chunk.FinishReason != "" {
        // 最终 chunk 可能携带 Usage（若 Provider 支持）
        fmt.Printf("Tokens: %d total\n", chunk.Usage.TotalTokens)
        break
    }
    fmt.Print(chunk.Chunk.Delta.Content)
}
```

### 6. Prompt 注入引擎

按 Provider/Model 通配匹配，自动注入模板，无需改动业务代码：

```go
pi := aoeo.NewPromptInjector()

// 所有 Provider 统一系统提示
pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "*",
    Model:    "*",
    Position: "system",
    Content:  "You are a helpful assistant.",
})

// DeepSeek 专用编程提示
pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "deepseek",
    Position: "system",
    Content:  "You are an expert programmer.",
})

client.SetPromptInjector(pi)
```

支持三个注入位置：`system`、`prepend_user`、`append_user`。支持 `{{var}}` 变量替换。

### 7. 计费统计与成本追踪

```go
// 配置单价
cfg.Providers[0].Pricing = aoeo.Pricing{
    PromptPer1K:     2.0,
    CompletionPer1K: 8.0,
    Currency:        "CNY",
}

// 每次调用自动计算成本
resp, _ := client.ChatComplete(ctx, req)
cost := resp.Usage.Cost(pricing)  // 0.38 CNY

// 安全访问内容（自动处理 nil / 空 Choices）
content := resp.Content()
_ = content

// 按 Provider 聚合统计
for name, s := range client.Stats() {
    fmt.Printf("%s: %d calls, %.4f %s total\n", name, s.TotalCalls, s.TotalCost, s.Currency)
}
```

内置默认价格：DeepSeek 2/8 CNY、Kimi 3/12 CNY、GLM 5/5 CNY、Qwen 5/10 CNY。

### 8. 熔断器与重试

每个 Provider 独立维护熔断状态：
- 连续 3 次失败 → 开启熔断，冷却 60 秒
- 成功后自动重置计数
- 指数退避重试，自动识别超时/502/503/504/限流等可重试错误

### 9. 事件系统

监听 Provider 生命周期事件：

```go
client.SetEmitter(&MyEmitter{})

// 内置事件：
// provider:fail    — Provider 调用失败
// provider:open    — 熔断器开启
// provider:recover — 熔断恢复
// scheduler:fallback — Fallback 触发
// audit:disagree   — 审计发现分歧
```

### 10. 定向 AI API 代理

每个 Provider 可独立配置代理，支持 HTTP 代理 URL、环境变量 `HTTP_PROXY` 和 SOCKS5 URL：

```go
cfg := aoeo.Config{
    Providers: []aoeo.ProviderConfig{
        {
            Name:  "kimi",
            Proxy: "http://proxy-a1.example.com:8080", // Kimi 走代理 A1
        },
        {
            Name:  "gpt",
            Proxy: "socks5://127.0.0.1:1080", // GPT 走 SOCKS5
        },
        {
            Name:  "deepseek",
            Proxy: "", // 空值则遵循系统环境变量 HTTP_PROXY
        },
    },
}
```

适用场景：
- 不同 Provider 的网络出口隔离
- 内网环境通过不同代理访问外网
- 按 Provider 走不同线路优化延迟

### 11. 拦截器机制

`core.Interceptor` 提供 `BeforeRequest` / `AfterResponse` / `AfterStreamChunk` / `AfterStreamDone` Hook，用于日志、监控、限流、请求篡改、流式数据处理等横切关注点：

```go
ic := aoeo.Interceptor{
    BeforeRequest: func(ctx context.Context, req *aoeo.ChatCompletionRequest) error {
        // 例如：注入 Trace ID、校验预算
        req.Tags = append(req.Tags, "trace:xyz")
        return nil
    },
    AfterResponse: func(ctx context.Context, req aoeo.ChatCompletionRequest, resp *aoeo.ChatCompletionResponse, err error) (*aoeo.ChatCompletionResponse, error) {
        // 例如：记录延迟、统一错误包装
        return resp, err
    },
    AfterStreamChunk: func(ctx context.Context, req aoeo.ChatCompletionRequest, chunk *aoeo.StreamChunk) error {
        // 例如：实时敏感词过滤
        return nil
    },
    AfterStreamDone: func(ctx context.Context, req aoeo.ChatCompletionRequest, err error) error {
        // 例如：记录流式会话总耗时
        return nil
    },
}

client, _ := aoeo.NewClient(cfg, aoeo.WithInterceptors(ic))
```

- 多个拦截器按顺序执行，`BeforeRequest` / `AfterStreamChunk` 遇到错误立即中断
- `AfterResponse` / `AfterStreamDone` 可转换响应或错误，对调用方透明
- 实现必须线程安全

### 12. 路由策略

支持可插拔的路由策略，替代默认的「第一个可用 Provider」逻辑：

```go
// 轮询（Round-Robin）
client.SetRouter(&aoeo.RoundRobinRouter{})

// 随机（Random）
client.SetRouter(&aoeo.RandomRouter{})

// 自定义路由
client.SetRouter(&myCustomRouter{})
```

内置策略：

| 策略 | 行为 | 适用场景 |
|---|---|---|
| `PrimaryRouter` | 选第一个可用 Provider | 默认行为，主备架构 |
| `RoundRobinRouter` | 轮询可用 Provider | 均匀负载 |
| `RandomRouter` | 随机选可用 Provider | 简单负载分散 |

路由策略影响 `ChatComplete`、`ChatCompleteWithFallback` 的 Provider 选择顺序，以及 `ChatCompleteDual` 的配对策略。

### 13. 后台健康检查

Scheduler 可启动后台 goroutine，定期对所有 Provider 执行轻量级健康探测（默认关闭）：

```go
// 每 30 秒检查一次
s := aoeo.NewSchedulerWithOptions(providers, aoeo.WithHealthCheckInterval(30*time.Second))

// 或运行时动态调整
client.SetHealthCheckInterval(30 * time.Second)
client.SetHealthCheckInterval(0) // 关闭健康检查
```

- 健康检查通过 HTTP GET 探测 Provider Endpoint（5 秒超时）
- 检查失败会触发熔断器的 `RecordFailure`，加速 Provider 进入冷却
- 检查成功会触发 `RecordSuccess`，帮助 Provider 从冷却中恢复
- 关闭 Scheduler 时会自动停止后台健康检查 goroutine

### 14. 环境变量配置

无需硬编码，直接从 `AOEO_PROVIDER_N_*` 环境变量加载配置：

```bash
export AOEO_PROVIDER_0_NAME=deepseek
export AOEO_PROVIDER_0_API_KEY=sk-xxx
export AOEO_PROVIDER_0_ENDPOINT=https://api.deepseek.com
export AOEO_PROVIDER_0_MODEL=deepseek-v4-pro
export AOEO_PROVIDER_0_PROXY=http://proxy.example.com:8080

export AOEO_PROVIDER_1_NAME=kimi
export AOEO_PROVIDER_1_API_KEY=sk-yyy
export AOEO_PROVIDER_1_ENDPOINT=https://api.moonshot.cn/v1
export AOEO_PROVIDER_1_MODEL=kimi-k2.6
```

```go
cfg := aoeo.LoadConfigFromEnv()
client, err := aoeo.NewClient(cfg)
```

还支持自定义前缀：`LoadConfigFromEnvWithPrefix("MYAPP")` 读取 `MYAPP_PROVIDER_0_NAME` 等变量。

### 15. 自定义 HTTPClient

为单个 Provider 注入自定义 `*http.Client`，用于链路追踪、Mock 测试或特殊 TLS 配置：

```go
httpClient := &http.Client{
    Timeout: 30 * time.Second,
    Transport: &http.Transport{
        TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
    },
}

cfg := aoeo.Config{
    Providers: []aoeo.ProviderConfig{
        {
            Name:       "deepseek",
            HTTPClient: httpClient, // 完全自定义 HTTP 行为
        },
    },
}
```

优先级：自定义 `HTTPClient` > `Proxy` 字段 > 环境变量 `HTTP_PROXY`。

### 16. 隐私安全网关

AoEo 内置可逆隐私网关，确保敏感信息（PII、内网 IP、内部域名、身份证号等）在出网前被替换为伪造值，AI 响应返回时自动还原：

```go
// 加载本地规则库
rules, _ := privacy.LoadRuleDatabase("privacy_rules.yaml")

// 创建隐私网关
gateway, _ := privacy.NewGateway(privacy.GatewayConfig{
    Rules:  privacy.NewRuleEngine(rules),
    Policy: privacy.ActionPseudonymize,
})

// 接入 AoEo
client, _ := aoeo.NewClient(cfg, aoeo.WithInterceptors(gateway.ToInterceptor()))
```

**双层检测**：
- **本地规则引擎**：IP 黑白名单 / CIDR 网段、域名过滤、关键词、正则匹配
- **Privacy Filter 模型**：OpenAI Privacy Filter 本地模型检测姓名、电话、身份证、密钥等 PII

**处理策略**：
| 策略 | 行为 | 适用场景 |
|---|---|---|
| `block` | 检测到敏感数据直接阻断请求 | 高安全环境 |
| `mask` | 替换为 `[REDACTED]` | 审计日志 |
| `pseudonymize` | 替换为逼真的伪造值，返回时自动还原 | **生产推荐** |
| `audit` | 放行但记录审计日志 | 灰度观察 |

**完整使用手册**：见 [PRIVACY_GATEWAY.md](./PRIVACY_GATEWAY.md)

---

## 支持的 Provider

| Provider | 工厂函数 | 默认端点 | 默认模型 | 内置价格 (Prompt/Completion) |
|---|---|---|---|---|
| DeepSeek | `NewDeepSeekProvider()` | `https://api.deepseek.com` | `deepseek-v4-pro` | 2 / 8 CNY |
| Kimi (Moonshot) | `NewKimiProvider()` | `https://api.moonshot.cn/v1` | `kimi-k2.6` | 3 / 12 CNY |
| GLM (智谱) | `NewGLMProvider()` | `https://open.bigmodel.cn/api/paas/v4` | `glm-5.1` | 5 / 5 CNY |
| Qwen (通义) | `NewQwenProvider()` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.7-max` | 5 / 10 CNY |
| 任意 OpenAI-compatible | `NewOpenAIProvider()` | *(从配置)* | *(从配置)* | *(自定义)* |

---

## 生产建议

1. **API Key 管理**：通过环境变量注入，不要硬编码
2. **并发上限**：根据各平台 RPM/TPM 限制设置 `MaxConcurrent`
3. **Stream 退出**：消费端提前 break 时，应同时 `cancel()` context
4. **Graceful Shutdown**：始终 `defer client.Close()`
5. **日志级别**：生产环境建议设置 `slog.LevelWarn`
6. **请求预验证**：发送前调用 `req.Validate()` 提前拦截参数错误
7. **Nil 安全访问**：优先使用 `resp.Content()` 代替 `resp.Choices[0].Message.Content`

---

## 项目结构

```
AoEo/
├── client.go          # 用户入口，fluent API + 类型别名
├── options.go         # 请求构建器
├── go.mod
├── core/              # 公共类型和工具
│   ├── types.go       # 统一请求/响应类型
│   ├── config.go      # 配置验证
│   ├── pricing.go     # 价格模型 + 成本计算
│   ├── retry.go       # 重试配置
│   ├── interceptor.go # 拦截器链（含流式拦截）
│   ├── router.go      # 可插拔路由策略接口
│   ├── event.go       # 事件系统
│   └── logger.go      # 结构化日志
├── providers/         # Provider 接口和实现
│   └── providers.go   # Provider 接口 + BaseProvider + OpenAI + 内置 Provider
├── privacy/           # 隐私安全网关
│   ├── types.go           # 类型定义（EntityType, Span, Mapping）
│   ├── store.go           # SQLite 映射表存储
│   ├── generator.go       # 伪造数据生成器
│   ├── detector.go        # 检测器接口
│   ├── rules.go           # 本地规则引擎（IP/域名/关键词/正则）
│   ├── pseudonymizer.go   # 核心伪匿名化器（检测→替换→回溯）
│   └── gateway.go         # AoEo Interceptor 集成
├── internal/          # 内部实现
│   └── engine/
│       ├── scheduler.go   # 调度核心 + 选项
│       ├── history.go     # 调用历史 + 统计聚合
│       ├── prompt.go      # Prompt 注入引擎
│       ├── stream.go      # SSE 流式支持
│       ├── audit.go       # 审计模式
│       ├── result.go      # 结果处理 + JSON 提取
│       ├── semaphore.go   # 自适应并发限流
│       └── retry_impl.go  # 指数退避重试实现
├── examples/
│   ├── basic/
│   ├── multi_provider/
│   ├── streaming/
│   └── list_models/
├── README.md
├── DESIGN.md
├── AUDIT_REPORT.md
├── LICENSE
└── aoeo_test.go       # 230 个单元测试（含 Race Detector），整体覆盖率 71.7%
```

---

## 后续更新计划

### Phase 2 — 生产增强（已完成）
- [x] 指数退避重试
- [x] Token 用量追踪与成本估算
- [x] Prompt 注入系统
- [x] 结构化日志
- [x] 优雅关闭
- [x] 全路径 panic 恢复 + 信号量防泄漏
- [x] 请求预验证 `Validate()`
- [x] 安全访问器 `Content()`
- [x] Stream Usage 透传

### Phase 3 — 网络与可观测性增强（已完成）
- [x] **定向 AI API 代理**
- [x] **拦截器机制（BeforeRequest / AfterResponse）**
- [x] **环境变量配置（`LoadConfigFromEnv`）**
- [x] **自定义 HTTPClient**

### Phase 3.5 — 架构偿债（已完成）
- [x] **流式架构重构**：Provider 接口新增 `ChatCompleteStream`，解耦 `OpenAIProvider` 类型断言
- [x] **流式 interceptor 支持**：`BeforeRequest` 在流式路径生效
- [x] **流式 goroutine 泄漏修复**：固定缓冲 channel + 安全 select 退出
- [x] **测试补齐**：流式 8 个 + buildRecord 5 个 + Client 18 个 + Retry Validate
- [x] **覆盖率提升**：整体 66.6% → 71.7%，根包 52.4% → 84.5%

### Phase 4 — 生态扩展
- [ ] 权重路由（按价格/延迟/质量加权选择 Provider）
- [ ] Provider 主动健康检查心跳
- [ ] Function Calling 抽象层
- [ ] CLI 工具（`aoeo list-models`, `aoeo test`）

---

## 许可证

MIT License. See [LICENSE](LICENSE) for details.

> AoEo = "Aggregation of Everything Open" —— 聚合一切 OpenAI-compatible 的模型服务。
