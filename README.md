# AoEo

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue)](https://golang.org)
[![Go Report Card](https://g.shields.io/badge/go%20report-A+-brightgreen.svg)](https://goreportcard.com)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

> **生产级多 Provider AI API 网关 —— 统一接入、智能路由、自动容灾、隐私保护**

## 它解决什么问题

你在用 DeepSeek、Kimi、GPT、GLM、Qwen 等多家 AI 服务时，是否遇到：

- **单点故障**：主 API 挂了，服务直接不可用
- **代码碎片**：每家 API 格式不同，切换要改一堆代码
- **限流熔断**：不知道哪家会突然限流，缺乏自动降级
- **成本黑盒**：各平台计费不同，算不清花了多少
- **数据泄露**：请求里带身份证号、手机号，直接发给第三方

**AoEo 是一个 Go 库，让你用同一套代码同时接入多家 AI，自动处理路由、故障转移、限流熔断、成本统计，并可选隐私网关——敏感数据出网前自动替换，返回时还原，用户无感知。**

```
你的代码 ──→ AoEo ──┬──→ DeepSeek (主)
                   ├──→ Kimi (备用)
                   ├──→ GPT (备用)
                   └──→ 隐私网关 (出网前脱敏)
```

---

## 核心能力一览

| 痛点 | AoEo 的解法 |
|---|---|
| 多平台 API 不统一 | **统一接口**：一条 `ChatComplete` 调用任意 OpenAI-compatible Provider |
| 单点故障 | **自动故障转移**：主 API 失败自动切备用，业务无感知 |
| 不知道用哪家 | **可插拔路由**：Primary / Round-Robin / Random / 自定义策略 |
| 结果不可信 | **双模型验证**：同一请求并发发给两家，对比结果一致性 |
| 流式输出难处理 | **SSE 流式**：逐字返回，支持流式拦截器实时处理 |
| Prompt 散落各处 | **统一注入**：按 Provider/模型通配注入系统提示，零业务侵入 |
| 成本算不清 | **自动计费**：每次调用算 Token 成本，按 Provider 聚合 |
| 服务状态不明 | **健康探测**：后台定期探测，自动熔断/恢复 |
| 路由不智能 | **权重路由**：按延迟/成功率加权选择，自动避开慢节点 |
| 需要 Tool Calling | **Function Calling**：统一 Tools/ToolCalls 抽象，兼容各家实现 |
| 敏感数据泄露 | **隐私网关**：本地规则 + PII 检测，出网前替换伪造值 |
| 生产不稳定 | **熔断 + 限流 + Panic 恢复 + 优雅关闭** |

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

### 带隐私网关的调用（1 行接入）

```bash
export AOEO_PRIVACY_ENABLED=true
export AOEO_PRIVACY_ENDPOINT=http://localhost:8080
```

```go
client, _ := aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
```

敏感信息（手机号、身份证号、内网 IP）出网前自动替换为伪造值，AI 响应返回时自动还原。支持多台 Sidecar 横向扩容 + 智能路由，见 [PRIVACY_GATEWAY.md](./PRIVACY_GATEWAY.md)。

---

## 安装

```bash
go get github.com/JishiTeam-J1wa/AoEo
```

Go 版本要求：≥ 1.25（`modernc.org/sqlite` 等依赖要求）

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

AoEo 内置可逆隐私网关，确保敏感信息（PII、内网 IP、内部域名、身份证号等）在出网前被替换为伪造值，AI 响应返回时自动还原。支持 **批量检测**、**多 Sidecar 智能路由**、**HTTP/2**、**连接预热**。

#### 一行接入（环境变量自动配置）

```bash
export AOEO_PRIVACY_ENABLED=true
export AOEO_PRIVACY_ENDPOINT=http://localhost:8080
```

```go
import "github.com/JishiTeam-J1wa/AoEo/privacy"

client, _ := aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
```

#### 多实例 + 智能路由（LeastLatency）

```go
gw, _ := privacy.NewGateway(privacy.GatewayConfig{
    // 逗号分隔多地址，Go 端自动做负载均衡
    ModelEndpoint: "http://sidecar-1:8080,http://sidecar-2:8080,http://sidecar-3:8080",
    LBStrategy:    model.LeastLatency, // 自动路由到延迟最低的节点
    Policy:        privacy.ActionPseudonymize,
    FailOpen:      true, // sidecar 全部故障时透传请求
})
client, _ := aoeo.NewClient(cfg, aoeo.WithInterceptors(gw.ToInterceptor()))
```

**批量检测**：请求含多条 message 时，自动合并为一次 `DetectBatch` 调用，N 条降为 1 次 HTTP 往返。

**负载均衡策略**：
| 策略 | 行为 | 适用场景 |
|---|---|---|
| `RoundRobin` | 轮询分发 | 均匀负载 |
| `Random` | 随机分发 | 简单分散 |
| `LeastLatency` | EWMA 延迟加权，自动路由到最快节点 | **生产推荐** |

**处理策略**：
| 策略 | 行为 | 适用场景 |
|---|---|---|
| `block` | 检测到敏感数据直接阻断请求 | 高安全环境 |
| `mask` | 替换为 `[REDACTED]` | 审计日志 |
| `pseudonymize` | 替换为逼真的伪造值，返回时自动还原 | **生产推荐** |
| `audit` | 放行但记录审计日志 | 灰度观察 |

**完整使用手册**：见 [PRIVACY_GATEWAY.md](./PRIVACY_GATEWAY.md)

### 17. Function Calling

统一 Tool/Function 抽象，兼容所有 OpenAI-compatible Provider：

```go
req := aoeo.BuildRequest(
    []aoeo.Message{{Role: "user", Content: "What's the weather in Beijing?"}},
    aoeo.WithTools([]aoeo.Tool{
        {
            Type: "function",
            Function: &aoeo.FunctionDefinition{
                Name:        "get_weather",
                Description: "Get current weather for a city",
                Parameters:  map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
            },
        },
    }),
    aoeo.WithToolChoice("auto"),
)

resp, _ := client.ChatComplete(ctx, req)
if len(resp.Choices[0].Message.ToolCalls) > 0 {
    tc := resp.Choices[0].Message.ToolCalls[0]
    fmt.Printf("Model wants to call %s with args: %s\n", tc.Function.Name, tc.Function.Arguments)
}
```

- 完整的 `Tool`/`ToolCall`/`FunctionDefinition` 类型，支持流式 ToolCalls
- 双向映射：core 类型 ↔ go-openai 类型，不丢失任何字段
- `WithTools` / `WithToolChoice` / `WithParallelToolCalls` builder 选项

### 18. 权重路由

根据 Provider 实时健康指标智能选择：

```go
// 按延迟加权：延迟越低，被选中的概率越高
client.SetRouter(&aoeo.WeightedRouter{Strategy: aoeo.StrategyLatency})

// 按成功率加权
client.SetRouter(&aoeo.WeightedRouter{Strategy: aoeo.StrategySuccessRate})

// 综合策略：延迟 50% + 成功率 50%
client.SetRouter(&aoeo.WeightedRouter{Strategy: aoeo.StrategyCombined})
```

- 基于 20 次调用滑动窗口的实时健康数据
- 支持 `SelectSequence`：按得分降序排列，用于 fallback
- 可配合 `SingleProviderRouter` 实现 CLI 定向调用

### 19. 持久化存储后端

AoEo 提供统一的 `core.Storage` 接口，支持将调用历史、审计日志、隐私映射表持久化到数据库：

```go
// SQLite（默认，零配置，单机部署推荐）
store, _ := storage.NewSQLite("./aoeo.db")

// MySQL（远程/集群部署）
store, _ := storage.NewMySQL("user:pass@tcp(localhost:3306)/aoeo?charset=utf8mb4")

// PostgreSQL
store, _ := storage.NewPostgres("postgres://user:pass@localhost:5432/aoeo?sslmode=disable")

// 接入 History（调用历史持久化）
history := engine.NewHistory(100)
history.SetStorage(store)

// 接入 Privacy Gateway（Pebble KV 映射表持久化）
gateway, _ := privacy.NewGateway(privacy.GatewayConfig{
    ModelEndpoint: "http://localhost:8080",
    Policy:        privacy.ActionPseudonymize,
})
```

**支持的存储**：SQLite（纯 Go）/ MySQL / PostgreSQL，统一 Schema：calls、audits、privacy_mappings 三张表。

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
│   ├── detector.go        # 检测器接口（含 DetectBatch）
│   ├── model_adapter.go   # model.Client → Detector 适配器
│   ├── generator.go       # 伪造数据生成器
│   ├── pseudonymizer.go   # 核心伪匿名化器（批量检测→替换→回溯）
│   ├── gateway.go         # AoEo Interceptor 集成
│   ├── option.go          # WithPrivacyFilter() / WithPrivacyModel()
│   ├── store/             # Pebble KV 映射存储
│   │   └── pebble.go
│   └── model/             # Sidecar HTTP 客户端
│       ├── client.go      # Client 接口（Detect / DetectBatch / HealthCheck）
│       ├── http.go        # HTTP/JSON 客户端（HTTP/2 + 连接池 + 批量）
│       └── loadbalancer.go # 多后端负载均衡（LeastLatency + 健康检查 + 预热）
├── storage/           # 持久化存储后端（SQLite / MySQL / Postgres）
│   ├── base.go            # 公共 SQL CRUD 逻辑
│   ├── sqlite.go          # SQLite 后端（纯 Go，零 CGO）
│   ├── mysql.go           # MySQL 后端
│   └── postgres.go        # PostgreSQL 后端
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
├── cmd/
│   └── aoeo/
│       └── main.go      # CLI 工具（list-models / test / status / chat / stream）
├── examples/
│   ├── basic/
│   ├── multi_provider/
│   ├── streaming/
│   └── list_models/
├── README.md
├── DESIGN.md
├── AUDIT_REPORT.md
├── LICENSE
└── aoeo_test.go       # 300+ 个单元测试，`-race` / `go vet` 全绿
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

### Phase 4 — 生态扩展（已完成）
- [x] **权重路由**：按延迟/成功率加权选择 Provider（`WeightedRouter`）
- [x] **Provider 主动健康检查心跳**：20 次滑动窗口，实时追踪延迟/成功率/连续失败
- [x] **Function Calling 抽象层**：统一 `Tool`/`ToolCall`/`FunctionDefinition` 类型，Provider 双向映射
- [x] **CLI 工具**：`aoeo list-models` / `test` / `status` / `chat` / `stream`

### Phase 5 — 未来方向
- [ ] 权重路由扩展：按成本/自定义评分函数加权
- [ ] Provider 插件机制：动态加载外部 Provider
- [ ] 分布式调度：多节点状态同步

---

## 许可证

MIT License. See [LICENSE](LICENSE) for details.

> AoEo = "Aggregation of Everything Open" —— 聚合一切 OpenAI-compatible 的模型服务。
