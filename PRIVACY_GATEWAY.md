# AoEo Privacy Gateway

> AI 网关的内置隐私过滤层。一行代码启用，零配置部署，全程透明。
>
> 支持 **批量检测**、**智能路由（LeastLatency）**、**HTTP/2 连接复用**、**连接预热**，
> 多台隐私检测 Sidecar 可横向扩容。

---

## 快速开始（3 种方式）

### 方式 1：环境变量自动配置（推荐）

```bash
export AOEO_PRIVACY_ENABLED=true
export AOEO_PRIVACY_ENDPOINT=http://localhost:8080
export AOEO_PRIVACY_POLICY=pseudonymize
export AOEO_PRIVACY_FAILOPEN=false
```

```go
import (
    aoeo "github.com/JishiTeam-J1wa/AoEo"
    "github.com/JishiTeam-J1wa/AoEo/privacy"
)

func main() {
    client, err := aoeo.NewClient(cfg, privacy.WithPrivacyFilter())
}
```

### 方式 2：显式指定端点（单节点）

```go
client, err := aoeo.NewClient(cfg, privacy.WithPrivacyModel("http://localhost:8080"))
```

### 方式 3：手动配置（高级：多实例 + 智能路由）

```go
import (
    "time"

    aoeo "github.com/JishiTeam-J1wa/AoEo"
    "github.com/JishiTeam-J1wa/AoEo/privacy"
    "github.com/JishiTeam-J1wa/AoEo/privacy/model"
)

// 部署了 3 台隐私检测 Sidecar，使用 LeastLatency 智能路由
// 自动把请求发给延迟最低的节点，EWMA 实时更新
func main() {
    gw, err := privacy.NewGateway(privacy.GatewayConfig{
        ModelEndpoint: "http://sidecar-1:8080,http://sidecar-2:8080,http://sidecar-3:8080",
        LBStrategy:    model.LeastLatency, // 可选: RoundRobin / Random / LeastLatency
        Policy:        privacy.ActionPseudonymize,
        SessionTTL:    7 * 24 * time.Hour,
        FailOpen:      true, // sidecar 全部故障时透传请求，不阻断业务
    })
    if err != nil {
        log.Fatal(err)
    }
    defer gw.Close()

    client, err := aoeo.NewClient(cfg, aoeo.WithInterceptors(gw.ToInterceptor()))
}
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `AOEO_PRIVACY_ENABLED` | `false` | 是否启用隐私过滤 |
| `AOEO_PRIVACY_ENDPOINT` | `http://localhost:8080` | AI 隐私检测 sidecar 地址。支持逗号分隔多地址，如 `http://s1:8080,http://s2:8080` |
| `AOEO_PRIVACY_POLICY` | `pseudonymize` | `block` / `mask` / `pseudonymize` / `audit` |
| `AOEO_PRIVACY_FAILOPEN` | `false` | sidecar 不可用时是否透传请求（不阻断） |

> 注：`AOEO_PRIVACY_ENDPOINT` 填多地址时，默认使用 `RoundRobin` 策略。
> 如需 `LeastLatency`，请用「方式 3」手动配置。

---

## 部署

### 多实例部署（客户端负载均衡）

Go 端内置 `LoadBalancedClient`，支持多台 Sidecar 的智能路由，无需 Nginx：

```yaml
# docker-compose.yml
services:
  privacy-sidecar-1:
    build: ./cmd/privacy-sidecar
    environment: [MODEL_PATH=/app/models, DEVICE=cpu, PORT=8080]
    networks: [aoeo]

  privacy-sidecar-2:
    build: ./cmd/privacy-sidecar
    environment: [MODEL_PATH=/app/models, DEVICE=cpu, PORT=8080]
    networks: [aoeo]

  privacy-sidecar-3:
    build: ./cmd/privacy-sidecar
    environment: [MODEL_PATH=/app/models, DEVICE=cpu, PORT=8080]
    networks: [aoeo]

  aoeo-gateway:
    build: .
    environment:
      - AOEO_PRIVACY_ENABLED=true
      # 直接写多地址，Go 端自动做负载均衡 + 健康检查 + 故障转移
      - AOEO_PRIVACY_ENDPOINT=http://privacy-sidecar-1:8080,http://privacy-sidecar-2:8080,http://privacy-sidecar-3:8080
    networks: [aoeo]

networks:
  aoeo:
    driver: bridge
```

#### 客户端负载均衡能力

| 能力 | 说明 |
|------|------|
| **批量检测** | 一次请求含多条 message 时，合并为 `DetectBatch` 单 POST，减少 HTTP 往返 |
| **LeastLatency** | EWMA（指数加权移动平均）延迟排序，自动把请求发给最快的节点 |
| **RoundRobin** | 轮询分发，均匀负载 |
| **Random** | 随机分发 |
| **健康检查** | 每 10 秒自动探测，故障节点自动剔除，恢复后自动加入 |
| **故障转移** | 请求失败自动尝试下一个健康节点 |
| **连接预热** | 启动时自动对每个健康节点发一次预热请求，消除首包 TCP 握手延迟 |
| **HTTP/2** | 强制启用 HTTP/2，连接复用，降低多请求开销 |

### 单实例部署

```bash
docker-compose up -d
```

### 本地开发

```bash
# 1. 启动 sidecar
cd cmd/privacy-sidecar
MODEL_PATH=ckiplab/bert-base-chinese-ner python main.py

# 2. 启动你的 AoEo 应用（自动读取环境变量）
export AOEO_PRIVACY_ENABLED=true
export AOEO_PRIVACY_ENDPOINT=http://localhost:8080
go run ./cmd/aoeo
```

---

## 处理策略

| 策略 | 行为 | 适用场景 |
|------|------|----------|
| `ActionBlock` | 检测到敏感数据直接返回错误 | 高安全环境 |
| `ActionMask` | 替换为 `[REDACTED]` | 审计日志 |
| `ActionPseudonymize` | 替换为逼真的伪造值，返回时自动还原 | **生产推荐** |
| `ActionAudit` | 放行，仅记录日志 | 灰度观察 |

---

## 运行时统计

```go
gw, _ := privacy.NewGateway(cfg)

// 隐私网关级统计
stats := gw.Stats()
fmt.Printf("已伪匿名化: %d, 已还原: %d, 失败: %d, 检测到: %d\n",
    stats.RequestsPseudonymized,
    stats.RequestsRestored,
    stats.RequestsFailed,
    stats.SpansDetected,
)

// 多实例时，查看各 Sidecar 健康状态与延迟
// （需在手动配置模式下保存 LoadBalancedClient 引用）
```

---

## 核心原理

```
用户输入（原始值）
    │
    ▼
┌─────────────────────────────────────────┐
│  Privacy Gateway (BeforeRequest)        │
│  1. 批量 AI 模型检测敏感信息              │
│  2. 原始值 → 伪造值（写入 Pebble KV）    │
│  3. 替换请求文本                         │
└─────────────────────────────────────────┘
    │
    ▼
AI Provider（只能看到伪造值）
    │
    ▼
┌─────────────────────────────────────────┐
│  Privacy Gateway (AfterResponse)        │
│  4. 从本次请求的 mappings 还原           │
│  5. 伪造值 → 原始值                      │
└─────────────────────────────────────────┘
    │
    ▼
用户输出（再次看到原始值）
```

**关键设计**：
- `AfterResponse` 只还原**本次请求**产生的 fake 值，不会误碰历史 session 中的旧 mapping
- 多条 message 合并为一次 `DetectBatch` 调用，减少 HTTP 往返
- 模糊标点匹配：AI 常在伪造值后加 `.` `,` `!` 等，还原时自动处理
- 残留检测：扫描响应中是否还有未还原的 fake 值，发现即打 WARN 日志

---

## 批量检测（DetectBatch）

当请求包含多条 message 时，Privacy Gateway 不再逐一发送 HTTP 请求，而是：

1. 提取每条 message 的 `Content`
2. 合并为一次 `POST /detect/batch` 发送给 Sidecar
3. Sidecar 逐条检测，返回每条对应的 spans
4. 合并结果，统一生成 fake 值并替换

**效果**：N 条 message 从 N 次 HTTP 往返降为 1 次。

---

## 智能路由（LeastLatency）

当配置了多台 Sidecar 时，开启 `LeastLatency` 策略：

```go
gw, _ := privacy.NewGateway(privacy.GatewayConfig{
    ModelEndpoint: "http://s1:8080,http://s2:8080,http://s3:8080",
    LBStrategy:    model.LeastLatency,
})
```

- 每次成功的 `Detect` / `DetectBatch` 后，更新该节点的 EWMA 延迟
- 新请求优先发给延迟最低的节点
- 健康检查每 10 秒执行一次，故障节点自动剔除
- 节点恢复后自动重新参与路由

---

## 常见问题

**Q：Sidecar 宕机怎么办？**

A：设置 `AOEO_PRIVACY_FAILOPEN=true`（或 `FailOpen: true`），请求自动透传，业务不中断，同时打 WARN 日志。

**Q：多台 Sidecar 需要 Nginx 吗？**

A：不需要。Go 端内置 `LoadBalancedClient`，支持健康检查 + 故障转移 + LeastLatency 智能路由，直接写逗号分隔地址即可。

**Q：映射表会无限增长吗？**

A：不会。通过 `SessionTTL` 自动清理，或手动调用 `store.Cleanup`。

**Q：还原可靠吗？**

A：精确匹配 + 模糊标点匹配 + 残留检测告警。每个请求只还原自己产生的 fake 值，不会被历史数据污染。

**Q：批量检测会不会导致跨 message 的检测错误？**

A：不会。`DetectBatch` 对每条 message 独立检测，span offset 保持在各自 message 内部，替换时互不干扰。

---

## 文件清单

| 文件 | 说明 |
|------|------|
| `privacy/gateway.go` | 隐私网关核心（Interceptor 集成） |
| `privacy/pseudonymizer.go` | 伪匿名化器（检测→替换→还原） |
| `privacy/detector.go` | 检测器接口 |
| `privacy/model_adapter.go` | model.Client → Detector 适配器 |
| `privacy/option.go` | `WithPrivacyFilter()` / `WithPrivacyModel()` |
| `privacy/generator.go` | 伪造数据生成器 |
| `privacy/store/` | Pebble KV 映射存储 |
| `privacy/model/client.go` | Sidecar 客户端接口（含 DetectBatch） |
| `privacy/model/http.go` | HTTP/JSON 客户端（HTTP/2 + 连接池） |
| `privacy/model/loadbalancer.go` | 多后端负载均衡（LeastLatency + 健康检查 + 预热） |
| `cmd/privacy-sidecar/` | AI 检测 Sidecar（FastAPI + HuggingFace） |
| `Dockerfile` | AoEo 网关镜像 |
| `docker-compose.yml` | 一体化部署 |
| `examples/privacy/main.go` | 接入示例 |
