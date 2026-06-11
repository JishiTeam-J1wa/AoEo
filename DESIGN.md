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
│  │    Router    │  │HealthCheck  │  │Adaptive Semaphore  │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │Circuit Breaker│  │ProviderStatus│  │PromptInjector      │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │History/Cost  │  │RetryConfig   │  │InterceptorChain    │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │Graceful Shutd│  │EnvConfig     │  │Proxy/HTTPClient    │  │
│  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │   Storage    │  │              │  │                    │  │
│  │ SQLite/MySQL │  │              │  │                    │  │
│  │   /Postgres  │  │              │  │                    │  │
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
    ChatCompleteStream(ctx context.Context, req ChatCompletionRequest) (<-chan StreamCompletionResponse, error)
    IsAvailable() bool
    ListModels(ctx context.Context) ([]ModelInfo, error)
    SetEmitter(e EventEmitter)
    HealthCheck(ctx context.Context) error
}
```

**设计决策**：
- 使用 `ChatComplete` + `ChatCompleteStream` 覆盖同步和流式两种调用模式
- `ListModels` 是特色功能，很多 SDK 不提供
- `IsAvailable` 包含熔断状态检查（原子操作，无锁）
- `SetEmitter` 支持 Provider 生命周期事件订阅
- `HealthCheck` 支持后台探活（轻量 HTTP 探测，5s 超时）

#### 2.2.2 BaseProvider — 公共基础设施

```go
type BaseProvider struct {
    config ProviderConfig
    // Circuit breaker state (atomic)
    failCount atomic.Int32
    failUntil atomic.Int64 // UnixNano
    // System prompt override (atomic pointer)
    sysPrompt atomic.Pointer[string]
    // Event emitter (atomic.Value)
    emitter atomic.Value
    // Runtime health metrics (sliding window)
    healthMu     sync.RWMutex
    healthWindow [20]healthEntry
    healthHead   int
    healthCount  int
    healthLatest atomic.Pointer[ProviderHealth]
}
```

包含：
- **熔断器**：连续 3 次失败 → 60 秒冷却
- **运行时健康追踪**：20 次调用滑动窗口，记录延迟/成功率/连续失败（`RecordHealthCheck` / `RecordCallResult`）
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
    Tags []string; FallbackFrom string; Cost float64; Currency string
}

type ProviderStats struct {
    Provider string; TotalCalls int; FailedCalls int
    TotalLatencyMs int64; AvgLatencyMs int64; MaxLatencyMs int64
    TotalCost float64; Currency string
}
```

- 线程安全的 ring buffer（固定长度 O(maxSize)，无 append realloc），自动丢弃最旧记录
- 可选持久化：通过 `SetStorage(core.Storage)` 将调用历史写入 SQLite/MySQL/Postgres
- 自动按 Provider 聚合统计：调用次数、失败次数、延迟分布、总成本
- 支持按标签过滤历史记录
- `Currency` 由首次见到的记录价格决定，保持单 Provider 单币种语义

#### 2.2.8 Interceptor — 请求/响应/流式拦截器

```go
type Interceptor struct {
    BeforeRequest    func(ctx context.Context, req *ChatCompletionRequest) error
    AfterResponse    func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error)
    AfterStreamChunk func(ctx context.Context, req ChatCompletionRequest, chunk *StreamChunk) error
    AfterStreamDone  func(ctx context.Context, req ChatCompletionRequest, err error) error
}
```

**职责**：
- `BeforeRequest`：请求发送前执行，可修改请求或中止调用
- `AfterResponse`：Provider 返回后执行，可转换响应/错误
- `AfterStreamChunk`：流式响应每个 chunk 转发前执行，可修改 chunk 或中止流
- `AfterStreamDone`：流式响应结束时执行，用于清理/日志/统计

**设计决策**：
- 链式执行，多个拦截器按注册顺序调用
- `BeforeRequest` / `AfterStreamChunk` 遇到错误立即短路
- `AfterResponse` / `AfterStreamDone` 每个钩子可修改返回值，对调用方完全透明
- 使用 `atomic.Pointer[[]Interceptor]` 保证线程安全，支持运行时动态替换
- 与 Scheduler 解耦：拦截器属于调度选项，不影响 Provider 实现

#### 2.2.9 Router — 可插拔路由策略

```go
type Router interface {
    Select(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) (int, error)
    SelectSequence(ctx context.Context, candidates []ProviderStatus, req ChatCompletionRequest) ([]int, error)
}
```

**内置策略**：

| 策略 | 实现 | 特点 |
|---|---|---|
| PrimaryRouter | 选第一个可用 | 默认行为，零开销 |
| RoundRobinRouter | `atomic.Uint64` 轮询 | O(1)，自动跳过不可用 |
| RandomRouter | `atomic.Uint64` 伪随机 | 简单负载分散 |
| WeightedRouter | 按健康指标加权 | 支持 latency / success-rate / combined 策略 |
| SingleProviderRouter | 强制指定 Provider | CLI 调试和定向调用 |

**设计决策**：
- `Select` 用于单次调用（`ChatComplete`）
- `SelectSequence` 用于 Fallback / Dual 的 Provider 排序
- 使用 `atomic.Pointer[core.Router]` 支持运行时热切换
- Scheduler 的 `PickPrimaryProvider` / `PickProviderRoundRobin` 保留为兼容方法，内部委托给 Router

#### 2.2.10 HealthCheck — 后台探活与 Provider 健康指标

Provider 接口：

```go
type Provider interface {
    // ...
    HealthCheck(ctx context.Context) error
}
```

- `BaseProvider` 默认实现：HTTP GET Endpoint（5s 超时），结果自动记录到健康窗口
- `OpenAIProvider` 可覆盖为更具体的检查

**Provider 主动健康指标**：

```go
type ProviderHealth struct {
    LastCheckAt      time.Time `json:"last_check_at"`
    LastLatencyMs    int64     `json:"last_latency_ms"`
    AvgLatencyMs     int64     `json:"avg_latency_ms"`
    SuccessRate      float64   `json:"success_rate"`       // 0.0~1.0
    ConsecutiveFails int       `json:"consecutive_fails"`
    TotalChecks      int       `json:"total_checks"`
}
```

- 每次 `HealthCheck` 和实际 API 调用后自动更新
- 20 次滑动窗口，O(1) 更新，原子快照读取
- `ProviderStatus` 携带 `Health` 字段，供 Router 做加权决策

Scheduler 后台 goroutine：

```go
s.StartHealthCheck(30 * time.Second) // 每 30s 探测一次
s.SetHealthCheckInterval(0)          // 关闭探测
```

**设计决策**：
- 默认关闭，避免测试/短生命周期场景泄漏 goroutine
- 健康检查失败直接触发熔断器 `RecordFailure`，加速 Provider 进入冷却
- 健康检查成功触发 `RecordSuccess`，帮助 Provider 恢复
- 失败调用 `RecordFailure()`，成功调用 `RecordSuccess()`，复用熔断器逻辑
- 关闭 Scheduler 时自动 `stopHealthCheck()`，通过 `sync.WaitGroup` 保证 goroutine 退出
- `healthCheckStop` 通道通过参数传递给 goroutine，避免 data race

#### 2.2.11 Proxy / HTTPClient — 网络层定制

每个 Provider 独立配置网络出口：

```go
type ProviderConfig struct {
    HTTPClient *http.Client  // 完全自定义 HTTP 行为（追踪、Mock、特殊 TLS）
    Proxy      string        // HTTP / SOCKS5 代理 URL
}
```

**优先级**：自定义 `HTTPClient` > `Proxy` 字段 > 环境变量 `HTTP_PROXY`

**实现**：`buildHTTPClient()` 在 `NewOpenAIProvider` 时组装最终 `*http.Client`：
- 若只提供了 `Proxy`，克隆 `http.DefaultTransport` 并注入 `ProxyURL`
- 若提供了自定义 `HTTPClient`，保留其 `CheckRedirect`/`Jar`，覆盖 Transport 上的 `Proxy` 和 `TLSConfig`
- 支持 `SkipTLSVerify` 与自定义 Transport 共存

#### 2.2.12 CLI 工具

```go
go install github.com/JishiTeam-J1wa/AoEo/cmd/aoeo@latest
```

**子命令**：

| 命令 | 说明 | 示例 |
|---|---|---|
| `list-models` | 列出所有 Provider 的可用模型 | `aoeo list-models` |
| `test` | 对所有 Provider 执行健康检查 | `aoeo test` |
| `status` | 查看 Provider 状态、延迟、成功率 | `aoeo status` |
| `chat` | 单次对话 | `aoeo chat -message "Hello" -provider deepseek` |
| `stream` | 流式对话 | `aoeo stream -message "Explain Go"` |

**实现**：Go 标准库 `flag`，零外部依赖，复用 `LoadConfigFromEnv()` 加载配置。

#### 2.2.13 Storage — 持久化存储层

```go
type Storage interface {
    RecordCall(ctx context.Context, r CallRecord) error
    GetCalls(ctx context.Context, limit int) ([]CallRecord, error)
    GetCallsByTag(ctx context.Context, tag string, limit int) ([]CallRecord, error)
    GetCallsByProvider(ctx context.Context, provider string, limit int) ([]CallRecord, error)
    GetProviderStats(ctx context.Context) (map[string]ProviderStats, error)

    RecordAudit(ctx context.Context, e AuditEvent) error
    GetAudits(ctx context.Context, limit int) ([]AuditEvent, error)

    CreateMapping(ctx context.Context, m PrivacyMapping) error
    FindFake(ctx context.Context, sessionID, original string) (string, bool, error)
    FindOriginal(ctx context.Context, sessionID, fake string) (string, bool, error)
    GetMappings(ctx context.Context, sessionID string) ([]PrivacyMapping, error)
    DeleteMappingsBySession(ctx context.Context, sessionID string) error
    CleanupMappings(ctx context.Context, before time.Time) error

    Close() error
}
```

**设计决策**：
- 统一接口，三种后端：SQLite（纯 Go，零 CGO）、MySQL、PostgreSQL
- Schema 统一：calls（调用历史）、audits（审计日志）、privacy_mappings（隐私映射）
- History 通过 `SetStorage()` 注入，Record 时异步写入，查询时优先读数据库
- Privacy Gateway 通过 `GatewayConfig.Storage` 注入，支持跨会话持久化映射关系
- 占位符自适应：SQLite/MySQL 用 `?`，Postgres 用 `$1, $2...`

#### 2.2.13 EnvConfig — 环境变量配置源

```go
cfg := aoeo.LoadConfigFromEnv()
```

**设计决策**：
- 零配置启动：容器化/K8s 场景下无需修改代码即可切换 Provider
- 统一前缀 `AOEO_PROVIDER_{N}_*`，索引从 0 开始，遇到空位终止扫描
- 支持字段：NAME、API_KEY、ENDPOINT、MODEL、MAX_CONCURRENT、SKIP_TLS_VERIFY、PROXY
- 提供 `LoadConfigFromEnvWithPrefix()` 支持自定义前缀，避免与其他应用冲突
- `SetEnvConfig()` / `UnsetEnvConfig()` 用于测试和 CLI 工具，不推荐给生产 secret 管理

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
  ├── Router.Select() → pick provider
  ├── PromptInjector.Inject()
  ├── InterceptorChain.ApplyBefore()
  └── p.ChatCompleteStream() (Provider 接口)
        └── goroutine:
              ├── for each chunk:
              │     ├── InterceptorChain.ApplyAfterStreamChunk()
              │     └── select { case wrapped <- msg; case <-ctx.Done(): return }
              └── defer InterceptorChain.ApplyAfterStreamDone()
```

- Provider 接口原生支持 `ChatCompleteStream`，无需类型断言
- `OpenAIProvider` 使用底层 `go-openai` SSE 能力，其他 Provider 可自定义实现
- goroutine 包装 channel，固定缓冲（16）解耦生产/消费速率
- 流式拦截器 `AfterStreamChunk` 可在每个 chunk 转发前修改或中止
- `AfterStreamDone` 在流结束时触发，用于统计/清理
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
| Tool Calling | **已实现** | 统一 `Tool`/`ToolCall`/`FunctionDefinition` 抽象 |
| 权重路由 | **已实现** | 基于运行时健康指标加权选择 |
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
| **拦截器** | `BeforeRequest` / `AfterResponse` Hook |
| **定向代理** | 按 Provider 独立配置 Proxy / HTTPClient |
| **环境变量配置** | `LoadConfigFromEnv()` 零代码启动 |
| **测试覆盖** | 230 个单元测试，`-race` 干净，整体覆盖率 71.7% |

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

### Phase 3 — 网络与可观测性（已完成）
- [x] 定向 AI API 代理（Proxy / HTTPClient）
- [x] 拦截器机制（Interceptor Chain）
- [x] 环境变量配置（`LoadConfigFromEnv`）

### Phase 3.5 — 架构偿债（已完成）
- [x] Provider 接口新增 `ChatCompleteStream`，解耦 `OpenAIProvider` 类型断言
- [x] 流式路径支持 `BeforeRequest` / `AfterStreamChunk` / `AfterStreamDone` interceptor
- [x] 修复流式 goroutine 泄漏（固定缓冲 + 安全 select）
- [x] History Ring Buffer 重构（O(maxSize) 恒定空间，消除 append realloc）
- [x] BaseProvider 原子化（`failCount` / `failUntil` / `emitter` 全部原子操作）
- [x] Router 接口抽象（`PrimaryRouter` / `RoundRobinRouter` / `RandomRouter`，热切换）
- [x] Provider 后台健康检查（`HealthCheck` 接口 + 定时 goroutine，复用熔断器）
- [x] 补全流式 / buildRecord / Client / Retry / Router / HealthCheck 测试，覆盖率 71.7% → 70.8%

### Phase 3.6 — 隐私安全网关（已完成）
- [x] **纯 AI 模型检测**：删除本地规则引擎，完全依赖 HuggingFace NER 模型检测 PII
- [x] **可逆伪匿名化（Pseudonymization）**：原始值 ↔ 伪造值映射，Pebble KV 持久化
- [x] **伪造数据生成器**（IP、域名、姓名、电话、身份证号、密钥等）
- [x] **AoEo Gateway 集成**（BeforeRequest / AfterResponse / AfterStreamChunk / AfterStreamDone）
- [x] **会话隔离 + TTL 清理**
- [x] **Sidecar 微服务化**：FastAPI + HuggingFace，独立部署
- [x] **FailOpen**：sidecar 故障时可选透传，不阻断业务
- [x] **全链路日志**：Go 侧（检测/替换/还原/失败）+ Sidecar 侧（spans/latency）

### Phase 3.7 — 隐私网关微服务化优化（已完成）
- [x] **批量检测 API**：`DetectBatch`，N 条 message 合并为 1 次 HTTP 往返
- [x] **智能负载均衡（LeastLatency）**：EWMA 延迟 + 加权路由，自动选择最快 Sidecar
- [x] **HTTP/2 强制启用**：`ForceAttemptHTTP2: true`，连接复用
- [x] **连接预热**：启动时自动预热 TCP / HTTP/2 连接，消除首包延迟
- [x] **客户端负载均衡**：内置 `LoadBalancedClient`，无需 Nginx 即可多实例部署
- [x] **健康检查 + 故障剔除/恢复**：每 10 秒自动探测，故障节点自动隔离

### Phase 4 — 生态扩展（已完成）
- [x] 权重路由（按延迟/成功率加权，`WeightedRouter`）
- [x] Provider 主动健康检查心跳（20 次滑动窗口，`ProviderHealth`）
- [x] Function Calling 抽象层（`Tool`/`ToolCall`/`FunctionDefinition`）
- [x] CLI 工具（`aoeo list-models` / `test` / `status` / `chat` / `stream`）

### Phase 5 — 未来方向
- [ ] 权重路由扩展：按成本/自定义评分函数加权
- [ ] 与 Langfuse / LangSmith 集成
- [ ] Provider 插件机制（动态加载）
- [ ] 分布式调度：多节点状态同步
- [ ] Sidecar 模型热更新：无需重启更换检测模型
- [ ] 批量检测并行化：Sidecar 端多 GPU 并行 inference

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
