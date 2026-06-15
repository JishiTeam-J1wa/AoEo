## AoEo 功能实现深度审计报告

**审计时间**: 2026-06-15
**审计范围**: 39 个源文件，约 7,700 行代码

---

### 总体评价

AoEo 整体架构分层清晰（Client Facade → Scheduler Engine → Core Interfaces → Providers），依赖方向正确，核心接口层只定义类型不包含实现，符合依赖倒置原则。OPF 隐私引擎集成完整，负载均衡客户端具备 EWMA 延迟追踪和 CAS 无锁更新。代码在路由器的无锁原子设计、环形缓冲区的 GC 感知清理、SSE 流式生命周期管理等方面展示了较高的工程水准。

但在并发安全、资源管理和边界处理方面存在若干需要修复的问题。以下按模块和严重程度排列。

---

### 全模块评分总览

| 模块 | 文件 | 评分 | HIGH | MEDIUM | LOW | 核心亮点 |
|------|------|------|------|--------|-----|---------|
| **core/** | 12 files | **B+** | 2 | 5 | 10 | 路由器原子无锁设计、APIKey 脱敏序列化 |
| **engine/** | 8 files | **B** | 5 | 10 | 8 | CAS+FIFO 信号量、调度器 panic recovery |
| **privacy/** | 13 files | **B+** | 3 | 6 | 5 | EWMA CAS 负载均衡、Pebble 原子批量写入 |
| **storage/** | 4 files | **A-** | 0 | 3 | 5 | 模板方法复用 95% SQL、完整错误路径清理 |
| **providers/** | 1 file | **B+** | 3 | 4 | 3 | 原子熔断器、固定滑动窗口健康指标 |
| **cmd/** | 1 file | **B** | 1 | 2 | 3 | Context 超时控制、tabwriter 格式化 |

---

### HIGH 级问题清单（建议优先修复）

#### 1. client.go — ChatCompleteWithProvider 并发数据竞争

**位置**: client.go 第 282-287 行

`SetRouter` 通过 `atomic.Pointer.Store` 实现单次读写原子性，但"读旧值 → 设新值 → 执行请求 → defer 恢复旧值"四步操作作为整体不是原子的。两个 goroutine 并发调用时会互相覆盖路由器，请求被发送到错误的 Provider。

**建议**: 改为请求级别路由注入（context 传递或独立参数），而非修改共享状态。

#### 2. stream.go — 缺少 req.Clone() 导致原始请求被修改

**位置**: stream.go 第 52 行

当未配置 PromptInjector 时，`reqCopy := req` 是浅拷贝，与原始请求共享 Messages 切片。`ApplyBefore` 修改 `reqCopy.Messages` 会污染调用方的原始请求。这与 SCHED-01/02 修复的是同类问题。

**建议**: 始终调用 `req.Clone()` 而非条件性克隆。

#### 3. history.go — Record() 无界 goroutine 创建

**位置**: history.go 第 92 行

每次 `Record()` 调用都 `go h.persistRecord(r)` 创建新 goroutine。高吞吐场景下（如 1000 req/s），goroutine 积累速度超过完成速度，可能导致内存耗尽。

**建议**: 使用 worker pool 或有界 channel 限制并发持久化数量。

#### 4. prompt.go — Inject/Clear 数据竞争

**位置**: prompt.go 第 101-103 行

`Inject()` 在 RLock 下获取切片头后释放锁，然后在无锁状态下遍历底层数组。如果 `Clear()` 并发执行（持有 Lock 截断切片），`Inject` 读取的底层数组可能已被修改。

**建议**: 在 RLock 持有期间完成全部遍历，或使用 `Templates()` 返回的深拷贝副本。

#### 5. scheduler.go — ChatCompleteWithFallback 未克隆请求

**位置**: scheduler.go 第 533 行

`ApplyBefore` 接收原始 `req` 的指针，拦截器可以修改调用方的请求对象。与 `ChatComplete`（第 451 行正确调用了 `req.Clone()`）行为不一致。

**建议**: 在 `ApplyBefore` 之前克隆请求。

#### 6. scheduler.go — ChatCompleteDual 历史记录的 Tags 切片竞争

**位置**: scheduler.go 第 751-767 行

两处 `append(req.Tags, "dual")` 如果 `cap(req.Tags) > len(req.Tags)`，两个 append 会写入同一底层数组的同一位置，造成数据竞争。

**建议**: 显式克隆 Tags 切片后再 append。

#### 7. providers.go — ChatCompleteStream 流式循环 busy-wait

**位置**: providers.go 第 672-681 行

外层 select 使用 `default` 分支检查 ctx.Done()，当 `stream.Recv()` 快速返回时会形成紧密循环消耗 CPU。

**建议**: 去掉 default 分支，使用独立 goroutine + channel 处理 ctx 取消。

#### 8. providers.go — WaitGroup 创建但从未 Wait

**位置**: providers.go 第 664-665 行

`sync.WaitGroup` 被创建并在 goroutine 中 Done()，但函数返回后没有 Wait()。Provider 关闭时流式 goroutine 会继续运行，造成泄漏。

**建议**: 将 WaitGroup 存储为 OpenAIProvider 字段，在 Close() 中 Wait()。

#### 9. privacy/pseudonymizer.go — 替换顺序不确定性

检测到的 span 按长度降序排列后逐一替换，但当多个 span 存在重叠时，替换顺序影响最终结果。如果短 span 先被替换，长 span 的匹配可能失败。

**建议**: 从后向前替换（按 start 位置降序），确保前面的替换不影响后面的偏移量。

#### 10. privacy/generator.go — genericMask 按字节而非 rune 计数

**位置**: generator.go

`genericMask` 使用 `len(original)` 计算长度，对多字节 UTF-8 字符（如中文）会返回字节数而非字符数，导致伪造值长度与原始值不匹配。

**建议**: 使用 `utf8.RuneCountInString(original)` 计算字符数。

#### 11. cmd/aoeo/main.go — 指定 -model 时丢失 -temperature

**位置**: main.go 第 223-225 行、第 269-271 行

先构建带 temperature 的请求，然后当指定 model 时重新构建请求但没有带上 WithTemperature，导致 temperature 参数被静默丢弃。

**建议**: 重建请求时同时传入所有已有选项。

---

### MEDIUM 级问题清单（建议在下一迭代处理）

| 编号 | 文件 | 问题 |
|------|------|------|
| M-01 | client.go | `SetEmitter` 持锁期间遍历 Provider 调用 SetEmitter，可能锁持有时间过长 |
| M-02 | core/env.go | `SetEnvConfig` 硬编码 "AOEO" 前缀，与 LoadConfigFromEnvWithPrefix 不对称 |
| M-03 | core/env.go | `SetEnvConfig` 不清理先前配置遗留的环境变量 |
| M-04 | core/retry.go | `IsRetryableError` 基于字符串子串匹配，可能误判 |
| M-05 | core/router.go | WeightedRouter Select 过滤零分 Provider 而 SelectSequence 不过滤，行为不一致 |
| M-06 | core/interceptor.go | 拦截器链无 panic 恢复机制 |
| M-07 | core/pricing.go | 使用 float64 进行货币计算存在精度问题 |
| M-08 | engine/scheduler.go | ChatCompleteDual 审计超时共享，主 Provider 耗时过多会导致审计被取消 |
| M-09 | engine/semaphore.go | `setMaxConc` 无下限校验，n<=0 会导致所有 Acquire 永久阻塞 |
| M-10 | engine/history.go | `persistRecord` 在无锁状态下访问 `h.storage`，与 `SetStorage` 存在竞争 |
| M-11 | engine/prompt.go | `injectSystem` 替换而非追加 system 消息，静默丢弃原始 system prompt |
| M-12 | engine/result.go | 全局正则缓存满时全量清空，造成延迟尖峰 |
| M-13 | providers.go | HealthCheck 每次创建新 http.Client，不复用连接 |
| M-14 | storage/base.go | JSON Marshal/Unmarshal 错误被静默丢弃 |
| M-15 | storage/base.go | GetCallsByTag 使用 LIKE 对 JSON 数组做子串匹配，精确度有限 |
| M-16 | storage/mysql.go | 未执行 SET NAMES utf8mb4 或验证字符集 |
| M-17 | cmd/main.go | 死代码 cmdPrivacy 未在 main() switch 中注册 |
| M-18 | cmd/main.go | 缺少 SIGINT/SIGTERM 信号处理 |

---

### 架构亮点

**无锁原子路由器**: RoundRobinRouter 使用 `atomic.Uint64` 实现零分配轮询，WeightedRouter 使用黄金比例常数（0x9E3779B97F4A7C15）改善确定性分布。

**自适应信号量**: CAS 快速路径零锁争用，FIFO 慢速路径保证公平性，SEM-01 修复是教科书级的 lock-free-to-locked 回退设计。

**OPF 负载均衡客户端**: EWMA 延迟追踪（alpha=0.3）使用 CAS 循环无锁更新，LeastLatency 策略配合 Fisher-Yates 随机打散和选择排序确保最优分配。

**Pebble KV 存储**: Batch 原子写入双向映射（fake→original + original→fake），8 字节 BigEndian 时间戳前缀编码支持高效过期清理。

**Provider 熔断器**: `failCount atomic.Int32` + `failUntil atomic.Int64` 实现完全无锁的熔断/冷却机制，固定大小 `[20]healthEntry` 滑动窗口无 GC 压力。

**存储层模板方法**: sqlStorage + 占位符回调将 95% 的 SQL 逻辑复用，三个后端文件各仅 70-80 行。

---

### 可扩展性评估

| 扩展场景 | 难度 | 当前支持情况 |
|---------|------|------------|
| 新增 OpenAI 兼容 Provider | 极低 | ~15 行工厂函数即可 |
| 新增非 OpenAI 协议 Provider | 中 | 实现 Provider 接口 + 嵌入 BaseProvider |
| 新增路由策略 | 低 | 实现 Router 接口 |
| 新增拦截器 | 低 | 构造 Interceptor 结构体即可 |
| 新增 SQL 存储后端 | 低 | ~70 行文件 + 方言参数 |
| 新增非 SQL 存储后端 | 中 | 实现 core.Storage 全部 13 个方法 |
| 新增 PII 检测引擎 | 低 | 实现 Detector 接口 |
| 新增伪造值生成策略 | 低 | 扩展 generator.go 的 switch 分支 |
