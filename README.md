# AoEo — 多模型聚合调度 SDK for Go

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

**AoEo** (Aggregation of Everything Open) 是一个用于聚合多种 OpenAI-compatible 大模型 API 的 Go SDK。它提供统一的调用接口、自动熔断降级、并发限流、计费统计、Prompt 注入和交叉审计能力，让你用 10 行代码获得生产级的多模型调度能力。

> **设计哲学**：只做多 Provider 聚合调度，不做 Prompt 模板管理、对话历史、RAG 等上层抽象。保持极简，易于嵌入任何项目。

---

## 特性一览

| 特性 | 说明 |
|---|---|
| 🔄 **多 Provider 聚合** | DeepSeek / Kimi / GLM / Qwen / Claude / 任意 OpenAI-compatible API |
| ⚡ **自动 Fallback** | 主 Provider 失败时自动切换到备用，支持多轮重试 |
| 🛡️ **熔断器** | 连续 3 次失败自动冷却 60 秒，恢复后自动复位 |
| 🔒 **并发限流** | 自适应信号量，容量 = Σ(Provider.MaxConcurrent)，防止配额耗尽 |
| 📊 **双模型验证** | Dual 模式并发调用两个模型，Consensus 判断结果一致性 |
| 🔍 **审计模式** | Audit 串行调用两个模型，交叉验证并合并结果 |
| 📡 **SSE 流式** | 原生 Streaming 支持，逐字返回 |
| 💰 **计费统计** | 自动记录 Token 用量、计算成本、按 Provider 聚合 |
| 💉 **Prompt 注入** | 支持按 Provider/Model 通配匹配，自动注入 System/Prepend/Append 模板 |
| 📈 **事件系统** | 内置 Provider 失败/恢复/Fallback/Audit 分歧事件 |
| 🔄 **指数退避重试** | 可配置的重试策略，自动识别可重试错误 |
| 🧹 **优雅关闭** | `Close()` 方法安全释放资源，拒绝新请求 |

---

## 安装

```bash
go get github.com/JishiTeam-J1wa/AoEo
```

---

## 快速开始

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/JishiTeam-J1wa/AoEo"
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

    req := aoeo.BuildRequest(
        []aoeo.Message{{Role: "user", Content: "Hello, world!"}},
        aoeo.WithTemperature(0.7),
    )

    resp, err := client.ChatComplete(context.Background(), req)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(resp.Choices[0].Message.Content)
    fmt.Printf("Cost: %.4f CNY | Tokens: %d\n",
        resp.Usage.Cost(aoeo.DefaultPricing("deepseek", "deepseek-v4-pro")),
        resp.Usage.TotalTokens)
}
```

---

## 功能详解

### 1. 基础调用 — Primary Provider

始终使用配置列表中第一个可用的 Provider：

```go
resp, err := client.ChatComplete(ctx, req)
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.Choices[0].Message.Content)
fmt.Printf("Usage: prompt=%d completion=%d total=%d\n",
    resp.Usage.PromptTokens,
    resp.Usage.CompletionTokens,
    resp.Usage.TotalTokens)
```

### 2. 自动 Fallback — 高可用保障

主 Provider 失败后自动尝试下一个，直到成功或全部失败：

```go
resp, err := client.ChatCompleteWithFallback(ctx, req)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Response from: %s\n", resp.Model)
```

**事件通知**（可选）：
```go
client.SetEmitter(&MyEventEmitter{})

// 当 Fallback 被触发时收到事件
const EventFallbackTrigger = "scheduler:fallback"
```

### 3. Dual 双模型验证 — 并发交叉调用

同时发给两个不同 Provider，返回两者结果 + 一致性判断：

```go
dual, err := client.ChatCompleteDual(ctx, req)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Consensus: %v\n", dual.Consensus)
if dual.Result1 != nil {
    fmt.Println("Provider 1:", dual.Result1.Choices[0].Message.Content)
}
if dual.Result2 != nil {
    fmt.Println("Provider 2:", dual.Result2.Choices[0].Message.Content)
}
```

### 4. Streaming 流式响应

```go
stream, err := client.ChatCompleteStream(ctx, req)
if err != nil {
    log.Fatal(err)
}

for chunk := range stream {
    if chunk.Err != nil {
        log.Printf("Stream error: %v\n", chunk.Err)
        break
    }
    if chunk.Chunk.FinishReason != "" {
        fmt.Printf("\n[Finish: %s]\n", chunk.Chunk.FinishReason)
        break
    }
    fmt.Print(chunk.Chunk.Delta.Content)
}
```

### 5. Audit 审计模式 — 串行交叉验证

先调用 Primary，再用另一个 Provider 验证结果：

```go
result, err := client.Audit(ctx, req)
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Consensus: %v\n", result.Consensus)
fmt.Println("Primary:", result.Primary.Choices[0].Message.Content)
if result.Audit != nil {
    fmt.Println("Audit  :", result.Audit.Choices[0].Message.Content)
}
if !result.Consensus {
    fmt.Println("⚠️ 结果不一致，建议人工复核")
}
```

### 6. Prompt 注入 — 动态模板

#### 全局 System Prompt 注入

```go
client, _ := aoeo.NewClient(cfg,
    aoeo.WithSystemPromptInjector(
        "You are a helpful assistant. Current date: {{date}}.",
        map[string]string{"date": time.Now().Format("2006-01-02")},
    ),
)
```

#### 按 Provider/Model 匹配注入

```go
pi := aoeo.NewPromptInjector()

// DeepSeek 专用系统提示
pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "deepseek",
    Model:    "*",
    Position: "system",
    Content:  "You are DeepSeek, specialized in coding.",
})

// Kimi 专用前置任务标记
pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "kimi",
    Model:    "kimi-k2.6",
    Position: "prepend_user",
    Content:  "[Task: {{task}}]",
    Vars:     map[string]string{"task": "creative-writing"},
})

client, _ := aoeo.NewClient(cfg, aoeo.WithPromptInjector(pi))
```

**注入位置**：
- `"system"` — 替换/添加 system 消息
- `"prepend_user"` — 在第一条 user 消息前插入
- `"append_user"` — 在最后一条 user 消息后追加

**通配匹配**：`Provider: "*"` 或 `Model: "*"` 匹配全部。

### 7. 计费统计 — 成本追踪

#### 配置价格

```go
cfg := aoeo.Config{
    Providers: []aoeo.ProviderConfig{
        {
            Name:     "deepseek",
            APIKey:   "sk-xxx",
            Endpoint: "https://api.deepseek.com",
            Model:    "deepseek-v4-pro",
            Pricing: aoeo.Pricing{
                PromptPer1K:     2.0,  // 每1K prompt tokens 2元
                CompletionPer1K: 8.0,  // 每1K completion tokens 8元
                Currency:        "CNY",
            },
        },
    },
}
```

#### 实时成本查询

```go
hist := aoeo.NewHistory(500)
client, _ := aoeo.NewClient(cfg, aoeo.WithHistory(hist))

resp, _ := client.ChatComplete(ctx, req)
fmt.Println(resp.Usage.CostString(cfg.Providers[0].Pricing))
// 输出: 0.380000 CNY
```

#### 聚合统计

```go
stats := client.Stats()
for name, s := range stats {
    fmt.Printf("%s: %d calls, %.4f %s total, %.1fms avg, %d failed\n",
        name, s.TotalCalls, s.TotalCost, s.Currency,
        float64(s.AvgLatencyMs), s.FailedCalls)
}
```

#### 按标签过滤历史

```go
// 调用时打标签
req.Tags = []string{"production", "v2-prompt"}
resp, _ := client.ChatComplete(ctx, req)

// 后续查询
for _, r := range hist.RecordsByTag("production") {
    fmt.Printf("%s: %dms, cost=%.4f\n", r.Provider, r.LatencyMs, r.Cost)
}
```

### 8. 事件系统 — 监听 Provider 生命周期

```go
type MyEmitter struct{}

func (e *MyEmitter) Emit(topic string, data ...any) {
    switch topic {
    case aoeo.EventProviderFail:
        fmt.Printf("❌ Provider %s failed (count=%d)\n", data[0], data[1])
    case aoeo.EventProviderOpen:
        fmt.Printf("🔒 Provider %s circuit breaker OPEN\n", data[0])
    case aoeo.EventProviderRecover:
        fmt.Printf("✅ Provider %s recovered\n", data[0])
    case aoeo.EventFallbackTrigger:
        fmt.Printf("🔄 Fallback triggered: %s\n", data[0])
    case aoeo.EventAuditDisagree:
        fmt.Printf("⚠️  Audit disagreement: %s\n", data[0])
    }
}

client.SetEmitter(&MyEmitter{})
```

### 9. 配置选项

```go
client, _ := aoeo.NewClient(cfg,
    aoeo.WithTimeout(60*time.Second),           // 单次请求超时
    aoeo.WithHistory(aoeo.NewHistory(1000)),    // 历史记录
    aoeo.WithRetry(aoeo.RetryConfig{             // 重试策略
        MaxRetries: 3,
        BaseDelay:  500 * time.Millisecond,
        MaxDelay:   10 * time.Second,
        Multiplier: 2.0,
    }),
    aoeo.WithPromptInjector(pi),                 // Prompt 注入
)
```

### 10. 模型列表查询

```go
models, err := client.ListModels(ctx, "deepseek")
for _, m := range models {
    fmt.Printf("  - %s (by %s)\n", m.ID, m.OwnedBy)
}

// 连通性测试
if err := client.TestProvider(ctx, "kimi"); err != nil {
    log.Printf("Kimi unreachable: %v", err)
}
```

### 11. 优雅关闭

```go
// 安全关闭，拒绝新请求
if err := client.Close(); err != nil {
    log.Printf("Close error: %v", err)
}

// 关闭后所有调用返回错误
_, err := client.ChatComplete(ctx, req)
// err: "scheduler is closed"
```

### 12. 结构化日志

默认使用 `log/slog` 输出 JSON 格式日志到 stderr：

```json
{"time":"2026-05-29T10:24:08","level":"WARN","msg":"circuit breaker opened","provider":"deepseek","failCount":3}
```

自定义日志：

```go
aoeo.SetLogger(mySlogLogger)
```

---

## 支持的 Provider

| Provider | 内置工厂 | 默认端点 | 默认模型 |
|---|---|---|---|
| **DeepSeek** | `aoeo.NewDeepSeekProvider()` | `https://api.deepseek.com` | `deepseek-v4-pro` |
| **Kimi (Moonshot)** | `aoeo.NewKimiProvider()` | `https://api.moonshot.cn/v1` | `kimi-k2.6` |
| **GLM (智谱)** | `aoeo.NewGLMProvider()` | `https://open.bigmodel.cn/api/paas/v4` | `glm-5.1` |
| **Qwen (通义)** | `aoeo.NewQwenProvider()` | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen3.7-max` |
| **Claude / OpenAI / 任意兼容** | `aoeo.NewOpenAIProvider()` | `https://api.openai.com/v1` | *(从配置读取)* |

任何支持 OpenAI Chat Completions 协议的 API 都可以通过 `NewOpenAIProvider()` 接入。

---

## 架构设计

```
┌─────────────┐
│   Client    │  ← 用户入口，fluent API，事件发射
└──────┬──────┘
       │
┌──────▼──────┐
│  Scheduler  │  ← Provider 管理、负载均衡、并发控制、Prompt 注入
└──────┬──────┘
       │
  ┌────┼────┬────────┐
  ▼    ▼    ▼        ▼
┌───┐┌───┐┌───┐  ┌───────┐
│DS ││KM ││GLM│  │Generic│  ← Provider 实现 (OpenAI-compatible)
└───┘└───┘└───┘  └───────┘
  │    │    │        │
  └────┴────┴────────┘
         │
    go-openai SDK
         │
    HTTP / SSE
```

**核心组件**：
- **Provider 接口**：统一抽象，支持自定义实现
- **BaseProvider**：熔断器、系统提示词覆盖、事件发射
- **Scheduler**：Primary 选择、Round-Robin、自适应信号量
- **History**：调用记录、Token 统计、成本聚合、标签过滤
- **PromptInjector**：模板匹配、变量替换、多位置注入

---

## 生产部署建议

### 1. 环境变量管理 API Key

**永远不要**在代码中硬编码 API Key：

```go
apiKey := os.Getenv("DEEPSEEK_API_KEY")
if apiKey == "" {
    log.Fatal("DEEPSEEK_API_KEY not set")
}
```

### 2. 设置合理的并发上限

```go
ProviderConfig{
    Name:          "deepseek",
    MaxConcurrent: 5,  // 根据平台速率限制调整
}
```

### 3. 启用历史记录用于监控

```go
hist := aoeo.NewHistory(10000)  // 保留最近 10000 条记录
client, _ := aoeo.NewClient(cfg, aoeo.WithHistory(hist))

// 定期导出统计
ticker := time.NewTicker(1 * time.Minute)
for range ticker.C {
    for name, s := range client.Stats() {
        metrics.Record("aoeo.calls", s.TotalCalls, "provider", name)
        metrics.Record("aoeo.cost", s.TotalCost, "provider", name)
    }
}
```

### 4. 处理 Stream 消费者的提前退出

调用方如果提前 `break` 或 `return`，应同时 `cancel` ctx 以确保 goroutine 正确退出：

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

stream, _ := client.ChatCompleteStream(ctx, req)
for chunk := range stream {
    if someCondition {
        cancel() // 确保内部 goroutine 收到退出信号
        break
    }
}
```

### 5. 自定义 Logger

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelWarn,
}))
aoeo.SetLogger(logger)
```

---

## 完整示例

见 [`examples/`](examples/) 目录：

- [`basic/`](examples/basic/) — 单 Provider 基础调用
- [`multi_provider/`](examples/multi_provider/) — 多 Provider + Fallback + Dual
- [`streaming/`](examples/streaming/) — SSE 流式响应
- [`list_models/`](examples/list_models/) — 模型列表查询 + 连通性测试

---

## Roadmap

- [x] 指数退避重试
- [x] Token 用量追踪与成本估算
- [x] Prompt 注入系统
- [x] 结构化日志
- [x] 优雅关闭
- [ ] 权重路由（按价格/延迟/质量加权）
- [ ] Provider 主动健康检查心跳
- [ ] Function Calling 抽象层
- [ ] CLI 工具（`aoeo list-models`, `aoeo test`）

---

## License

MIT License. See [LICENSE](LICENSE) for details.
