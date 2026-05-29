# AoEo 集成指南 — 如何将 SDK 嵌入现有系统

本文档面向已有 Go 项目的开发者，说明如何将 AoEo SDK 作为基础设施层集成到现有系统中，替换或增强现有的单 Provider LLM 调用逻辑。

---

## 目录

1. [最小改动集成](#1-最小改动集成)
2. [在 Web 服务中集成](#2-在-web-服务中集成)
3. [在微服务架构中集成](#3-在微服务架构中集成)
4. [在 Worker / 队列消费中集成](#4-在-worker--队列消费中集成)
5. [替换现有单 Provider 调用](#5-替换现有单-provider-调用)
6. [接入监控与告警](#6-接入监控与告警)
7. [Prompt 注入最佳实践](#7-prompt-注入最佳实践)
8. [成本管控与预算告警](#8-成本管控与预算告警)

---

## 1. 最小改动集成

如果你的代码目前是直接调用 OpenAI SDK：

```go
// 改造前
client := openai.NewClient(apiKey)
resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{...})
```

改造为 AoEo 只需 3 步：

```go
// 改造后
import "github.com/JishiTeam-J1wa/AoEo"

// Step 1: 初始化（放在 main 或依赖注入容器中）
aoeoClient, err := aoeo.NewClient(aoeo.Config{
    Providers: []aoeo.ProviderConfig{
        {
            Name:     "openai",
            APIKey:   os.Getenv("OPENAI_API_KEY"),
            Endpoint: "https://api.openai.com/v1",
            Model:    "gpt-4o",
        },
    },
})

// Step 2: 构造请求（字段名几乎相同）
req := aoeo.ChatCompletionRequest{
    Model:    "gpt-4o",
    Messages: []aoeo.Message{{Role: "user", Content: prompt}},
}

// Step 3: 调用（签名一致，返回值多了 Usage/Cost）
resp, err := aoeoClient.ChatComplete(ctx, req)
if err != nil {
    return err
}
content := resp.Content()
```

**升级 Fallback**：只需把 `ChatComplete` 改成 `ChatCompleteWithFallback`，并添加第二个 Provider：

```go
cfg := aoeo.Config{
    Providers: []aoeo.ProviderConfig{
        {Name: "openai", APIKey: os.Getenv("OPENAI_API_KEY"), ...},
        {Name: "deepseek", APIKey: os.Getenv("DEEPSEEK_API_KEY"), ...},
    },
}
resp, err := aoeoClient.ChatCompleteWithFallback(ctx, req)
```

---

## 2. 在 Web 服务中集成

### 2.1 Gin / Echo / HTTP 服务中的生命周期管理

```go
package main

import (
    "context"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/JishiTeam-J1wa/AoEo"
    "github.com/gin-gonic/gin"
)

func main() {
    // 初始化 AoEo 客户端
    client, err := aoeo.NewClient(aoeo.Config{
        Providers: []aoeo.ProviderConfig{
            {
                Name:     "deepseek",
                APIKey:   os.Getenv("DEEPSEEK_API_KEY"),
                Endpoint: "https://api.deepseek.com",
                Model:    "deepseek-v4-pro",
                MaxConcurrent: 10,
            },
        },
    })
    if err != nil {
        panic(err)
    }
    defer client.Close()

    // 注入到 Gin handler
    r := gin.Default()
    r.POST("/api/chat", func(c *gin.Context) {
        var body struct {
            Messages []aoeo.Message `json:"messages"`
        }
        if err := c.BindJSON(&body); err != nil {
            c.JSON(400, gin.H{"error": err.Error()})
            return
        }

        ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
        defer cancel()

        resp, err := client.ChatComplete(ctx, aoeo.ChatCompletionRequest{
            Messages: body.Messages,
        })
        if err != nil {
            c.JSON(502, gin.H{"error": err.Error()})
            return
        }

        c.JSON(200, gin.H{
            "content": resp.Content(),
            "usage": gin.H{
                "prompt":     resp.Usage.PromptTokens,
                "completion": resp.Usage.CompletionTokens,
                "total":      resp.Usage.TotalTokens,
            },
        })
    })

    // 优雅关闭
    srv := &http.Server{Addr: ":8080", Handler: r}
    go srv.ListenAndServe()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    client.Close()
    srv.Shutdown(context.Background())
}
```

### 2.2 请求上下文传递

```go
// 将用户信息注入 context，在 PromptInjector 中使用
func middleware(c *gin.Context) {
    ctx := context.WithValue(c.Request.Context(), "user_id", c.GetHeader("X-User-ID"))
    c.Request = c.Request.WithContext(ctx)
    c.Next()
}
```

---

## 3. 在微服务架构中集成

### 3.1 作为共享库 / internal package

```
my-service/
├── internal/
│   └── llm/              # 封装 AoEo
│       ├── client.go     # 初始化和配置
│       ├── prompt.go     # 业务 Prompt 模板
│       └── cost.go       # 成本上报
├── cmd/
│   └── server/
│       └── main.go
```

`internal/llm/client.go`：

```go
package llm

import (
    "sync"

    "github.com/JishiTeam-J1wa/AoEo"
)

var (
    once   sync.Once
    client *aoeo.Client
    initErr error
)

func Init(cfg aoeo.Config) error {
    once.Do(func() {
        client, initErr = aoeo.NewClient(cfg)
    })
    return initErr
}

func C() *aoeo.Client {
    if client == nil {
        panic("llm client not initialized")
    }
    return client
}

func Close() error {
    if client != nil {
        return client.Close()
    }
    return nil
}
```

`internal/llm/prompt.go`：

```go
package llm

import "github.com/JishiTeam-J1wa/AoEo"

func SetupPrompts() {
    pi := aoeo.NewPromptInjector()

    // 所有 Provider 统一系统提示
    pi.AddTemplate(aoeo.PromptTemplate{
        Provider: "*",
        Model:    "*",
        Position: "system",
        Content:  "You are a helpful assistant for service {{service}}.",
        Vars:     map[string]string{"service": "my-app"},
    })

    C().SetPromptInjector(pi)
}
```

业务代码使用：

```go
package main

import "my-service/internal/llm"

func main() {
    llm.Init(aoeo.Config{...})
    llm.SetupPrompts()
    defer llm.Close()

    resp, err := llm.C().ChatComplete(ctx, req)
}
```

### 3.2 在 Kubernetes 中部署

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-service
spec:
  template:
    spec:
      containers:
        - name: app
          image: my-service:latest
          env:
            - name: DEEPSEEK_API_KEY
              valueFrom:
                secretKeyRef:
                  name: llm-keys
                  key: deepseek
            - name: KIMI_API_KEY
              valueFrom:
                secretKeyRef:
                  name: llm-keys
                  key: kimi
          resources:
            requests:
              memory: "128Mi"
              cpu: "100m"
---
apiVersion: v1
kind: Secret
metadata:
  name: llm-keys
type: Opaque
stringData:
  deepseek: sk-...
  kimi: sk-...
```

---

## 4. 在 Worker / 队列消费中集成

### 4.1 带重试和 Fallback 的异步任务

```go
func processTask(ctx context.Context, task Task) error {
    req := aoeo.ChatCompletionRequest{
        Messages: []aoeo.Message{
            {Role: "system", Content: task.SystemPrompt},
            {Role: "user", Content: task.UserPrompt},
        },
        Tags: []string{"worker", task.Queue},
    }

    // 优先使用 Fallback 模式确保高可用
    resp, err := llm.C().ChatCompleteWithFallback(ctx, req)
    if err != nil {
        // 所有 Provider 都失败，进入死信队列
        return fmt.Errorf("all providers failed: %w", err)
    }

    // 处理结果...
    return nil
}
```

### 4.2 批量任务的成本控制

```go
func batchProcess(ctx context.Context, tasks []Task) {
    for _, t := range tasks {
        resp, err := llm.C().ChatComplete(ctx, buildReq(t))
        if err != nil {
            log.Printf("task %s failed: %v", t.ID, err)
            continue
        }

        cost := resp.Usage.Cost(aoeo.DefaultPricing("deepseek", "deepseek-v4-pro"))
        metrics.Record("llm.cost", cost, "provider", resp.Model)
    }

    // 批量完成后上报总成本
    for name, stats := range llm.C().Stats() {
        log.Printf("[%s] total cost: %.4f %s", name, stats.TotalCost, stats.Currency)
    }
}
```

---

## 5. 替换现有单 Provider 调用

### 5.1 渐进式迁移策略

不要一次性全量替换。推荐按以下步骤：

**Phase 1：Shadow 模式**
```go
// 保持原有调用不变，同时用 AoEo 做对比
oldResp, oldErr := oldClient.ChatComplete(ctx, req)
aoeoResp, aoeoErr := aoeoClient.ChatComplete(ctx, req)

// 记录差异用于评估
if (oldErr == nil) != (aoeoErr == nil) {
    log.Printf("mismatch: old_err=%v aoeo_err=%v", oldErr, aoeoErr)
}
```

**Phase 2：Fallback 模式**
```go
// 主链路仍是旧客户端，AoEo 作为备用
resp, err := oldClient.ChatComplete(ctx, req)
if err != nil {
    resp, err = aoeoClient.ChatCompleteWithFallback(ctx, req)
}
```

**Phase 3：完全替换**
```go
// 完全切换到 AoEo
resp, err := aoeoClient.ChatCompleteWithFallback(ctx, req)
```

### 5.2 封装适配器减少侵入

```go
type LLMClient interface {
    Complete(ctx context.Context, prompt string) (string, error)
    CompleteStream(ctx context.Context, prompt string) (<-chan string, error)
}

type aoEoAdapter struct {
    client *aoeo.Client
}

func (a *aoEoAdapter) Complete(ctx context.Context, prompt string) (string, error) {
    resp, err := a.client.ChatComplete(ctx, aoeo.ChatCompletionRequest{
        Messages: []aoeo.Message{{Role: "user", Content: prompt}},
    })
    if err != nil {
        return "", err
    }
    return resp.Content(), nil
}

func (a *aoEoAdapter) CompleteStream(ctx context.Context, prompt string) (<-chan string, error) {
    stream, err := a.client.ChatCompleteStream(ctx, aoeo.ChatCompletionRequest{
        Messages: []aoeo.Message{{Role: "user", Content: prompt}},
    })
    if err != nil {
        return nil, err
    }

    out := make(chan string)
    go func() {
        defer close(out)
        for chunk := range stream {
            if chunk.Err != nil {
                return
            }
            out <- chunk.Chunk.Delta.Content
        }
    }()
    return out, nil
}
```

---

## 6. 接入监控与告警

### 6.1 基于事件系统的 Prometheus 指标

```go
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
    providerCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "aoeo_provider_calls_total",
        Help: "Total calls per provider",
    }, []string{"provider", "status"})

    providerCost = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "aoeo_provider_cost_total",
        Help: "Total cost per provider",
    }, []string{"provider", "currency"})
)

type PrometheusEmitter struct{}

func (e *PrometheusEmitter) Emit(topic string, data ...any) {
    switch topic {
    case aoeo.EventProviderFail:
        providerCalls.WithLabelValues(data[0].(string), "fail").Inc()
    case aoeo.EventProviderRecover:
        providerCalls.WithLabelValues(data[0].(string), "recover").Inc()
    case aoeo.EventFallbackTrigger:
        providerCalls.WithLabelValues("scheduler", "fallback").Inc()
    }
}
```

### 6.2 定期上报 Stats

```go
ticker := time.NewTicker(30 * time.Second)
for range ticker.C {
    for name, s := range client.Stats() {
        providerCalls.WithLabelValues(name, "success").Add(float64(s.TotalCalls - s.FailedCalls))
        providerCalls.WithLabelValues(name, "fail").Add(float64(s.FailedCalls))
        providerCost.WithLabelValues(name, s.Currency).Add(s.TotalCost)
    }
}
```

### 6.3 熔断告警

```go
type AlertEmitter struct {
    webhook string
}

func (e *AlertEmitter) Emit(topic string, data ...any) {
    if topic == aoeo.EventProviderOpen {
        provider := data[0].(string)
        sendAlert(e.webhook, fmt.Sprintf("Provider %s circuit breaker OPEN", provider))
    }
}
```

---

## 7. Prompt 注入最佳实践

### 7.1 按环境区分系统提示

```go
func NewPromptInjector(env string) *aoeo.PromptInjector {
    pi := aoeo.NewPromptInjector()

    pi.AddTemplate(aoeo.PromptTemplate{
        Provider: "*",
        Model:    "*",
        Position: "system",
        Content:  "Environment: {{env}}. Respond concisely.",
        Vars:     map[string]string{"env": env},
    })

    return pi
}
```

### 7.2 按业务模块注入任务指令

```go
// 客服模块
pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "*",
    Model:    "*",
    Position: "prepend_user",
    Content:  "[Support Ticket] Priority: {{priority}} | Category: {{category}}",
    Vars:     map[string]string{"priority": "normal", "category": "general"},
})

// 代码生成模块
pi.AddTemplate(aoeo.PromptTemplate{
    Provider: "deepseek",
    Model:    "*",
    Position: "system",
    Content:  "You are an expert Go programmer. Output only code, no explanation.",
})
```

### 7.3 运行时动态变量

```go
func (s *Service) HandleRequest(ctx context.Context, userID string, msg string) {
    req := aoeo.ChatCompletionRequest{
        Messages: []aoeo.Message{{Role: "user", Content: msg}},
    }

    // 通过 Tags 传递动态变量到 PromptInjector
    // （PromptInjector 在 Scheduler 层统一处理，业务代码不需要关心）
    resp, err := s.client.ChatComplete(ctx, req)
    ...
}
```

---

## 8. 成本管控与预算告警

### 8.1 单请求成本上限

```go
const maxCostPerRequest = 1.0 // 1 CNY

resp, err := client.ChatComplete(ctx, req)
if err != nil {
    return err
}

cost := resp.Usage.Cost(pricing)
if cost > maxCostPerRequest {
    log.Printf("WARNING: high cost request: %.4f %s", cost, pricing.Currency)
}
```

### 8.2 按 Provider 预算熔断

```go
type BudgetLimiter struct {
    mu      sync.Mutex
    budgets map[string]float64 // provider -> max cost
    spent   map[string]float64
}

func (b *BudgetLimiter) Check(provider string, cost float64) bool {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.spent[provider] += cost
    return b.spent[provider] <= b.budgets[provider]
}
```

### 8.3 成本归因

```go
// 每个请求打标签，后续按标签统计
req.Tags = []string{"feature_x", "user_tier_premium"}
resp, _ := client.ChatComplete(ctx, req)

// 查询时按标签过滤
records := hist.RecordsByTag("feature_x")
totalCost := 0.0
for _, r := range records {
    totalCost += r.Cost
}
log.Printf("Feature X total cost: %.4f", totalCost)
```

---

## 总结

AoEo 的集成哲学是**渐进式、非侵入式**：

- 可以从单 Provider 开始，逐步增加 Fallback
- 可以从 `ChatComplete` 开始，按需升级到 `ChatCompleteWithFallback`、`Dual` 或 `Stream`
- Prompt 注入可以在不修改业务代码的情况下，统一管理所有系统提示词
- 成本统计和历史记录零配置自动生效

无论你的系统是单体应用、微服务、还是 Serverless 函数，只需初始化一个 `*aoeo.Client` 实例，即可获得生产级的多模型调度能力。
