## AoEo 服务化改造方案

### 一、现状总结

AoEo 当前是一个 **Go SDK / 嵌入式库**，使用方式是 `import aoeo "github.com/JishiTeam-J1wa/AoEo"`，由业务方在自己的 Go 进程中实例化 `Client`，通过代码或环境变量完成配置。

```
┌──────────────────────────────────────────────┐
│           业务进程 (Go Application)            │
│  ┌─────────────────────────────────────────┐ │
│  │            AoEo SDK (嵌入)                │ │
│  │  Client → Scheduler → Providers → AI    │ │
│  │  History(Ring) │ Interceptors │ Router   │ │
│  │  CircuitBreaker │ HealthCheck │ Prompt   │ │
│  └─────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
```

**核心能力已具备：**
- 多 Provider 调度（DeepSeek / Kimi / GLM / Qwen / 通用 OpenAI 兼容）
- 熔断器（3 次失败 → 60s 冷却）
- 5 种路由策略（轮询 / 随机 / 加权 / 主备 / 单 Provider）
- 自适应信号量（CAS 快速路径 + FIFO 慢速路径）
- 4 阶段拦截器链（Before → After → StreamChunk → StreamDone）
- Prompt 注入引擎（模板 + 通配符）
- 隐私网关（OPF Sidecar + 假名化 + Pebble KV）
- SQL 持久化（SQLite / MySQL / PostgreSQL）
- 滑动窗口健康指标

**缺失的服务化能力：**
| 能力 | Nacos 有 | AoEo 现状 |
|------|---------|----------|
| HTTP/gRPC API Server | ✅ | ❌ 仅 CLI 工具 |
| 配置热更新（REST API） | ✅ | ❌ 仅环境变量 + 代码 |
| 服务注册/发现 | ✅ | ❌ 无 |
| Web 管理控制台 | ✅ | ❌ 无 |
| Prometheus 指标导出 | ✅ | ❌ 仅进程内日志 |
| 多实例状态同步 | ✅ | ❌ 全部 in-process |
| 健康探针（readiness/liveness） | ✅ | ❌ 无 |
| 多租户隔离 | ✅ | ❌ 单客户端 |
| 配置持久化（文件/数据库） | ✅ | ❌ 无配置文件 |

---

### 二、目标架构

将 AoEo 改造为一个 **独立运行的 AI API 网关服务**，业务方只需通过 HTTP/gRPC 接入，无需嵌入 SDK：

```
                        ┌──────────────────────────────────────┐
                        │          AoEo Gateway Service         │
                        │                                       │
 业务方 A (Python) ───► │  ┌─── HTTP Server (OpenAI 兼容) ──┐  │
 业务方 B (Node.js) ──► │  │  POST /v1/chat/completions     │  │
 业务方 C (Go SDK)  ──► │  │  POST /v1/chat/completions/stream│ │
 业务方 D (curl)    ──► │  │  GET  /v1/models               │  │
                        │  │  GET  /healthz                  │  │
                        │  └─────────────────────────────────┘  │
                        │                                       │
                        │  ┌─── Admin API ──────────────────┐  │
                        │  │  CRUD /admin/providers          │  │
                        │  │  GET  /admin/stats              │  │
                        │  │  GET  /admin/health             │  │
                        │  │  PUT  /admin/config/reload      │  │
                        │  │  GET  /admin/metrics (Prom)     │  │
                        │  └─────────────────────────────────┘  │
                        │                                       │
                        │  ┌─── Core Engine (现有 SDK) ─────┐  │
                        │  │  Scheduler │ Router │ CB │ Histo │  │
                        │  │  Interceptor │ Prompt │ Privacy  │  │
                        │  └─────────────────────────────────┘  │
                        │                                       │
                        │  ┌─── Config Store ───────────────┐  │
                        │  │  YAML/DB │ Hot-Reload │ Watch   │  │
                        │  └─────────────────────────────────┘  │
                        └──────────────────────────────────────┘
                                │                    │
                    ┌───────────┤                    ├───────────┐
                    ▼           ▼                    ▼           ▼
              DeepSeek       Kimi                GLM         Qwen
```

---

### 三、改造步骤（6 个 Phase）

#### Phase 1: HTTP Server 层（核心，~3 天）

在 `cmd/server/` 下新建一个独立的服务入口，复用现有 SDK 核心：

```go
// cmd/server/main.go
func main() {
    cfg := config.Load()                          // Phase 2 的配置管理
    client, _ := aoeo.NewClient(cfg)

    mux := http.NewServeMux()

    // OpenAI 兼容端点 —— 业务方直接用 openai SDK 对接
    mux.HandleFunc("POST /v1/chat/completions", handleChat(client))
    mux.HandleFunc("POST /v1/chat/completions/stream", handleStream(client))
    mux.HandleFunc("GET /v1/models", handleModels(client))

    // Kubernetes 探针
    mux.HandleFunc("GET /healthz", handleHealth(client))
    mux.HandleFunc("GET /readyz", handleReady(client))

    // Prometheus
    mux.HandleFunc("GET /metrics", handleMetrics(client))

    // Admin API
    mux.HandleFunc("GET /admin/providers", handleProviderStatus(client))
    mux.HandleFunc("GET /admin/stats", handleStats(client))
    mux.HandleFunc("PUT /admin/providers", handleAddProvider(client))
    mux.HandleFunc("DELETE /admin/providers/{name}", handleRemoveProvider(client))
    mux.HandleFunc("PUT /admin/config/reload", handleReload(client))

    srv := &http.Server{Addr: ":8081", Handler: withMiddleware(mux)}
    srv.ListenAndServe()
}
```

**关键设计：**
- `/v1/chat/completions` 接收 OpenAI 标准格式 JSON，内部转换为 `core.ChatCompletionRequest`
- 请求头 `Authorization: Bearer <aoeo-api-key>` 做接入鉴权
- 流式响应使用标准 SSE 格式（`data: {...}\n\n`），与 OpenAI 完全兼容
- 业务方无需修改代码，只需改 `base_url` 指向 AoEo 网关

**新增文件：**
```
cmd/server/
├── main.go              # 服务入口
├── handler_chat.go      # /v1/chat/completions
├── handler_stream.go    # /v1/chat/completions/stream (SSE)
├── handler_models.go    # /v1/models
├── handler_admin.go     # /admin/* 管理端点
├── handler_health.go    # /healthz /readyz
├── handler_metrics.go   # /metrics (Prometheus)
├── middleware.go        # 鉴权、限流、日志、CORS
├── converter.go         # OpenAI JSON ↔ core 类型转换
└── server_test.go
```

#### Phase 2: 配置管理系统（~2 天）

替代当前纯环境变量方式，引入 YAML 配置文件 + 热更新 API：

```yaml
# aoeo.yaml
server:
  addr: ":8081"
  api_key: "sk-aoeo-xxx"        # 接入鉴权
  read_timeout: 120s
  write_timeout: 120s

providers:
  - name: deepseek
    api_key: "${DEEPSEEK_API_KEY}"   # 支持环境变量引用
    endpoint: https://api.deepseek.com
    model: deepseek-v4-pro
    max_concurrent: 10
    max_failures: 3
    cooldown: 60s
    pricing:
      prompt_per_1k: 2.0
      completion_per_1k: 8.0
      currency: CNY

  - name: kimi
    api_key: "${KIMI_API_KEY}"
    endpoint: https://api.moonshot.cn/v1
    model: kimi-k2.6

router:
  strategy: weighted              # round-robin | random | weighted | primary
  weight_strategy: combined       # latency | success_rate | combined

interceptors:
  privacy:
    enabled: true
    endpoint: "http://opf-1:8080,http://opf-2:8080"
    lb_strategy: least_latency
    fail_open: true
    policy: strict

retry:
  max_retries: 2
  base_delay: 500ms
  max_delay: 5s
  multiplier: 2.0

storage:
  driver: sqlite                  # sqlite | mysql | postgres
  dsn: "data/aoeo.db"

health_check:
  interval: 30s

history:
  ring_size: 1000
  persist: true
```

**新增文件：**
```
config/
├── config.go           # YAML 解析、环境变量替换
├── watcher.go          # fsnotify 文件变更监听
├── config_test.go
```

#### Phase 3: 可观测性（~2 天）

**Prometheus 指标导出：**

```go
// 暴露的核心指标
aoeo_requests_total{provider, model, status}           // 请求计数
aoeo_request_duration_seconds{provider, model}          // 延迟直方图
aoeo_tokens_total{provider, model, type}                // Token 用量 (prompt/completion)
aoeo_cost_total{provider, model, currency}              // 费用累计
aoeo_provider_available{provider}                       // Provider 可用性 (0/1)
aoeo_provider_health_success_rate{provider}             // 成功率
aoeo_provider_health_avg_latency_ms{provider}           // 平均延迟
aoeo_provider_circuit_breaker_state{provider}           // 熔断器状态
aoeo_semaphore_active{provider}                         // 当前并发数
aoeo_semaphore_capacity{provider}                       // 最大并发数
```

**OpenTelemetry 追踪集成：**
- 每次请求生成 Span，附带 provider、model、latency、token 等属性
- 支持导出到 Jaeger / Zipkin / OTLP Collector

**新增文件：**
```
observability/
├── metrics.go          # Prometheus collector
├── tracing.go          # OpenTelemetry setup
├── middleware.go       # 自动注入 metrics/tracing 到请求链路
```

#### Phase 4: 管理控制台（~3 天）

提供一个轻量的 Web Dashboard（可选，内嵌到 Admin API）：

```
GET /dashboard → 嵌入式 SPA (React/Vue)

功能页面：
├── 概览         # 请求量、成功率、费用趋势图
├── Provider 管理 # 增删改查、健康状态、熔断器手动重置
├── 调用记录      # 分页查询、按标签/Provider 过滤
├── 审计日志      # 内容审核记录
├── 配置管理      # 在线编辑 YAML、热更新
├── 隐私映射      # 查看/清理 Session 映射
└── 系统设置      # 路由策略、超时、重试配置
```

可以用 Go `embed.FS` 将前端打包嵌入二进制，零额外依赖。

#### Phase 5: 高可用与集群（~3 天）

```
┌─── AoEo Instance 1 ──┐    ┌─── AoEo Instance 2 ──┐
│  Core Engine          │    │  Core Engine          │
│  Local CircuitBreaker │    │  Local CircuitBreaker │
└────────┬──────────────┘    └────────┬──────────────┘
         │                           │
         └───────────┬───────────────┘
                     ▼
           ┌──── Shared Storage ────┐
           │  MySQL/PostgreSQL      │
           │  - 调用记录             │
           │  - 审计日志             │
           │  - 配置 (或 etcd/Redis) │
           └────────────────────────┘
```

**关键改造：**
- 调用记录和审计 → 共享 MySQL/PostgreSQL（已支持）
- 熔断器状态 → 可选 Redis 共享（跨实例一致）或保持本地（各自独立熔断）
- 配置同步 → 文件变更通知 + 数据库版本号，或接入 etcd/Consul
- 负载均衡 → 前置 Nginx/Envoy 做 L7 分流

#### Phase 6: 多租户与插件化（远期）

- **多租户**：按 API Key 隔离 Provider 池、配额、历史记录
- **Provider 插件**：允许用户通过 WASM 或 Go plugin 注册自定义 Provider
- **灰度发布**：按比例将流量路由到不同 Provider/模型

---

### 四、改造优先级建议

| 优先级 | Phase | 工作量 | 价值 |
|--------|-------|--------|------|
| **P0** | Phase 1 HTTP Server | 3 天 | 从 SDK 变为服务，解锁所有语言接入 |
| **P0** | Phase 2 配置管理 | 2 天 | YAML 配置 + 热更新，降低使用门槛 |
| **P1** | Phase 3 可观测性 | 2 天 | 生产必备，Prometheus + 健康探针 |
| **P2** | Phase 4 管理控制台 | 3 天 | 可视化运维，降低操作复杂度 |
| **P2** | Phase 5 高可用 | 3 天 | 多实例部署，支撑大规模流量 |
| **P3** | Phase 6 多租户 | 远期 | SaaS 化运营 |

---

### 五、核心改造量估算

现有代码 **~7,700 行**，SDK 核心全部保留不动。服务化改造主要是 **新增外壳层**：

| 新增模块 | 预估代码量 | 说明 |
|----------|-----------|------|
| HTTP Server + Handlers | ~1,500 行 | OpenAI 兼容 API + Admin API |
| Middleware (Auth/Log/CORS) | ~300 行 | 请求预处理 |
| OpenAI JSON Converter | ~200 行 | 请求/响应格式转换 |
| Config (YAML + Watcher) | ~500 行 | 配置加载 + 热更新 |
| Metrics (Prometheus) | ~400 行 | 指标采集 + 导出 |
| Health Probes | ~100 行 | K8s 探针 |
| 测试代码 | ~1,000 行 | Handler 集成测试 |
| **合计** | **~4,000 行** | |

SDK 核心（Scheduler / Provider / Router / Interceptor / Privacy / Storage）几乎 **零改动**。这是 AoEo 架构的优势——6 层分层设计天然支持在上面套一个 Server 壳。

---

### 六、与 Nacos 的对标映射

| Nacos 能力 | AoEo 对应实现 |
|-----------|--------------|
| 服务注册 | Provider 配置（YAML/API 动态注册） |
| 服务发现 | Router + 健康检查自动剔除不可用 Provider |
| 配置管理 | `aoeo.yaml` + Admin API + fsnotify 热更新 |
| 健康检查 | 后台 HealthCheck goroutine + 熔断器 |
| 控制台 | Web Dashboard（Phase 4） |
| 集群模式 | 共享 DB + 前置 LB（Phase 5） |
| 命名空间 | 多租户 Provider 池隔离（Phase 6） |

---

### 七、快速启动（Phase 1 完成后）

```bash
# 启动 AoEo Gateway
aoeo-server --config aoeo.yaml

# 业务方只需修改 base_url，其他代码不变
# Python:
import openai
client = openai.OpenAI(base_url="http://aoeo-gateway:8081/v1", api_key="sk-aoeo-xxx")
response = client.chat.completions.create(model="deepseek-v4-pro", messages=[...])

# curl:
curl http://aoeo-gateway:8081/v1/chat/completions \
  -H "Authorization: Bearer sk-aoeo-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"Hello"}]}'
```
