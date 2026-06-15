# AoEo

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.25-blue)](https://golang.org)
[![Go Report Card](https://g.shields.io/badge/go%20report-A+-brightgreen.svg)](https://goreportcard.com)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/JishiTeam-J1wa/AoEo/pulls)

> **Production-grade Multi-Provider AI API Gateway SDK — Unified Access, Smart Routing, Auto Failover, Privacy Protection**

AoEo（Aggregation of Everything Open）是一个 Go 语言 SDK，让你用同一套代码同时接入 DeepSeek、Kimi、GPT、GLM、Qwen 等多家 AI 服务。自动处理路由、故障转移、限流熔断、成本统计，并内置可选隐私网关——敏感数据出网前自动替换，返回时还原，用户无感知。

```
你的代码 ──→ AoEo ──┬──→ DeepSeek (主)
                   ├──→ Kimi (备用)
                   ├──→ GPT (备用)
                   └──→ Privacy Gateway (出网前脱敏)
```

---

## Architecture

```mermaid
graph TB
    subgraph "Layer 1 · Client Facade"
        A["Client SDK<br/>client.go · options.go"]
    end

    subgraph "Layer 2 · Scheduler Engine"
        B["Scheduler<br/>调度核心 · 并发控制 · 重试"]
        C["History & Audit<br/>Ring Buffer · 持久化"]
        D["Stream & Semaphore<br/>SSE · 自适应信号量"]
    end

    subgraph "Layer 3 · Provider Layer"
        E["BaseProvider<br/>熔断器 · 健康监控"]
        F["OpenAIProvider<br/>通用适配器 · Tool Calling"]
        G["Factories<br/>DeepSeek · Kimi · GLM · Qwen"]
    end

    subgraph "Layer 4 · Core Interfaces"
        H["Router<br/>5 策略"]
        I["Interceptor<br/>4 hooks"]
        J["Storage<br/>3 SQL + Pebble"]
        K["EventEmitter<br/>6 topics"]
    end

    subgraph "Layer 5 · Privacy Gateway"
        L["Gateway<br/>BeforeRequest / AfterResponse"]
        M["Pseudonymizer<br/>Detect → Replace → Restore"]
        N["OPF Client<br/>LoadBalanced · EWMA"]
        O["FakeGenerator<br/>10 Entity Types"]
        P["PebbleStore<br/>双向映射 KV"]
    end

    subgraph "Layer 6 · Storage Backends"
        Q["SQLite"]
        R["MySQL"]
        S["PostgreSQL"]
    end

    A --> B
    B --> C
    B --> D
    B --> E
    E --> F
    F --> G
    B --> H
    B --> I
    C --> J
    B --> K
    I --> L
    L --> M
    M --> N
    M --> O
    M --> P
    C --> Q
    C --> R
    C --> S
```

> 📐 **详细工程蓝图**（Flat Engineering Blueprint 风格）：
> - [系统全局架构图](docs/diagrams/aoeo_architecture.html)
> - [Privacy 模块详细工程图](docs/diagrams/aoeo_privacy_module.html)
> - [请求调度与数据流全链路图](docs/diagrams/aoeo_request_lifecycle.html)

---

## Quick Start

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

### Install

```bash
go get github.com/JishiTeam-J1wa/AoEo
```

> Go ≥ 1.25 required

---

## Features at a Glance

| 痛点 | AoEo 的解法 |
|---|---|
| 多平台 API 不统一 | **统一接口**：一条 `ChatComplete` 调用任意 OpenAI-compatible Provider |
| 单点故障 | **自动故障转移**：主 API 失败自动切备用，业务无感知 |
| 不知道用哪家 | **可插拔路由**：Primary / Round-Robin / Random / Weighted / 自定义策略 |
| 结果不可信 | **双模型验证**：同一请求并发发给两家，对比结果一致性 |
| 流式输出难处理 | **SSE 流式**：逐字返回，支持流式拦截器实时处理 |
| Prompt 散落各处 | **统一注入**：按 Provider/模型通配注入系统提示，零业务侵入 |
| 成本算不清 | **自动计费**：每次调用算 Token 成本，按 Provider 聚合 |
| 服务状态不明 | **健康探测**：后台定期探测，自动熔断/恢复 |
| 需要 Tool Calling | **Function Calling**：统一 Tools/ToolCalls 抽象 |
| 敏感数据泄露 | **隐私网关**：OPF 模型检测 PII，出网前替换伪造值，返回自动还原 |

---

## Core Features

### 1. Multi-Provider Scheduling

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
- **Round-Robin**：自动负载均衡
- **自适应并发限流**：总容量 = Σ(MaxConcurrent)，FIFO 队列防止 goroutine 爆炸

### 2. Auto Failover

主 Provider 失败后自动尝试下一个，直到成功或全部失败：

```go
resp, err := client.ChatCompleteWithFallback(ctx, req)
```

### 3. Dual Provider Verification

同一请求并发发给两个不同 Provider，对比结果一致性：

```go
dual, err := client.ChatCompleteDual(ctx, req)
fmt.Printf("Consensus: %v\n", dual.Consensus)
```

### 4. Audit Mode

串行调用两个 Provider 进行交叉验证，适合对结果可信度要求极高的场景：

```go
result, err := client.Audit(ctx, req)
if !result.Consensus {
    fmt.Println("结果不一致，建议人工复核")
}
```

### 5. SSE Streaming

```go
stream, err := client.ChatCompleteStream(ctx, req)
for chunk := range stream {
    if chunk.Err != nil {
        log.Fatal(chunk.Err)
    }
    if chunk.Chunk.FinishReason != "" {
        fmt.Printf("Tokens: %d total\n", chunk.Usage.TotalTokens)
        break
    }
    fmt.Print(chunk.Chunk.Delta.Content)
}
```

### 6. Prompt Injection Engine

按 Provider/Model 通配匹配，自动注入模板，无需改动业务代码：

```go
pi := aoeo.NewPromptInjector()

pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "*",
    Model:    "*",
    Position: "system",
    Content:  "You are a helpful assistant.",
})

pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "deepseek",
    Position: "system",
    Content:  "You are an expert programmer.",
})

client.SetPromptInjector(pi)
```

支持三个注入位置：`system`、`prepend_user`、`append_user`。支持 `{{var}}` 变量替换。

### 7. Cost Tracking

```go
cfg.Providers[0].Pricing = aoeo.Pricing{
    PromptPer1K:     2.0,
    CompletionPer1K: 8.0,
    Currency:        "CNY",
}

resp, _ := client.ChatComplete(ctx, req)
cost := resp.Usage.Cost(pricing) // 0.38 CNY

for name, s := range client.Stats() {
    fmt.Printf("%s: %d calls, %.4f %s total\n", name, s.TotalCalls, s.TotalCost, s.Currency)
}
```

内置默认价格：DeepSeek 2/8 CNY、Kimi 3/12 CNY、GLM 5/5 CNY、Qwen 5/10 CNY。

### 8. Circuit Breaker & Retry

每个 Provider 独立维护熔断状态：
- 连续 3 次失败 → 开启熔断，冷却 60 秒
- 成功后自动重置计数
- 指数退避重试（30% 抖动），自动识别超时/502/503/504/限流等可重试错误

```mermaid
stateDiagram-v2
    [*] --> CLOSED: 正常服务
    CLOSED --> OPEN: failCount >= MaxFailures
    OPEN --> HALFDASH: cooldown 过期
    HALFDASH --> CLOSED: 探测成功
    HALFDASH --> OPEN: 探测失败
    note right of CLOSED: sliding window 记录延迟/成功率
    note right of OPEN: IsAvailable() = false
```

### 9. Event System

```go
client.SetEmitter(&MyEmitter{})

// 内置事件：
// provider:fail      — Provider 调用失败
// provider:open      — 熔断器开启
// provider:recover   — 熔断恢复
// scheduler:fallback — Fallback 触发
// audit:disagree     — 审计发现分歧
// scheduler:dual     — 双模型完成
```

### 10. Interceptor Chain

`core.Interceptor` 提供 4 个 Hook，用于日志、监控、限流、请求篡改等横切关注点：

```go
ic := aoeo.Interceptor{
    BeforeRequest: func(ctx context.Context, req *aoeo.ChatCompletionRequest) error {
        req.Tags = append(req.Tags, "trace:xyz")
        return nil
    },
    AfterResponse: func(ctx context.Context, req aoeo.ChatCompletionRequest, resp *aoeo.ChatCompletionResponse, err error) (*aoeo.ChatCompletionResponse, error) {
        return resp, err
    },
    AfterStreamChunk: func(ctx context.Context, req aoeo.ChatCompletionRequest, chunk *aoeo.StreamChunk) error {
        return nil
    },
    AfterStreamDone: func(ctx context.Context, req aoeo.ChatCompletionRequest, err error) error {
        return nil
    },
}

client, _ := aoeo.NewClient(cfg, aoeo.WithInterceptors(ic))
```

### 11. Router Strategies

```go
client.SetRouter(&aoeo.RoundRobinRouter{})   // 轮询
client.SetRouter(&aoeo.RandomRouter{})        // 随机
client.SetRouter(&aoeo.WeightedRouter{        // 按延迟加权
    Strategy: aoeo.StrategyLatency,
})
```

| 策略 | 行为 | 适用场景 |
|---|---|---|
| `PrimaryRouter` | 选第一个可用 Provider | 默认，主备架构 |
| `RoundRobinRouter` | 轮询可用 Provider | 均匀负载 |
| `RandomRouter` | 随机选 Provider | 简单负载分散 |
| `WeightedRouter` | 按延迟/成功率/综合加权 | 智能路由 |
| `SingleProviderRouter` | 指定名称 Provider | CLI 定向调用 |

### 12. Function Calling

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
    fmt.Printf("Call %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
}
```

### 13. Persistent Storage

```go
// SQLite（零配置，单机推荐）
store, _ := storage.NewSQLite("./aoeo.db")

// MySQL（集群部署）
store, _ := storage.NewMySQL("user:pass@tcp(localhost:3306)/aoeo?charset=utf8mb4")

// PostgreSQL
store, _ := storage.NewPostgres("postgres://user:pass@localhost:5432/aoeo?sslmode=disable")
```

统一 Schema：calls、audits、privacy_mappings 三张表。

### 14. Environment Variable Configuration

```bash
export AOEO_PROVIDER_0_NAME=deepseek
export AOEO_PROVIDER_0_API_KEY=sk-xxx
export AOEO_PROVIDER_0_ENDPOINT=https://api.deepseek.com
export AOEO_PROVIDER_0_MODEL=deepseek-v4-pro

export AOEO_PROVIDER_1_NAME=kimi
export AOEO_PROVIDER_1_API_KEY=sk-yyy
export AOEO_PROVIDER_1_ENDPOINT=https://api.moonshot.cn/v1
export AOEO_PROVIDER_1_MODEL=kimi-k2.6
```

```go
cfg := aoeo.LoadConfigFromEnv()
client, err := aoeo.NewClient(cfg)
```

---

## Privacy Gateway

AoEo 内置基于 [OpenAI Privacy Filter (OPF)](https://github.com/gh0stkey/opf-privacy-filter) 模型的可逆隐私网关。敏感信息（PII、手机号、身份证号、内网 IP 等）在出网前被替换为逼真伪造值，AI 响应返回时自动还原，用户完全无感知。

```mermaid
graph LR
    A["原始请求<br/>含 PII"] --> B["Detect<br/>OPF /redact"]
    B --> C["Pseudonymize<br/>PII → Fake"]
    C --> D["Forward<br/>发给 AI Provider"]
    D --> E["Restore<br/>Fake → PII"]
    E --> F["原始响应<br/>已还原"]

    style B fill:#fff,stroke:#64748b
    style C fill:#fff,stroke:#dc2626
    style D fill:#fff,stroke:#64748b
    style E fill:#fff,stroke:#16a34a
```

### One-Line Integration

```bash
export AOEO_PRIVACY_ENABLED=true
export AOEO_PRIVACY_ENDPOINT=http://localhost:8080
```

```go
import "github.com/JishiTeam-J1wa/AoEo/privacy"

client, _ := aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
```

### Multi-Instance Cluster

```go
gw, _ := privacy.NewGateway(privacy.GatewayConfig{
    ModelEndpoint: "http://sidecar-1:8080,http://sidecar-2:8080,http://sidecar-3:8080",
    LBStrategy:    model.LeastLatency,
    Policy:        privacy.ActionPseudonymize,
    FailOpen:      true,
})
client, _ := aoeo.NewClient(cfg, aoeo.WithInterceptors(gw.ToInterceptor()))
```

### PII Entity Types (10 types, 30+ OPF labels normalized)

| EntityType | OPF Labels | Fake Example |
|---|---|---|
| `person` | NAME, PERSON, PER | 张伟 / John Smith |
| `email` | EMAIL_ADDRESS, EMAIL | a3f8@masked.com |
| `phone` | PHONE_NUMBER, PHONE | 13800138000 |
| `ip` | IP_ADDRESS, IP | 10.24.131.5 |
| `idcard` | ID_CARD, NRP | 110101199001011234 |
| `address` | LOCATION, ADDRESS, FAC | 北京市朝阳区建国路1号 |
| `url` | URL, URI | https://masked.local/path |
| `domain` | DOMAIN, HOSTNAME | sub.masked.local |
| `date` | DATE_TIME, DATE, TIME | 1995-07-22 |
| `secret` | CREDIT_CARD, CRYPTO, ... | ****-****-**** |

### Load Balancing Strategies

| 策略 | 行为 | 适用场景 |
|---|---|---|
| `RoundRobin` | 轮询分发 | 均匀负载 |
| `Random` | 随机分发 | 简单分散 |
| `LeastLatency` | EWMA 延迟加权 (alpha=0.3) | **生产推荐** |

### Processing Policies

| 策略 | 行为 | 适用场景 |
|---|---|---|
| `block` | 检测到敏感数据直接阻断 | 高安全环境 |
| `mask` | 替换为 `[REDACTED]` | 审计日志 |
| `pseudonymize` | 替换为逼真伪造值，返回时自动还原 | **生产推荐** |
| `audit` | 放行但记录审计日志 | 灰度观察 |

**完整使用手册**：见 [PRIVACY_GATEWAY.md](./PRIVACY_GATEWAY.md)

---

## Docker Deployment

### Cluster Mode (3x OPF instances)

```bash
docker-compose up -d
```

```mermaid
graph TB
    subgraph "OPF Cluster"
        OPF1["opf-1<br/>:8000"]
        OPF2["opf-2<br/>:8000"]
        OPF3["opf-3<br/>:8000"]
    end

    subgraph "Load Balancing"
        NGINX["nginx-opf<br/>:9000<br/>least_conn"]
        SIDECAR["privacy-sidecar<br/>:8080<br/>FastAPI + httpx"]
    end

    GW["aoeo-gateway<br/>:8081<br/>Go SDK 直连"]

    OPF1 --> NGINX
    OPF2 --> NGINX
    OPF3 --> NGINX
    NGINX --> SIDECAR
    GW -->|"LoadBalancedClient<br/>LeastLatency EWMA"| OPF1
    GW -->|"LoadBalancedClient<br/>LeastLatency EWMA"| OPF2
    GW -->|"LoadBalancedClient<br/>LeastLatency EWMA"| OPF3
```

| Service | Image | Port | Purpose |
|---|---|---|---|
| `opf-1/2/3` | `ghcr.io/gh0stkey/opf-privacy-filter` | 8000 | OPF PII 检测实例 |
| `nginx-opf` | `nginx:alpine` | 9000 | OPF 集群负载均衡 |
| `privacy-sidecar` | local build | 8080 | 向后兼容 /detect 代理 |
| `aoeo-gateway` | local build | 8081 | 主网关 (Go 直连 OPF) |

---

## Supported Providers

| Provider | Factory | Default Endpoint | Default Model | Pricing (Prompt/Completion) |
|---|---|---|---|---|
| DeepSeek | `NewDeepSeekProvider()` | `https://api.deepseek.com` | `deepseek-v4-pro` | 2 / 8 CNY |
| Kimi (Moonshot) | `NewKimiProvider()` | `https://api.moonshot.cn/v1` | `kimi-k2.6` | 3 / 12 CNY |
| GLM (ZhiPu) | `NewGLMProvider()` | `https://open.bigmodel.cn/api/paas/v4` | `glm-5.1` | 5 / 5 CNY |
| Qwen (TongYi) | `NewQwenProvider()` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.7-max` | 5 / 10 CNY |
| Any OpenAI-compat | `NewOpenAIProvider()` | *(from config)* | *(from config)* | *(custom)* |

---

## Project Structure

```
AoEo/
├── client.go              # SDK 入口, Client facade + 47 type aliases
├── options.go             # Functional request builder (13 options)
├── go.mod                 # Go 1.25, 5 direct deps
│
├── core/                  # 公共类型与接口 (9 files, ~900 lines)
│   ├── types.go           # Request, Response, Message, Choice, Usage, DualResult
│   ├── config.go          # ProviderConfig + validation
│   ├── env.go             # LoadConfigFromEnv()
│   ├── pricing.go         # Token cost calculation
│   ├── retry.go           # Exponential backoff config
│   ├── interceptor.go     # 4-hook interceptor chain
│   ├── router.go          # 5 router strategies
│   ├── storage.go         # Storage interface (calls, audits, mappings)
│   ├── event.go           # EventEmitter + 6 topics
│   └── logger.go          # Structured logger
│
├── providers/             # Provider 实现 (845 lines)
│   └── providers.go       # Provider interface, BaseProvider, OpenAIProvider, 4 factories
│
├── internal/engine/       # 调度引擎核心 (6 files, ~2,062 lines)
│   ├── scheduler.go       # Scheduler: orchestration, fallback, dual, health check
│   ├── history.go         # Ring buffer + async persistence
│   ├── prompt.go          # Template-based prompt injection
│   ├── stream.go          # SSE streaming + ParseSSE
│   ├── audit.go           # Dual-provider audit
│   ├── result.go          # JSON extraction + consensus
│   ├── semaphore.go       # Adaptive semaphore (CAS + FIFO)
│   └── retry_impl.go      # Exponential backoff with jitter
│
├── privacy/               # 隐私网关 (13 source files, ~2,100 lines)
│   ├── gateway.go         # Privacy Gateway (Interceptor integration)
│   ├── pseudonymizer.go   # Detect → Replace → Restore pipeline
│   ├── generator.go       # FakeGenerator (10 entity types)
│   ├── detector.go        # Detector interface
│   ├── model_adapter.go   # model.Client → Detector adapter
│   ├── option.go          # WithPrivacyFilter() / WithPrivacyModel()
│   ├── types.go           # EntityType, Span, MappingEntry
│   ├── store/             # Pebble KV mapping storage
│   │   ├── interface.go   # MappingStore interface
│   │   └── pebble.go      # Pebble LSM-Tree backend
│   └── model/             # OPF detection client
│       ├── client.go      # Client interface (Detect / DetectBatch / HealthCheck)
│       ├── http.go        # OPF HTTP client (/redact, /redact/batch, /health)
│       └── loadbalancer.go # Multi-backend LB (RR/Random/LeastLatency EWMA)
│
├── storage/               # SQL 持久化后端 (4 files, 431 lines)
│   ├── base.go            # Shared SQL CRUD (3 tables, 6 indexes)
│   ├── sqlite.go          # SQLite (pure Go, no CGO)
│   ├── mysql.go           # MySQL
│   └── postgres.go        # PostgreSQL
│
├── cmd/
│   ├── aoeo/              # CLI tool
│   │   └── main.go        # list-models / test / status / chat / stream / privacy
│   └── privacy-sidecar/   # OPF proxy sidecar
│       ├── main.py        # FastAPI + httpx (OPF /redact proxy)
│       ├── Dockerfile     # python:3.12-slim (~50MB)
│       └── requirements.txt
│
├── docs/
│   ├── diagrams/          # Engineering blueprint diagrams
│   │   ├── aoeo_architecture.html       # 6-layer system architecture
│   │   ├── aoeo_privacy_module.html     # Privacy module detailed blueprint
│   │   └── aoeo_request_lifecycle.html  # Request scheduling & data flow
│   ├── DEEP_AUDIT_REPORT.md             # 7-dimension deep audit report
│   └── COMMENT_AUDIT_REPORT.md          # Comment normalization audit
│
├── examples/              # Usage examples
│   ├── basic/
│   ├── streaming/
│   ├── multi_provider/
│   ├── audit/
│   ├── events/
│   ├── list_models/
│   └── privacy/
│
├── docker-compose.yml     # Full stack: 3x OPF + nginx + sidecar + gateway
├── nginx.conf             # Sidecar load balancer
├── nginx-opf.conf         # OPF load balancer
├── Dockerfile             # Go gateway container (static binary)
├── DESIGN.md              # Architecture design document
├── PRIVACY_GATEWAY.md     # Privacy Gateway usage guide
└── LICENSE                # MIT License
```

---

## Production Checklist

1. **API Key 管理**：通过环境变量注入，不要硬编码
2. **并发上限**：根据各平台 RPM/TPM 限制设置 `MaxConcurrent`
3. **Stream 退出**：消费端提前 break 时，应同时 `cancel()` context
4. **Graceful Shutdown**：始终 `defer client.Close()`
5. **日志级别**：生产环境建议 `slog.LevelWarn`
6. **请求预验证**：发送前调用 `req.Validate()` 提前拦截参数错误
7. **Nil 安全访问**：优先使用 `resp.Content()` 代替 `resp.Choices[0].Message.Content`

---

## Changelog

### v1.3.0 — 深度审计 & 注释规范化 (2026-06-15)

**深度代码审计**: 对全部 39 个源文件（~7,700 行）执行 7 维度功能审计（架构合理性、并发安全、错误处理、边界条件、安全漏洞、性能隐患、可扩展性），发现并修复 **11 个 HIGH + 18 个 MEDIUM** 级问题。

<details>
<summary>HIGH 级修复 (11项)</summary>

| 模块 | 问题 | 修复方案 |
|------|------|----------|
| `client.go` | `ChatCompleteWithProvider` 并发修改共享路由器导致数据竞争 | 改为请求级路由注入（`ChatCompleteWithRouter`），不再修改全局状态 |
| `stream.go` | 缺少 `req.Clone()`，拦截器修改污染调用方原始请求 | 始终使用 `req.Clone()` 深拷贝 |
| `scheduler.go` | `ChatCompleteWithFallback` 未克隆请求即传入拦截器 | `ApplyBefore` 前添加 `req.Clone()` |
| `scheduler.go` | `ChatCompleteDual` 两处 `append(req.Tags)` 共享底层数组竞争 | 显式 `make + copy` 克隆 Tags 切片 |
| `history.go` | `Record()` 每次创建新 goroutine，高吞吐下 OOM | 改为 4-worker 有界池 + 256 缓冲 channel |
| `prompt.go` | `Inject()` 释放 RLock 后遍历共享底层数组，与 `Clear()` 竞争 | RLock 内完成模板深拷贝 |
| `providers.go` | `ChatCompleteStream` 外层 select+default 形成 busy-wait | 改为 `ctx.Err()` 检查 + 自然阻塞 |
| `providers.go` | WaitGroup 创建但从未 Wait()，Provider 关闭时流式 goroutine 泄漏 | `OpenAIProvider` 新增 `streamWg`，`Close()` 中 `Wait()` |
| `pseudonymizer.go` | map 迭代不确定性导致替换顺序随机 | 改为按 spans 排序顺序遍历 |
| `generator.go` | `genericMask` 使用 `len()` 计算字节数而非字符数 | 改用 `utf8.RuneCountInString()` |
| `cmd/main.go` | 指定 `-model` 时重建请求丢失 `-temperature` | 重建时同时传入所有选项 |

</details>

<details>
<summary>MEDIUM 级修复 (18项)</summary>

| 模块 | 问题 | 修复方案 |
|------|------|----------|
| `client.go` | `SetEmitter` 持锁期间遍历 Provider，锁持有时间过长 | 先赋值再释放锁，锁外传播到 Provider |
| `core/env.go` | `SetEnvConfig` 硬编码前缀、不清理残留、静默吞错 | 支持自定义前缀 + 清理残留 + 记录警告日志 |
| `core/retry.go` | `IsRetryableError` 纯字符串匹配可能误判 | 优先用 `net.Error` 结构化判断 |
| `core/router.go` | `WeightedRouter` Select 过滤零分而 SelectSequence 不过滤 | 零分时回退到 RoundRobin |
| `core/interceptor.go` | 拦截器链无 panic 恢复 | 四个 Apply 方法添加 `recover()` |
| `core/pricing.go` | `float64` 货币计算 IEEE 754 精度漂移 | 改用整数运算（微单位） |
| `semaphore.go` | `setMaxConc` 无下限校验，n≤0 导致永久阻塞 | 添加 `n >= 1` 下限 |
| `result.go` | 正则缓存满时全量清空，造成延迟尖峰 | 随机淘汰一半 |
| `prompt.go` | `injectSystem` 替换而非追加 system 消息 | 改为 prepend + `\n\n` 分隔 |
| `providers.go` | `HealthCheck` 每次创建新 `http.Client` | 复用 Provider 级共享客户端 |
| `storage/base.go` | JSON Marshal/Unmarshal 错误被静默丢弃 | 记录警告日志 |
| `storage/base.go` | `GetCallsByTag` LIKE 匹配精确度不足 | 添加 Go 层精确二次过滤 |
| `storage/mysql.go` | 未设置 utf8mb4 字符集 | 初始化时执行 `SET NAMES utf8mb4` |
| `cmd/main.go` | 死代码 `cmdPrivacy` 未注册 | 在 switch 中添加 `"privacy"` case |
| `cmd/main.go` | 缺少 SIGINT/SIGTERM 信号处理 | 添加 `signal.NotifyContext` |
| `engine/stream.go` | `ChatCompleteStreamWithRouter` 新增 | 支持请求级路由注入的流式方法 |
| `engine/scheduler.go` | `ChatCompleteWithRouter` 新增 | 线程安全的指定路由补全方法 |
| `privacy/gateway.go` | `Stats()` 返回 atomic 值拷贝 | 改为返回 `*Stats` 指针 |

</details>

### v1.2.0 — 注释体系规范化 (2026-06-12)

对全部 39 个 Go 源文件执行注释体系规范化（+2,184 / -617 行）：

- 为 37 个文件补充标准文件头（功能描述 / Author / Changelog）
- 约 180 个导出函数补充 GoDoc 格式 Param / Return / Edge Cases 段
- 删除约 40 处仅复述代码行为的冗余注释
- 替换约 12 处 "implements X" 桩注释为有信息量的描述
- 全部注释语言统一为中文（保留 CAS / EWMA / FIFO 等技术术语）

### v1.1.0 — Bug 修复 + 中文注释 (2026-06-12)

深度审计全部代码，发现并修复 **16 个 HIGH + 5 个 MEDIUM** 级 Bug，涵盖存储层资源泄漏、核心引擎竞态条件、隐私网关并发安全、Provider 适配层死代码等。修复后全部添加详细中文注释。

### v1.0.0 — OPF 隐私引擎集成 (2026-06-11)

集成 OpenAI Privacy Filter 模型替代 HuggingFace NER，重写 Sidecar 为轻量 httpx 代理，实现 3x OPF 集群部署，完成工程蓝图设计。

---

## Roadmap

### Phase 2 — Production Hardening (Done)
- [x] Exponential backoff retry
- [x] Token usage & cost tracking
- [x] Prompt injection system
- [x] Structured logging
- [x] Graceful shutdown
- [x] Panic recovery + semaphore leak prevention
- [x] Request validation `Validate()`
- [x] Safe accessor `Content()`

### Phase 3 — Network & Observability (Done)
- [x] Per-provider proxy support
- [x] Interceptor chain (BeforeRequest / AfterResponse)
- [x] Environment variable configuration
- [x] Custom HTTPClient

### Phase 3.5 — Architecture Debt (Done)
- [x] Streaming architecture refactor
- [x] Stream interceptor support
- [x] Goroutine leak fix (buffered channel + safe select)
- [x] Test coverage: 66.6% → 71.7%

### Phase 4 — Ecosystem (Done)
- [x] Weighted router (latency / success rate / combined)
- [x] Provider health monitoring (20-entry sliding window)
- [x] Function Calling abstraction
- [x] CLI tool

### Phase 5 — Privacy Gateway (Done)
- [x] OPF (OpenAI Privacy Filter) integration as PII detection engine
- [x] LoadBalancedClient with LeastLatency EWMA strategy
- [x] Batch detection (N messages → 1 HTTP round-trip)
- [x] HTTP/2 + connection warm-up
- [x] Pebble KV bidirectional mapping storage
- [x] 3x OPF cluster deployment (Docker Compose)
- [x] Lightweight sidecar proxy (FastAPI + httpx, ~50MB)

### Phase 5.5 — Deep Audit & Comment Normalization (Done)
- [x] 7-dimension deep audit across all 39 source files (~7,700 lines)
- [x] 11 HIGH + 18 MEDIUM concurrency safety & functional defects fixed
- [x] Comment system normalization: GoDoc headers, Param/Return/Edge Cases for ~180 functions
- [x] All comments unified to Chinese (technical terms preserved)
- [x] `go build` + `go vet` + `go test -race` full verification

### Phase 6 — Future Directions
- [ ] Weighted router: cost-based / custom scoring functions
- [ ] Provider plugin mechanism: dynamic external provider loading
- [ ] Distributed scheduling: multi-node state sync
- [ ] Prometheus metrics exporter
- [ ] OpenTelemetry tracing integration

---

## Documentation

| 文档 | 说明 |
|---|---|
| [DESIGN.md](./DESIGN.md) | 架构设计文档 |
| [PRIVACY_GATEWAY.md](./PRIVACY_GATEWAY.md) | 隐私网关完整使用手册 |
| [INTEGRATION.md](./INTEGRATION.md) | 集成指南 |
| [DEEP_AUDIT_REPORT.md](./docs/DEEP_AUDIT_REPORT.md) | 功能实现深度审计报告（7 维度 · 39 文件） |
| [COMMENT_AUDIT_REPORT.md](./docs/COMMENT_AUDIT_REPORT.md) | 注释体系规范化整改报告 |
| [Architecture Blueprint](./docs/diagrams/aoeo_architecture.html) | 6 层系统全局架构图 |
| [Privacy Blueprint](./docs/diagrams/aoeo_privacy_module.html) | Privacy 模块详细工程图 |
| [Request Lifecycle](./docs/diagrams/aoeo_request_lifecycle.html) | 请求调度与数据流全链路图 |

---

## Contributing

PRs and issues are welcome! Please:

1. Fork the repo and create your branch from `master`
2. Write tests for new functionality
3. Ensure `go test ./... -race` passes
4. Submit a pull request

---

## License

MIT License. See [LICENSE](LICENSE) for details.

> AoEo = "Aggregation of Everything Open" — 聚合一切 OpenAI-compatible 的模型服务。
