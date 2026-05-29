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
| P2 | 请求/响应拦截器 | `BeforeRequest` / `AfterResponse` Hook |
| P2 | Trace ID | 为每次调用生成唯一 ID，便于链路追踪 |
| P2 | 成本估算 | 根据各平台定价计算调用成本 |
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
