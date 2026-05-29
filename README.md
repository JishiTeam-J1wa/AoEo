# AoEo

> **一个用于聚合调度多 AI API 的 Go SDK**

AoEo 让你在代码中用统一的接口调用多个大模型 API（DeepSeek、Kimi、GPT、GLM、Qwen 等），并自动处理故障转移、并发限流、熔断降级、成本统计和 Prompt 注入。

无需为每个平台写不同的调用代码，也无需自己实现重试和容灾逻辑。

---

## 用 AoEo 可以做什么

| 场景 | 你能做的 |
|---|---|
| **多模型统一调用** | 一条 `ChatComplete` 请求，自动路由到可用 Provider |
| **故障自动转移** | 主 API 失败时，自动切到备用 API，业务无感知 |
| **双模型交叉验证** | 同一请求并发发给两个模型，对比结果一致性 |
| **流式响应** | SSE 逐字返回，适用于聊天界面和实时场景 |
| **Prompt 统一管理** | 按 Provider/模型通配注入系统提示，无需改动业务代码 |
| **成本透明化** | 每次调用自动计算 Token 成本，按 Provider 聚合统计 |
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

    fmt.Println(resp.Choices[0].Message.Content)
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
```

每次尝试前获取信号量许可，失败时释放。适合生产环境的高可用场景。

### 3. 双模型并发验证（Dual）

同一请求并发发给两个不同 Provider，对比结果一致性：

```go
dual, err := client.ChatCompleteDual(ctx, req)
fmt.Printf("Consensus: %v\n", dual.Consensus)
```

### 4. 审计模式（Audit）

串行调用两个 Provider 进行交叉验证，适合对结果可信度要求极高的场景：

```go
result, err := client.Audit(ctx, req)
if !result.Consensus {
    fmt.Println("⚠️ 结果不一致，建议人工复核")
}
```

### 5. SSE 流式响应

```go
stream, err := client.ChatCompleteStream(ctx, req)
for chunk := range stream {
    if chunk.Err != nil {
        log.Fatal(chunk.Err)
    }
    if chunk.Chunk.FinishReason != "" {
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
│   ├── event.go       # 事件系统
│   └── logger.go      # 结构化日志
├── providers/         # Provider 接口和实现
│   └── providers.go   # Provider 接口 + BaseProvider + OpenAI + 内置 Provider
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
├── INTEGRATION.md
├── AUDIT_REPORT.md
├── LICENSE
└── aoeo_test.go       # 32 个单元测试（含 Race Detector）
```

---

## 后续更新计划

### Phase 2 — 生产增强（已完成）
- [x] 指数退避重试
- [x] Token 用量追踪与成本估算
- [x] Prompt 注入系统
- [x] 结构化日志
- [x] 优雅关闭

### Phase 3 — 网络增强
- [ ] **定向 AI API 代理**

  支持为不同 Provider 配置独立代理：

  ```go
  ProviderConfig{
      Name:    "kimi",
      Proxy:   "http://proxy-a1.example.com:8080",  // Kimi 走代理 A1
  },
  ProviderConfig{
      Name:    "gpt",
      Proxy:   "http://proxy-a2.example.com:8080",  // GPT 走代理 A2
  }
  ```

  适用场景：
  - 不同 Provider 的网络出口隔离
  - 内网环境通过不同代理访问外网
  - 按 Provider 走不同线路优化延迟

- [ ] 权重路由（按价格/延迟/质量加权选择 Provider）
- [ ] Provider 主动健康检查心跳
- [ ] Function Calling 抽象层
- [ ] CLI 工具（`aoeo list-models`, `aoeo test`）

---

## 许可证

MIT License. See [LICENSE](LICENSE) for details.

> AoEo = "Aggregation of Everything Open" —— 聚合一切 OpenAI-compatible 的模型服务。
