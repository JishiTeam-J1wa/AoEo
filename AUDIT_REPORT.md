# AoEo 深度审计报告

> 审计日期：2026-05-28  
> 审计版本：v1.1  
> 审计范围：全部 18 个 `.go` 文件

---

## 一、P0 级问题（已修复）

### 1.1 Provider 接口缺失 Config() 方法

**问题**：代码中大量使用 `p.(interface{ GetConfig() ProviderConfig })` 这种反射式类型断言来获取 Provider 配置，既丑陋又不安全。自定义 Provider 如果没有这个方法会导致 Model 默认值无法填充。

**修复**：在 `Provider` 接口中直接添加 `Config() ProviderConfig` 方法，所有内置 Provider 通过嵌入的 `BaseProvider` 自动实现。

```go
// 修复前
type Provider interface {
    Name() string
    ChatComplete(...)
    IsAvailable() bool
    ListModels(...)
}

// 修复后
type Provider interface {
    Name() string
    Config() ProviderConfig   // ← 新增
    ChatComplete(...)
    IsAvailable() bool
    ListModels(...)
}
```

### 1.2 ChatCompleteDual 并发安全隐患

**问题**：`reqCopy := req` 是 Go 的浅拷贝。`ChatCompletionRequest.Messages` 是切片（引用类型），底层数组共享。如果调用方在 goroutine 运行时修改了 Messages 内容，会导致 data race。

**修复**：添加 `ChatCompletionRequest.Clone()` 深拷贝方法，Dual 模式的两个 goroutine 各自使用独立副本。

```go
func (req ChatCompletionRequest) Clone() ChatCompletionRequest {
    cloned := req
    cloned.Messages = make([]Message, len(req.Messages))
    copy(cloned.Messages, req.Messages)
    return cloned
}
```

### 1.3 Client.SetEmitter 非线程安全

**问题**：`SetEmitter` 没有锁保护。如果 goroutine A 调用 `SetEmitter` 同时 goroutine B 调用 `ChatCompleteWithFallback`，存在 data race。

**修复**：添加 `sync.RWMutex` 保护 emitter，所有发射事件通过 `emit()` 辅助方法。

### 1.4 Stream goroutine 泄漏风险

**问题**：流式 goroutine 在 `ch <- StreamCompletionResponse` 时，如果调用方丢弃了 channel（不读取），goroutine 会永远阻塞在 channel 发送上，导致泄漏。

**修复**：
1. 所有 channel 发送都包裹在 `select { case <-ctx.Done(): return; case ch <- ...: }` 中
2. 添加 `sync.WaitGroup` 确保 goroutine 生命周期可追踪
3. 在循环开头检查 `ctx.Done()`

### 1.5 TestProvider defer cancel 在循环内

**问题**：`defer cancel()` 放在 `for` 循环内，cancel 函数在**外层函数返回时**才执行，而非循环迭代结束时。虽然此场景下不会真正泄露（因为找到匹配项就 return 了），但是不良实践。

**修复**：改为显式 `cancel()` 调用。

### 1.6 Client.ChatCompleteWithFallback 事件逻辑错误

**问题**：原代码在 `err != nil` 时才触发 fallback 事件，但 `ChatCompleteWithFallback` 成功时 `err == nil`，所以事件永远不会触发。

**修复**：先尝试 primary，失败后触发事件，再调用 fallback。

### 1.7 Choices[0] bounds check 缺失

**问题**：`ChatCompleteDual` 中直接访问 `dual.Result1.Choices[0]`，如果某个 Provider 返回了 Choices 为空的响应会 panic。

**修复**：添加 `len(dual.Result1.Choices) > 0` 检查。

---

## 二、P1 级性能优化（已实施）

### 2.1 Semaphore 快速路径 atomic 优化

**问题**：原 `adaptiveSemaphore` 每次 acquire 都获取 `sync.Mutex`，在高并发下锁竞争严重。

**优化**：使用 `atomic.Int32` 实现无锁快速路径，只有在容量不足时才回退到锁队列。

```go
// 快速路径：CAS 无锁
for {
    current := a.inUse.Load()
    maxC := a.maxConc.Load()
    if current+int32(n) > maxC { break }
    if a.inUse.CompareAndSwap(current, current+int32(n)) { return }
}
// 慢路径：FIFO 等待队列
```

**预期收益**：低竞争场景下 acquire/release 延迟从 ~100ns 降至 ~10ns。

### 2.2 availableProviders 缓存

**问题**：每次调用 `availableProviders()` 都遍历全部 Provider 检查 `IsAvailable()`。在高 QPS 下，这个遍历是热点。

**优化**：添加 1 秒 TTL 的 atomic 缓存，用 `atomic.Pointer[[]Provider]` 实现无锁读取。

```go
func (s *Scheduler) availableProviders() []Provider {
    if cached := s.availCache.Load(); cached != nil {
        if time.Since(cacheTime) < s.availCacheTTL {
            return *cached
        }
    }
    // 重新计算并更新缓存
}
```

### 2.3 超时可配置

**问题**：所有调用的超时都硬编码为 45 秒，无法根据场景调整。

**优化**：`Scheduler` 添加 `timeout` 字段，通过 `WithTimeout()` 选项配置。

---

## 三、P2 级功能增强（已实施）

### 3.1 指数退避重试（Retry）

**新增 `retry.go`**：
- 可配置 MaxRetries / BaseDelay / MaxDelay / Multiplier
- 智能判断错误是否可重试（timeout、503、connection refused 等）
- 与 context 联动，取消时立即终止重试

```go
cfg := DefaultRetryConfig()
cfg.MaxRetries = 3
client, _ := aoeo.NewClient(config, aoeo.WithRetry(cfg))
```

### 3.2 调用历史记录（History）

**新增 `history.go`**：
- 记录每次调用的 Provider、Model、Latency、Error、Tags
- 支持按 Tag / Provider 过滤查询
- 自动统计 Provider 成功率、平均延迟、最大延迟
- 环形缓冲区，自动淘汰旧记录

```go
hist := aoeo.NewHistory(500)
client, _ := aoeo.NewClient(config, aoeo.WithHistory(hist))

// 查询
for _, r := range hist.RecordsByTag("production") {
    fmt.Printf("%s: %dms\n", r.Provider, r.LatencyMs)
}

// 统计
stats := client.Stats()
for name, s := range stats {
    fmt.Printf("%s: %d calls, %.1fms avg, %d failed\n",
        name, s.TotalCalls, s.AvgLatencyMs, s.FailedCalls)
}
```

### 3.3 标签支持（Tags）

**新增 `ChatCompletionRequest.Tags`**：
- 给每次调用打标签，用于后续分类、过滤、统计
- Dual 模式自动添加 `"dual"` 标签

```go
req := aoeo.BuildRequest(messages, aoeo.WithTemperature(0.7))
req.Tags = []string{"production", "v2-prompt"}
resp, err := client.ChatComplete(ctx, req)
```

### 3.4 输入验证（ValidateConfig）

**新增 `config.go`**：
- 验证 Name / APIKey / Endpoint / Model 是否为空
- 验证 Endpoint 是否以 http:// 或 https:// 开头
- 验证 URL 格式是否合法
- 验证 MaxConcurrent >= 0

```go
issues := aoeo.ValidateConfig(cfg)
// 或
issuesMap := cfg.Validate() // map[providerName][]issue
```

---

## 四、展示流程 & UX 设计建议

### 4.1 分析历史展示

基于 `History` 和 `CallRecord`，可以构建以下展示维度：

| 维度 | 数据来源 | 展示形式 |
|---|---|---|
| 时间线 | `CallRecord.Timestamp` | 时间轴列表 |
| Provider 对比 | `CallRecord.Provider` + `LatencyMs` | 柱状图/折线图 |
| 成功率 | `Stats().FailedCalls / TotalCalls` | 仪表盘 |
| 标签分布 | `CallRecord.Tags` | 饼图/词云 |
| 调用详情 | `Request` + `Response` + `Error` | 折叠面板 |

### 4.2 标签布局建议

```
┌─────────────────────────────────────────┐
│  [全部] [生产环境] [测试] [dual模式] [审计] │  ← 标签过滤器
├─────────────────────────────────────────┤
│  🔴 DeepSeek  245ms  ✓  2026-05-28      │
│     Tags: [production] [v2-prompt]      │
│     Request: "Explain Go interfaces..." │
│     Response: "Go interfaces are..."    │
├─────────────────────────────────────────┤
│  🟢 Kimi      189ms  ✓  2026-05-28      │
│     Tags: [production] [dual]           │
│     ...                                 │
└─────────────────────────────────────────┘
```

### 4.3 结果对比展示（Dual 模式）

```
┌──────────────────────┬──────────────────────┐
│   DeepSeek           │   Kimi               │
│   245ms |  tokens    │   189ms |  tokens    │
├──────────────────────┼──────────────────────┤
│ Go interfaces are    │ In Go, an interface  │
│ a type that defines  │ defines a set of     │
│ a set of methods...  │ method signatures... │
├──────────────────────┼──────────────────────┤
│      [共识 ✓] 内容相似度: 85%              │
└──────────────────────┴──────────────────────┘
```

### 4.4 Provider 健康面板

```
┌──────────┬──────────┬─────────┬────────┬─────────┐
│ Provider │ Status   │ Model   │ Latency│ Success │
├──────────┼──────────┼─────────┼────────┼─────────┤
│ DeepSeek │ 🟢 正常  │ v4-pro  │ 245ms  │ 98.5%   │
│ Kimi     │ 🟢 正常  │ k2.6    │ 189ms  │ 99.2%   │
│ GLM      │ 🟡 熔断  │ glm-5.1 │ —      │ —       │
│ Qwen     │ 🔴 离线  │ qwen3.7 │ —      │ 0%      │
└──────────┴──────────┴─────────┴────────┴─────────┘
```

---

## 五、仍建议后续完善（未实施）

| 优先级 | 功能 | 说明 |
|---|---|---|
| P1 | 流式 Fallback | `ChatCompleteStream` 目前不支持 Fallback |
| P1 | Provider 健康心跳 | 定时自动探测 Provider 可用性，而非被动检查 |
| P2 | Trace ID | 为每次调用生成唯一 ID，便于链路追踪（已在 7.2.1 实施） |
| P2 | 成本估算 | 根据各平台定价计算调用成本（已在 Pricing/Cost 实施） |
| P3 | CLI 工具 | `aoeo list-models`, `aoeo test`, `aoeo history` |
| P3 | Web UI | 基于 History 的轻量 Web 面板 |

---

## 六、变更统计

| 类别 | 数量 |
|---|---|
| 新增文件 | 4 (`config.go`, `history.go`, `retry.go`, `AUDIT_REPORT.md`) |
| 修改文件 | 10+ |
| 修复 Bug | 7 |
| 性能优化 | 3 |
| 功能增强 | 4 |
| 新增测试 | 12 个 |
| 编译状态 | ✅ 通过 |
| 测试状态 | ✅ 12/12 通过 |


---

## 七、第三轮审计（2026-05-29）— 第三方 SDK 可用性

> 审计视角：从 Web / CLI / Desktop 应用集成方的角度，检查接口稳定性、零 panic 承诺、资源控制和向后兼容。

### 7.1 P0 级问题（已修复）

#### 7.1.1 `s.timeout` 数据竞争

**问题**：`SetTimeout()` 使用 `sync.Mutex` 写入，但 `ChatComplete` / `ChatCompleteWithFallback` / `ChatCompleteDual` / `Audit` / `Stream` 均直接读取 `s.timeout`，race detector 必报。

**修复**：`time.Duration` → `atomic.Int64`，所有读点改为 `time.Duration(s.timeout.Load())`。

影响文件：`scheduler.go`, `stream.go`, `audit.go`

#### 7.1.2 `s.promptInjector` 数据竞争

**问题**：`SetPromptInjector()` 直接赋值指针，`ChatComplete` 等并发读取，无同步。

**修复**：`*PromptInjector` → `atomic.Pointer[PromptInjector]`，读用 `.Load()`，写用 `.Store()`。

影响文件：`scheduler.go`, `prompt.go`, `stream.go`, `audit.go`

#### 7.1.3 `http.DefaultTransport` 类型断言 panic

**问题**：`NewOpenAIProvider` 中 `http.DefaultTransport.(*http.Transport).Clone()` 若消费者替换默认 Transport 为非 `*http.Transport`，直接 panic。

**修复**：改为 guarded type assertion：
```go
if tr, ok := http.DefaultTransport.(*http.Transport); ok {
    cloned := tr.Clone()
    ...
}
```

#### 7.1.4 `ChatCompleteStream` 生命周期不一致

**问题**：
1. 缺少 `checkClosed()`，Close 后仍可发起 Stream
2. 无 `s.timeout` 包装，行为与其他方法不一致
3. provider 不可用时返回 `fmt.Errorf("no available provider")` 而非 `ErrNoAvailableProvider`

**修复**：统一添加 closed 检查、timeout 包装、sentinel error。

#### 7.1.5 `ChatCompleteWithFallback` panic 时资源泄漏

**问题**：内层循环中 `cancel()` 和 `sem.Release()` 不是 defer，provider panic 时两者均不执行，导致信号量永久泄漏。

**修复**：重构为内层函数 + `defer recover/cancel/sem.Release`，并补全 retry 逻辑。

#### 7.1.6 `ChatComplete` / `TestProvider` panic 无恢复

**问题**：自定义 provider 若未实现 recover，panic 直接穿透到调用方。

**修复**：在 `ChatComplete` 和 `TestProvider` 中分别添加 `defer recover()`，捕获后转为 error 返回。

#### 7.1.7 `AvailableProviders` 缓存返回可变切片

**问题**：消费者修改返回的 `[]Provider` 会直接污染内部缓存。

**修复**：返回 `copyProviders()` 副本。

### 7.2 P1 级可用性增强（已实施）

#### 7.2.1 唯一请求 ID

**问题**：`buildRecord` 使用 `start.UnixNano()` 生成 ID，高并发下同 provider 同一微秒可能冲突。

**修复**：Scheduler 增加 `atomic.Uint64 reqID`，ID 格式改为 `%s-%d-%d`（provider + nanoseq + atomic counter）。

#### 7.2.2 请求预验证 `Validate()`

**新增**：`ChatCompletionRequest.Validate() []string`

验证项：
- Messages 非空
- 每条 Message 的 Role / Content 非空
- Temperature ∈ [0, 2]
- TopP ∈ [0, 1]
- MaxTokens ≥ 0

调用方可提前拦截无效请求，减少无效 API 调用：
```go
if issues := req.Validate(); len(issues) > 0 {
    return fmt.Errorf("invalid request: %v", issues)
}
```

#### 7.2.3 安全内容访问 `Content()`

**新增**：`ChatCompletionResponse.Content() string`

返回第一个 Choice 的 Message.Content，自动处理 nil response 和空 Choices，避免调用方因直接访问 `resp.Choices[0]` 导致 panic。

#### 7.2.4 Stream Usage 透传

**新增**：`StreamCompletionResponse.Usage`

在支持 `stream_options: {"include_usage": true}` 的 Provider（如 OpenAI）上，最终 chunk 会携带 Token 用量统计。`stream.go` 中从 `go-openai` 的 `response.Usage` 映射到 `core.Usage`。

#### 7.2.5 防御性拷贝

- `WithStop(stop []string)` 现在复制切片，防止调用方后续修改影响已构建的请求
- `CostString` 不再修改本地 `Pricing` 副本，改为读取后使用局部变量

#### 7.2.6 `NewScheduler` 过滤 nil provider

**问题**：`NewClientWithProviders(nil)` 或 variadic slice 中含 nil 直接 panic。

**修复**：`NewScheduler` 遍历中 `if p == nil { continue }`，只保留 valid providers。

### 7.3 测试覆盖

| 新增测试 | 验证目标 |
|---|---|
| `TestChatCompleteStream_Closed` | Stream 关闭后返回 `ErrSchedulerClosed` |
| `TestChatCompleteStream_NoAvailableProvider` | Stream 无可用 provider 返回 sentinel error |
| `TestSetTimeout_RaceSafe` | 100 并发 goroutine 同时 SetTimeout + ChatComplete，race 干净 |
| `TestAvailableProviders_Copy` | 修改返回 slice 不影响缓存 |
| `TestChatComplete_PanicRecovery` | panic 后 semaphore 释放，二次请求不挂死 |
| `TestChatCompleteWithFallback_PanicRecovery` | fallback 路径 panic 后自动切换下一 provider |
| `TestTestProvider_PanicRecovery` | 连通性测试 panic 优雅降级为 error |
| `TestAudit_NoAvailableProvider` | Audit 无可用 provider 返回 `ErrNoAvailableProvider` |
| `TestChatCompletionRequest_Validate` | 预验证拦截各类无效请求 |
| `TestChatCompletionResponse_Content` | nil / 空 Choices / 正常内容 均安全 |
| `TestWithStop_DefensiveCopy` | 原始 slice 修改不影响请求 |
| `TestUsage_CostString_DefaultCurrency` | 空 Currency 时默认 CNY 且不修改入参 |

### 7.4 变更统计（第三轮）

| 类别 | 数量 |
|---|---|
| 修改文件 | 7 (`scheduler.go`, `stream.go`, `audit.go`, `prompt.go`, `providers.go`, `history.go`, `client.go` 无修改) |
| 新增测试 | 12 |
| 修复 Bug | 7（数据竞争 ×2、panic 风险 ×2、资源泄漏、错误不一致、缓存污染） |
| 可用性增强 | 6（Validate、Content、Stream Usage、唯一 ID、防御拷贝、nil 过滤） |
| 编译状态 | ✅ 通过 |
| 测试状态 | ✅ 49/49 通过，`-race` 干净 |


---

## 八、第四轮审计（2026-05-31）— 网络增强与可观测性

> 审计视角：检查新增的网络层定制能力（Proxy、HTTPClient）、请求/响应拦截器、环境变量配置源的实现质量、线程安全和测试覆盖。

### 8.1 P0 级功能（已实施）

#### 8.1.1 定向 AI API 代理

**新增 `ProviderConfig.Proxy` + `buildHTTPClient()`**：
- 每个 Provider 独立配置代理，支持 `http://`、`https://`、`socks5://` 协议
- 空值时自动回退到系统环境变量 `HTTP_PROXY`
- 与 `SkipTLSVerify` 和自定义 `HTTPClient` 共存时不冲突

**设计决策**：
- 优先级：自定义 `HTTPClient` > `Proxy` 字段 > 环境变量
- 若调用方只提供 `Proxy`，复用 `http.DefaultTransport` 减少连接池浪费
- 若调用方提供自定义 `HTTPClient`，保留其 `CheckRedirect`/`Jar`，仅覆盖 Transport 层代理设置

#### 8.1.2 自定义 HTTPClient

**新增 `ProviderConfig.HTTPClient`**：
- 支持为单个 Provider 注入预配置的 `*http.Client`
- 典型用途：分布式链路追踪（OpenTelemetry Transport）、Mock 测试（httptest）、自定义 TLS/根证书

#### 8.1.3 拦截器机制

**新增 `core/interceptor.go` + `scheduler` 集成**：

```go
type Interceptor struct {
    BeforeRequest func(ctx context.Context, req *ChatCompletionRequest) error
    AfterResponse func(ctx context.Context, req ChatCompletionRequest, resp *ChatCompletionResponse, err error) (*ChatCompletionResponse, error)
}
```

**线程安全**：`Scheduler.interceptors` 使用 `atomic.Pointer[[]Interceptor]`，支持运行时 `SetInterceptors()`。

**调度顺序**（与 PromptInjector 的协作）：
```
User → Scheduler
  ├── Interceptor.ApplyBefore()   ← 拦截器先执行（可修改请求）
  ├── PromptInjector.Inject()     ← 再注入系统提示
  ├── Provider.ChatComplete()
  └── Interceptor.ApplyAfter()    ← 最后执行后置 Hook
```

#### 8.1.4 环境变量配置源

**新增 `core/env.go`**：
- `LoadConfigFromEnv()`：标准前缀 `AOEO_PROVIDER_N_*`
- `LoadConfigFromEnvWithPrefix()`：支持自定义前缀
- 扫描遇到空索引自动终止，避免无限循环
- 支持字段：NAME、API_KEY、ENDPOINT、MODEL、MAX_CONCURRENT、SKIP_TLS_VERIFY、PROXY
- `RetryConfigFromEnv()` 独立加载重试参数

### 8.2 P1 级质量加固

#### 8.2.1 测试覆盖扩张

| 指标 | 第三轮 | 第四轮 |
|---|---|---|
| 测试总数 | 49 | **201** |
| 新增测试 | 12 | **152** |
| Race Detector | ✅ 干净 | ✅ 干净 |

新增测试重点：
- `core/interceptor_test.go`：拦截器链式执行、Before 短路、After 转换、并发安全
- `core/env_test.go`：环境变量解析、前缀定制、空值边界、RetryConfig 加载
- `providers/providers_test.go`：Proxy 组装、自定义 HTTPClient 透传、TLS 跳过
- `internal/engine/scheduler_test.go`：Interceptor 与 Scheduler 集成、SetInterceptors 并发安全

#### 8.2.2 `ProviderConfig` 安全序列化

**新增 `MarshalJSON`**：序列化时自动将 `APIKey` 替换为 `***`，防止配置日志泄露凭证。

### 8.3 变更统计（第四轮）

| 类别 | 数量 |
|---|---|
| 新增文件 | 3 (`core/env.go`, `core/interceptor.go`, 各配套 `*_test.go`) |
| 修改文件 | 5+ (`providers.go`, `scheduler.go`, `client.go`, `config.go`, `options.go`) |
| 新增功能 | 4（定向代理、HTTPClient、拦截器、环境变量配置） |
| 新增测试 | 152 |
| 编译状态 | ✅ 通过 |
| 测试状态 | ✅ 201/201 通过，`-race` 干净 |

---

## 9. 第九轮审计（2026-05-31）— Phase 3.5 架构偿债

### 9.1 审计动机

第八轮审计报告（`AUDIT_REPORT_V2.md`）识别出三项 Critical 问题：
1. 流式架构的类型断言耦合（`stream.go:77` `p.(*providers.OpenAIProvider)`）
2. 流式零测试 + goroutine 泄漏风险
3. `buildRecord` 计费核心路径零覆盖

本轮目标：偿还这三项技术债务，为 Phase 4 功能扩展扫清障碍。

### 9.2 关键变更

#### 9.2.1 Provider 接口新增 `ChatCompleteStream`

```go
type Provider interface {
    // ... existing methods ...
    ChatCompleteStream(ctx context.Context, req core.ChatCompletionRequest) (<-chan core.StreamCompletionResponse, error)
}
```

- `BaseProvider` 提供默认实现（返回 `fmt.Errorf("does not support streaming")`）
- `OpenAIProvider` 实现完整的流式逻辑，保留 Proxy/TLS/HTTPClient 配置
- `Scheduler.ChatCompleteStream` 不再使用类型断言，直接调用 `p.ChatCompleteStream()`

**价值**：自定义 Provider（Azure、Vertex、vLLM 等）可独立实现流式，不再被 OpenAI 假设绑架。

#### 9.2.2 流式 interceptor 支持

`Scheduler.ChatCompleteStream` 现在应用 `BeforeRequest` interceptor：
```go
if err := chain.ApplyBefore(ctx, &reqCopy); err != nil {
    s.sem.Release()
    return nil, err
}
```

#### 9.2.3 流式 goroutine 泄漏修复

- `wrapped` channel 从 `cap(stream)`（可能为 0）改为固定 `16` 缓冲
- 生产者 goroutine 使用 `select { case msg, ok := <-stream: ... }` 模式，在 channel 关闭时安全退出
- 双重 `select` 确保 `streamCtx.Done()` 和 `wrapped <- msg` 都能及时响应

#### 9.2.4 测试补齐

| 新增测试文件 | 测试数 | 覆盖目标 |
|---|---|---|
| `internal/engine/stream_test.go` | 8 | 流式基本路径、context cancel、interceptor、慢消费者 |
| `internal/engine/scheduler_buildrecord_test.go` | 5 | buildRecord、Cost 计算、DefaultPricing 回退、ReqID 递增 |
| `client_test.go` | 18 | NewClient、Close、SetTimeout、Interceptors、History、Audit、Stream |
| `core/retry_test.go` | 1 | RetryConfig.Validate 边界值 |

### 9.3 质量指标对比

| 指标 | 第八轮 | 第九轮（本轮） |
|---|---|---|
| 测试总数 | 201 | **230** (+29) |
| 整体覆盖率 | 66.6% | **71.7%** |
| 根包覆盖率 | 52.4% | **84.5%** |
| `engine/` 覆盖率 | 70.9% | **80.0%** |
| Race Detector | ✅ | ✅ |
| go vet / staticcheck | ✅ | ✅ |
| examples 编译 | 未检查 | ✅ 全部通过 |

### 9.4 仍建议后续完善

| 问题 | 优先级 | 说明 |
|---|---|---|
| Semantic Consensus | 中 | 字符串级 Consensus 误报率高，建议 LLM-as-judge 可选 |
| Stream `AfterResponse` interceptor | 低 | 流式异步特性导致 AfterResponse 语义不清晰，文档化说明即可 |
| History 持久化接口 | 中 | 纯内存实现，进程重启丢失 |
| DefaultPricing 硬编码 | 低 | 价格写死源码，Provider 调价需发版 |
