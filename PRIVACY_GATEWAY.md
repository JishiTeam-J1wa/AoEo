# AoEo Privacy Gateway 使用手册

> 一个透明的可逆隐私代理层，确保敏感信息永远不会以明文形式出网。

---

## 1. 为什么需要 Privacy Gateway

在使用多 Provider AI API 时，用户的请求中往往包含敏感信息：

- **个人信息**：姓名、手机号、身份证号、地址
- **企业资产**：内网 IP、内部域名、API 密钥、员工工号
- **合规敏感词**：机密、绝密、内部资料

传统方案是在应用层手动脱敏，但容易遗漏、代码侵入性强、无法做到"用户看到原始值，AI 只看到伪造值"。

AoEo Privacy Gateway 通过 Interceptor 机制在 Scheduler 层统一处理，对业务代码完全透明。

---

## 2. 核心原理

```
用户输入（原始值）
    │
    ▼
┌─────────────────────────────────────────┐
│  Privacy Gateway (BeforeRequest)        │
│  1. 检测：Privacy Filter 模型 + 规则引擎 │
│  2. 替换：原始值 → 伪造值                │
│  3. 存储：原始值 ↔ 伪造值 写入映射表      │
└─────────────────────────────────────────┘
    │
    ▼
AI Provider（只能看到伪造值）
    │
    ▼
┌─────────────────────────────────────────┐
│  Privacy Gateway (AfterResponse)        │
│  4. 回溯：伪造值 → 原始值（查映射表）      │
└─────────────────────────────────────────┘
    │
    ▼
用户输出（再次看到原始值）
```

**关键特性**：
- **一致性**：同一次会话中，同一个原始值始终映射到同一个伪造值
- **可逆性**：所有替换都可以精确还原
- **透明性**：用户无感知，无需修改业务代码
- **本地化**：映射表保存在本地 SQLite，敏感数据不出境

---

## 3. 快速开始

### 3.1 创建规则文件

创建 `privacy_rules.yaml`：

```yaml
version: "1.0"

ip_rules:
  blocklist:
    - id: "ip-attack-001"
      value: "191.1.1.1"
      category: "attack_ip"
      severity: critical
      action: block

  allowlist:
    - id: "ip-internal-001"
      value: "192.2.2.2"
      category: "internal_ip"
      severity: medium
      action: mask

  cidr_blocks:
    - id: "cidr-internal-001"
      value: "10.0.0.0/8"
      category: "internal_network"
      severity: medium
      action: mask

domain_rules:
  allowlist:
    - id: "domain-internal-001"
      value: "www.x1.com"
      category: "internal_domain"
      severity: high
      action: mask

keyword_rules:
  - id: "kw-001"
    value: "机密"
    category: "confidential_mark"
    severity: high
    action: block
```

### 3.2 接入 AoEo

```go
package main

import (
    "context"
    "fmt"
    "log"

    aoeo "github.com/JishiTeam-J1wa/AoEo"
    "github.com/JishiTeam-J1wa/AoEo/privacy"
    "github.com/JishiTeam-J1wa/AoEo/storage"
)

func main() {
    // 1. 加载规则
    rules, err := privacy.LoadRuleDatabase("privacy_rules.yaml")
    if err != nil {
        log.Fatalf("load rules: %v", err)
    }

    // 2. 创建持久化存储（生产环境建议使用 MySQL/Postgres）
    store, err := storage.NewSQLite("privacy_mappings.db")
    if err != nil {
        log.Fatalf("new storage: %v", err)
    }
    defer store.Close()

    // 3. 创建隐私网关
    gateway, err := privacy.NewGateway(privacy.GatewayConfig{
        Rules:   privacy.NewRuleEngine(rules),
        Policy:  privacy.ActionPseudonymize,
        Storage: store, // 注入持久化后端
    })
    if err != nil {
        log.Fatalf("new gateway: %v", err)
    }
    defer gateway.Close()

    // 4. 创建 AoEo 客户端，注入隐私拦截器
    client, err := aoeo.NewClient(
        aoeo.Config{
            Providers: []aoeo.ProviderConfig{
                {
                    Name:     "deepseek",
                    APIKey:   "sk-xxx",
                    Endpoint: "https://api.deepseek.com",
                    Model:    "deepseek-v4-pro",
                },
            },
        },
        aoeo.WithInterceptors(gateway.ToInterceptor()),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // 5. 正常调用
    resp, err := client.ChatComplete(context.Background(),
        aoeo.BuildRequest([]aoeo.Message{
            {Role: "user", Content: "我叫张三，服务器IP是192.168.1.100"},
        }),
    )
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(resp.Content())
}
```

---

## 4. 四种处理策略

| 策略 | 行为 | 适用场景 |
|---|---|---|
| ActionBlock | 检测到敏感数据直接返回错误，请求不出网 | 高安全环境 |
| ActionMask | 替换为 [REDACTED] | 审计日志 |
| ActionPseudonymize | 替换为逼真的伪造值，返回时自动还原 | **生产推荐** |
| ActionAudit | 放行，仅记录审计日志 | 灰度观察 |

---

## 5. 配置文件详解

### IP 规则

```yaml
ip_rules:
  blocklist:      # 精确匹配
    - id: "ip-001"
      value: "191.1.1.1"
      action: block

  allowlist:      # 精确匹配，优先级高于 blocklist
    - id: "ip-safe"
      value: "8.8.8.8"
      action: audit

  cidr_blocks:    # 网段匹配
    - id: "cidr-001"
      value: "10.0.0.0/8"
      action: mask
```

### 域名规则

```yaml
domain_rules:
  blocklist:
    - id: "domain-bad"
      value: "malware.example.com"
      action: block

  allowlist:
    - id: "domain-internal"
      value: "www.x1.com"
      action: mask

  regex_patterns:
    - id: "domain-regex"
      pattern: ".*\\.internal\\.company\\.com$"
      action: mask
```

### 关键词规则

```yaml
keyword_rules:
  - id: "kw-001"
    value: "机密"
    action: block
```

### 正则规则

```yaml
regex_rules:
  - id: "regex-001"
    pattern: "[A-Z]{2}\\d{8}"   # 工号格式
    action: mask
```

---

## 6. 会话管理

通过 context 或 Tag 指定 Session ID：

```go
ctx := context.WithValue(context.Background(), "privacy_session_id", "user-123")
resp, _ := client.ChatComplete(ctx, req)
```

映射表 TTL 清理：

```go
gateway, _ := privacy.NewGateway(privacy.GatewayConfig{
    SessionTTL: 7 * 24 * time.Hour,
})
```

---

## 7. 流式调用支持

```go
stream, _ := client.ChatCompleteStream(ctx, req)
for chunk := range stream {
    fmt.Print(chunk.Chunk.Delta.Content)  // 已自动回溯还原
}
```

---

## 8. 与 Privacy Filter 模型集成（可选，预留接口）

> ⚠️ **当前状态**：`ModelDetector` 接口已预留，但 ONNX 运行时实现尚未完成。以下为 planned API，供参考。

```go
// TODO: 需自行实现 ONNX 运行时适配器
// model := privacy.NewONNXDetector("./models/openai-privacy-filter")

gateway, _ := privacy.NewGateway(privacy.GatewayConfig{
    Rules:         privacy.NewRuleEngine(rules),
    // ModelDetector: model,  // 预留接口
    Policy:        privacy.ActionPseudonymize,
})
```

---

## 9. 常见问题

**Q：如果 AI 响应中生成了与伪造值相同的文本，会被错误还原吗？**

A：伪造值是随机生成的，碰撞概率极低。即使碰撞，语义上通常可接受。

**Q：映射表会无限增长吗？**

A：不会。通过 SessionTTL 自动清理，或手动调用 store.Cleanup。

**Q：规则文件修改后需要重启吗？**

A：当前版本需要重新 LoadRuleDatabase。后续版本支持热重载。

---

## 10. 文件清单

| 文件 | 说明 |
|---|---|
| `core/storage.go` | 统一持久化接口定义（CallHistory / AuditLog / PrivacyMapping） |
| `storage/base.go` | 公共 SQL CRUD 实现（自动适配 SQLite `?` / MySQL `?` / Postgres `$N`） |
| `storage/sqlite.go` | SQLite 后端工厂（`:memory:` 或文件路径） |
| `storage/mysql.go` | MySQL 后端工厂 |
| `storage/postgres.go` | PostgreSQL 后端工厂 |
| `privacy/types.go` | Privacy 类型定义 |
| `privacy/store.go` | 映射表兼容层（基于 `core.Storage`） |
| `privacy/generator.go` | 伪造数据生成器 |
| `privacy/detector.go` | 检测器接口（含 `ModelDetector` 预留） |
| `privacy/rules.go` | 本地规则引擎 |
| `privacy/pseudonymizer.go` | 核心伪匿名化器 |
| `privacy/gateway.go` | AoEo Interceptor 集成 |
| `examples/privacy/main.go` | 完整示例 |
| `examples/privacy/privacy_rules.yaml` | 示例规则 |
