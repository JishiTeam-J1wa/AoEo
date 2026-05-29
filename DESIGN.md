# AoEo 聚合调度设计文档

> 文档版本：v1.1
> 日期：2026-05-29
> 作者：JishiTeam J1wa

---

## 1. 背景与动机

### 1.1 问题

在实际生产环境中使用大模型 API 时，我们面临以下痛点：

1. **单点故障**：依赖单一 Provider，一旦服务不可用或配额耗尽，整个系统停摆
2. **配额管理困难**：不同 Provider 的速率限制、并发限制各异，容易触发限流
3. **模型选择成本高**：每个平台模型列表不同，切换需要改代码
4. **结果可信度存疑**：单一模型的输出可能存在幻觉，缺乏交叉验证机制
5. **故障恢复滞后**：API 异常后缺乏自动熔断和恢复机制
6. **成本不透明**：各平台计费单位不同，难以统一追踪 Token 成本和调用量
7. **Prompt 管理碎片化**：系统提示词散落在业务代码各处，缺乏统一注入层

### 1.2 现有方案的问题

| 方案 | 问题 |
|---|---|
| 直接用 OpenAI SDK | 只支持单 Provider，无熔断、无 fallback |
| LiteLLM / OpenRouter | 功能强大但过重，引入额外服务依赖 |
| 自己写代理层 | 重复造轮子，每家 API 差异需要单独适配 |

### 1.3 我们的思路

**"只做聚合调度，不做上层抽象"**

- 不管理对话历史、RAG、Tool Calling 等上层逻辑
- 只解决"多 Provider 如何被统一、可靠、高效地调用"
- 基于 OpenAI-compatible 协议，最大化兼容性
- 在调度层内解决 Prompt 注入和成本追踪，不侵入业务代码

---

## 2. 核心设计

### 2.1 架构总览

```
┌─────────────────────────────────────────────────────────────┐
│                         User Code                            │
└─────────────────────────┬───────────────────────────────────┘
                          │
┌─────────────────────────▼───────────────────────────────────┐
│                       Client                                 │
│  ┌─────────────┐  ┌──────────────────┐  ┌───────────────┐  │
│  │ ChatComplete│  │ChatCompleteDual  │  │ChatComplete   │  │
│  │             │  │                  │  │WithFallback   │  │
│  └──────┬──────┘  └────────┬─────────┘  └───────┬───────┘  │
│  ┌──────────────┐  ┌──────────────────────────┐           │
│  │ Audit        │  │ SetEmitter / Close       │           │
│  └──────┬───────┘  └──────────────────────────┘           │
└─────────┼──────────────────────────────────────────────────┘
          │
┌─────────▼──────────────────────────────────────────────────┐
│                      Scheduler                               │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │pickPrimary() │  │pickRoundRobin│  │Adaptive Semaphore  │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │Circuit Breaker│  │ProviderStatus│  │PromptInjector      │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │History/Cost  │  │RetryConfig   │  │Graceful Shutdown   │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
└─────────┬──────────────────┬────────────────────┬──────────┘
          │                  │                    │
     ┌────┴────┬─────────────┴────────┐           │
     ▼         ▼                      ▼           │
┌────────┐ ┌────────┐           ┌────────┐        │
│DeepSeek│ │  Kimi  │  ......   │ Generic│◄───────┘
└────────┘ └────────┘           └────────┘
     │          │                    │
     └──────────┴────────────────────┘
                    │
            go-openai SDK
                    │
              HTTP / SSE
```

### 2.2 关键组件

#### 2.2.1 Provider 接口

所有 Provider 必须实现统一接口：

```go
type Provider interface {
    Name() string
    Config() ProviderConfig
    ChatComplete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
    IsAvailable() bool
    ListModels(ctx context.Context) ([]ModelInfo, error)
}
```

**设计决策**：
- 使用 `ChatComplete` 而非业务方法，保持通用性
- `ListModels` 是特色功能，很多 SDK 不提供
- `IsAvailable` 包含熔断状态检查

#### 2.2.2 BaseProvider — 公共基础设施

```go
type BaseProvider struct {
    config ProviderConfig
    // Circuit breaker state
    failMu sync.Mutex; failCount int; failUntil time.Time
    // System prompt override
    sysMu sync.RWMutex; systemPromptOverride string
    // Event emitter
    emitterMu sync.RWMutex; emitter EventEmitter
}
```

包含：
- **熔断器**：连续 3 次失败 → 60 秒冷却
- **系统提示词覆盖**：支持运行时动态替换
- **模型列表查询**：统一通过 OpenAI-compatible `/models` 端点
- **结构化日志**：使用 `log/slog` 记录熔断、失败、恢复事件
- **零 panic 承诺**：所有 provider 调用路径均有 `recover()`，自定义 provider panic 不会崩溃进程

#### 2.2.3 Scheduler — 调度核心

**职责**：
1. Provider 注册与生命周期管理
2. 请求路由（Primary / Round-Robin）
3. 并发控制（Adaptive Semaphore）
4. Prompt 注入（请求到达 Provider 前自动注入匹配模板）
5. 调用历史记录（自动记录 Latency、Token 用量、成本）
6. 指数退避重试
7. 优雅关闭（拒绝新请求，释放资源）

**调度策略**：

| 场景 | 策略 | 说明 |
|---|---|---|
| 普通调用 | Primary | 第一个可用 Provider |
| Fallback | 顺序遍历 | 主 Provider 失败后逐个尝试，每次获取信号量许可 |
| Dual 模式 | Round-Robin | 选两个不同 Provider 并发调用 |
| Audit | Round-Robin | 主结果完成后用另一个 Provider 审计（各自独立 timeout + panic recover）|
| Stream | Primary | 通过 `OpenAIProvider.client` 复用 go-openai 的 SSE 流，支持 `Usage` 透传 |

#### 2.2.4 Adaptive Semaphore — 自适应并发限流

```go
type adaptiveSemaphore struct {
    mu      sync.Mutex
    inUse   atomic.Int32
    maxConc atomic.Int32
    waiters []waiter  // FIFO 队列
}
```

**特性**：
- 容量 = ∑(Provider.MaxConcurrent)
- 运行时可通过 `setMaxConc()` 动态调整
- FIFO 等待队列，公平调度，防止 goroutine 爆炸
- 支持 `context` 取消：等待中的 goroutine 在 ctx 取消时自动从等待队列移除，避免 lost-wakeup

#### 2.2.5 Circuit Breaker — 熔断器

```
成功 ──→ failCount = 0
  │
  └── 失败 ──→ failCount++
                  │
                  ├── failCount < 3  → 继续服务
                  └── failCount >= 3 → 开启熔断，冷却 60s
```

**设计理由**：
- 3 次阈值：平衡敏感度和稳定性
- 60 秒冷却：给 Provider 恢复时间，又不至于永久隔离
- 每个 Provider 独立计数：避免一个 Provider 的问题影响其他

#### 2.2.6 PromptInjector — Prompt 模板注入引擎

```go
type PromptTemplate struct {
    Provider string            // "*" 匹配全部 Provider
    Model    string            // "*" 匹配全部 Model
    Position string            // "system" | "prepend_user" | "append_user"
    Content  string            // 支持 {{var}} 变量替换
    Vars     map[string]string // 变量表
}
```

**设计决策**：
- 通配匹配 `*` 避免为每个 Provider/Model 重复写模板
- `{{var}}` 语法轻量，无需引入模板引擎依赖
- 三个注入位置覆盖绝大多数业务场景
- 注入发生在 Scheduler 分发前，对 Provider 透明

#### 2.2.7 History — 调用历史与成本追踪

```go
type CallRecord struct {
    ID string; Provider string; Model string
    Request ChatCompletionRequest; Response *ChatCompletionResponse
    Error string; LatencyMs int64; Timestamp time.Time
    Tags []string; FallbackFrom string; Cost float64
}

type ProviderStats struct {
    Provider string; TotalCalls int; FailedCalls int
    TotalLatencyMs int64; AvgLatencyMs int64; MaxLatencyMs int64
    TotalCost float64; Currency string
}
```

- 线程安全的 ring buffer，自动丢弃最旧记录
- 自动按 Provider 聚合统计：调用次数、失败次数、延迟分布、总成本
- 支持按标签过滤历史记录
- `Currency` 由首次见到的记录价格决定，保持单 Provider 单币种语义

---

## 3. 调用模式

### 3.1 单 Provider 调用（Primary）

```
User → Client.ChatComplete() → Scheduler.pickPrimary() → PromptInjector.Inject() → Provider.ChatComplete()
                                                              │
                                                              → History.Record() (success/fail + cost)
```

- 始终使用配置列表中第一个可用 Provider
- 请求先经过 PromptInjector，再发给 Provider
- 返回后自动记录 History（含成本计算）

### 3.2 自动 Fallback

```
User → Client.ChatCompleteWithFallback()
  ├── try Provider A (fail) ──→ release sem, try Provider B (success) → Record + Return
  └── try Provider A (success) ──→ Record + Return
```

- 逐个尝试所有可用 Provider 直到成功
- 每次尝试前获取信号量许可，失败时释放
- 记录 `FallbackFrom` 字段以便追溯
- 适合高可用性要求的生产环境

### 3.3 双 Provider 并发（Dual）

```
User → Client.ChatCompleteDual()
  ├── go Provider A (req.Clone()) ──┐
  └── go Provider B (req.Clone()) ──┴──→ Consensus() → DualResult
```

- 同时发给两个不同 Provider
- 各自使用 `req.Clone()` 避免数据竞争
- 返回两者结果 + 一致性判断（通过 `Consensus()` 归一化比较）
- 成本分别按 Provider 独立计算并记录

### 3.4 审计模式（Audit）

```
User → Client.Audit()
  ├── Step 1: Primary Provider 分析 → Result A
  └── Step 2: Different Provider (req.Clone()) 分析 → Result B
        └── Consensus()
              ├── 一致 → 返回 Primary（含共识标记）
              └── 分歧 → 返回 Primary，发射 audit:disagree 事件
```

- 需要至少 2 个可用 Provider
- 主调用和审计调用各自有独立 `context.WithTimeout` + defer recover
- 审计请求使用审计 Provider 自身的默认模型（避免跨 Provider 模型名不匹配）
- 审计失败时静默降级，返回主结果
- 适合对结果可信度要求极高的场景

### 3.5 流式调用（Stream）

```
User → Client.ChatCompleteStream()
  ├── Scheduler acquire sem
  ├── PromptInjector.Inject()
  └── chatCompleteStreamWithProvider()
        ├── OpenAIProvider.client.CreateChatCompletionStream()
        └── goroutine: for each SSE chunk → select { case wrapped <- msg; case <-ctx.Done(): return }
```

- 通过类型断言复用 `OpenAIProvider.client` 的 SSE 能力
- goroutine 包装 channel，内部通过 `select ctx.Done()` 实现 graceful cancel
- 消费端提前 break 时，应同时 cancel ctx 以确保 goroutine 退出

---

## 4. 流式支持（Streaming）

### 4.1 设计

```go
stream, err := client.ChatCompleteStream(ctx, req)
for chunk := range stream {
    if chunk.Err != nil { log.Fatal(chunk.Err) }
    if chunk.Chunk.FinishReason != "" {
        // 最终 chunk 可能携带 Usage（若 Provider 支持）
        fmt.Printf("Tokens: %d prompt, %d completion\n", chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens)
        break
    }
    fmt.Print(chunk.Chunk.Delta.Content)
}
```

- 基于 go-openai SDK 的 `CreateChatCompletionStream`
- 返回 channel，用户通过 range 读取
- 每个 chunk 包含 Delta（增量内容）和 FinishReason
- **Usage 透传**：最终 chunk 携带 Token 用量（需 Provider 支持 `stream_options: {"include_usage": true}`）
- 错误通过 `chunk.Err` 传递，而非 panic 或关闭 channel
- **生命周期一致性**：与其他方法一样，Close 后禁止调用，且自动应用 `s.timeout`

### 4.2 SSE 解析

提供 `ParseSSE()` 工具函数用于调试或代理场景：

```go
chunks := aoeo.ParseSSE(rawReader)
for chunk := range chunks {
    // process
}
```

---

## 5. 结果处理

### 5.1 JSON 容错提取

LLM 输出不稳定是常态，提供 3 层解析策略：

```go
var result MyStruct
err := aoeo.ExtractJSON(rawContent, &result)
```

| 策略 | 说明 |
|---|---|
| Layer 1 | 直接 `json.Unmarshal` |
| Layer 2 | 从 Markdown code fence (```json) 提取 |
| Layer 3 | 在文本中搜索第一个 JSON 对象（处理嵌套花括号） |

### 5.2 多结果合并

```go
merged := aoeo.MergeChoices(r1, r2, consensus)
```

- 一致时：返回第一个结果，Token 用量累加
- 分歧时：合并两者输出，标记分歧

---

## 6. 重试机制

### 6.1 指数退避

```go
type RetryConfig struct {
    MaxRetries int
    BaseDelay  time.Duration
    MaxDelay   time.Duration
    Multiplier float64
    Retryable  func(error) bool
}
```

**行为**：
- 延迟按 `BaseDelay * Multiplier^n` 增长，上限 `MaxDelay`
- `doRetry` 内部复制 `RetryConfig`，不修改调用者传入的原始值
- 默认 `IsRetryableError` 识别：timeout、502/503/504、rate limit、connection refused 等

### 6.2 温度兼容性重试（Kimi 特例）

Kimi `kimi-k2.6` 只接受 `temperature=1`。当返回 `400` 错误且消息体包含 "temperature" 时，OpenAIProvider 自动重试一次（去掉 `Temperature` 字段）。

---

## 7. 事件系统

```go
type EventEmitter interface {
    Emit(topic string, data ...any)
}
```

内置事件：

| Topic | 触发时机 |
|---|---|
| `provider:fail` | Provider 调用失败 |
| `provider:open` | Provider 熔断器开启（连续 3 次失败后） |
| `provider:recover` | Provider 熔断恢复 |
| `scheduler:fallback` | Fallback 被触发 |
| `audit:disagree` | 审计发现分歧 |

---

## 8. 与业务逻辑的边界

**AoEo 不做的事情**（明确边界）：

| 功能 | 归属 | 原因 |
|---|---|---|
| 对话历史维护 | 用户代码 | 状态管理应由调用方控制 |
| RAG 检索 | 用户代码 | 与向量数据库耦合 |
| Tool Calling | 未来可能扩展 | 各家实现差异大 |
| 权重路由 | 未来可能扩展 | 需质量评估数据 |
| Provider 插件机制 | 未来可能扩展 | 动态加载复杂度高 |

**AoEo 做的事情**：

| 功能 | 说明 |
|---|---|
| 多 Provider 注册与管理 | 配置即代码，自动过滤 nil provider |
| 请求路由与负载均衡 | Primary / Round-Robin |
| 熔断与恢复 | 自动，无需配置 |
| 并发控制 | 自适应信号量 |
| 模型列表查询 | 统一接口 |
| 连通性测试 | 内置，带 panic 恢复 |
| 双模型验证 | Dual / Audit 模式 |
| 流式响应 | SSE 支持，Usage 透传 |
| **Prompt 注入** | 通配匹配 + 变量替换（atomic 线程安全） |
| **成本统计** | 自动按 Provider 聚合 |
| **指数退避重试** | 可配置策略 |
| **优雅关闭** | `Close()` 安全释放 |
| **结构化日志** | `slog` JSON 输出 |
| **请求预验证** | `Validate()` 提前拦截无效参数 |
| **安全访问器** | `Content()` 避免 `Choices[0]` panic |

---

## 9. 扩展指南

### 9.1 添加新的内置 Provider

```go
func NewCustomProvider(config ProviderConfig) Provider {
    if config.Endpoint == "" {
        config.Endpoint = "https://api.custom.com/v1"
    }
    if config.Model == "" {
        config.Model = "custom-model"
    }
    if config.Name == "" {
        config.Name = "custom"
    }
    return NewOpenAIProvider(config)
}
```

然后在 `CreateProvider()` 中添加 case。

### 9.2 自定义 Provider 实现

如果某家 API 不完全兼容 OpenAI：

```go
type CustomProvider struct {
    *aoeo.BaseProvider
}

func (p *CustomProvider) ChatComplete(ctx context.Context, req aoeo.ChatCompletionRequest) (*aoeo.ChatCompletionResponse, error) {
    // 自定义 HTTP 调用逻辑
}
```

### 9.3 自定义调度策略

```go
scheduler := aoeo.NewScheduler(providers...)
// 直接操作 Scheduler 实现自定义路由
```

---

## 10. 演进路线

### Phase 1 — MVP（已完成）
- [x] 统一 Provider 接口
- [x] 4 大内置 Provider
- [x] 通用 OpenAI Provider
- [x] 熔断器 + 并发限流
- [x] Fallback + Dual + Audit
- [x] 模型列表查询
- [x] SSE 流式

### Phase 2 — 增强（已完成）
- [x] 指数退避重试（Retry with Backoff）
- [x] Token 用量追踪与成本估算
- [x] Prompt 注入系统（通配匹配 + 变量替换）
- [x] 结构化日志（`log/slog`）
- [x] 优雅关闭（Graceful Shutdown）

### Phase 3 — 生态
- [ ] Provider 健康检查（定时探测）
- [ ] 权重路由（按价格/速度/质量加权）
- [ ] CLI 工具（`aoeo list-models`, `aoeo test`）
- [ ] 与 Langfuse / LangSmith 集成
- [ ] Function Calling 抽象层
- [ ] Provider 插件机制（动态加载）

---

## 11. 从原项目提炼的变更记录

| 原项目 (xdr-Desk) | AoEo SDK | 变更原因 |
|---|---|---|
| `Analyze()` 业务方法 | `ChatComplete()` 通用方法 | 去业务化 |
| `AnalysisResult` (含 verdict/confidence) | `ChatCompletionResponse` | 去业务化 |
| Skill 系统 | 移除 | 业务逻辑 |
| RAG 集成 | 移除 | 业务逻辑 |
| MCP 工具调用 | 移除 | 业务逻辑 |
| Hooks (evidence-chain, mitre) | 移除 | 业务逻辑 |
| Learning / 索引 | 移除 | 业务逻辑 |
| Langfuse 集成 | 简化 | 降低依赖 |
| Wails EventEmitter | 通用 EventEmitter 接口 | 去框架绑定 |
| SQLite 配置持久化 | 内存 Config | 去存储依赖 |
| JSON 4 层解析 + 证据校验 | 保留 `ExtractJSON` | 通用工具 |
| Panic Recovery | 保留 + 命名返回值 | 稳定性必备 |
| 无成本统计 | 新增 `cost.go` + `History.Cost` | 生产监控必备 |
| 无 Prompt 注入 | 新增 `prompt.go` | 减少业务代码侵入 |
| `log.Printf` | `slog` JSON 日志 | 可观测性 |

---

## 12. 生产注意事项

### 12.1 资源安全

- **Graceful Shutdown**：始终 `defer client.Close()` 或显式调用 `Close()`，确保 goroutine 和资源被正确释放
- **Stream Cancel**：流式消费端提前退出时，应同时 `cancel()` context，否则内部 goroutine 可能泄漏
- **Context 超时**：为每次调用设置合理的 `context.WithTimeout()`，避免 Provider 无响应时 goroutine 长期阻塞
- **Nil 安全访问**：优先使用 `resp.Content()` 而非 `resp.Choices[0].Message.Content`，避免空响应 panic
- **请求预验证**：生产环境建议在发送前调用 `req.Validate()`，提前发现参数错误
- **并发修改配置**：`SetTimeout()`、`SetPromptInjector()` 均为线程安全，可在运行时动态调整

### 12.2 并发与配额

- **MaxConcurrent**：根据各平台 RPM/TPM 限制设置，过小影响吞吐，过大触发限流
- **信号量等待**：FIFO 队列保证公平性，但长队可能导致尾部延迟增加，需监控

### 12.3 数据安全

- API Key 通过环境变量或 Vault 注入，**绝不**硬编码
- `SkipTLSVerify` 仅用于内网测试环境，生产环境必须关闭

---

## 13. 总结

AoEo 的设计目标非常聚焦：**让 Go 开发者用 10 行代码获得生产级的多模型聚合调度能力**。

它不试图成为又一个全功能 LLM 框架，而是作为基础设施层存在：
- 上游：对接任何业务逻辑（XDR、客服、代码生成...）
- 下游：对接任何 OpenAI-compatible API（国内外数十家）

这种"薄而聚焦"的设计使得 AoEo 可以被嵌入到任何 Go 项目中，无论其业务领域是什么。
