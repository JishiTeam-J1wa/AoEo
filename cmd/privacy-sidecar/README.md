# AoEo Privacy Filter Sidecar (OPF Proxy)

基于 OpenAI Privacy Filter 的 PII 检测代理服务。Sidecar 本身不加载 ML 模型，而是将请求代理到 OPF 服务，同时提供向后兼容的 `/detect` API 和 OPF 原生的 `/redact` API。

## 架构

```
AoEo Go SDK --> Sidecar (本服务) --> OPF (OpenAI Privacy Filter)
              /detect              /redact
              /detect/batch        /redact/batch
              /health              /health
```

## 快速开始

```bash
# 先启动 OPF 服务
docker run -d -p 8000:8000 --name opf ghcr.io/gh0stkey/opf-privacy-filter:latest

# 再启动 Sidecar
OPF_ENDPOINT=http://localhost:8000 python main.py
```

测试（兼容旧版 API）：
```bash
curl -X POST http://localhost:8080/detect \
  -H 'Content-Type: application/json' \
  -d '{"text": "My name is John and my email is john@example.com"}'
```

测试（OPF 原生 API 直通）：
```bash
curl -X POST http://localhost:8080/redact \
  -H 'Content-Type: application/json' \
  -d '{"text": "My name is John and my email is john@example.com"}'
```

## Docker Compose

推荐使用 docker-compose.yml 一键部署，包含 OPF + Sidecar + Nginx LB：

```bash
docker-compose up -d
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `OPF_ENDPOINT` | `http://opf:8000` | OPF 服务地址 |
| `PORT` | `8080` | Sidecar 监听端口 |
| `TIMEOUT` | `30` | 上游请求超时（秒） |
| `LOG_LEVEL` | `info` | 日志级别 |

## API 端点

### 向后兼容端点（legacy）

- `POST /detect` — 单文本 PII 检测，返回 `{text, spans}` 格式
- `POST /detect/batch` — 批量检测，返回 `{results: [{spans}]}` 格式

### OPF 原生端点（直通）

- `POST /redact` — 直通 OPF /redact，返回完整 OPF 响应
- `POST /redact/batch` — 直通 OPF /redact/batch

### 健康检查

- `GET /health` — 检查 Sidecar 和 OPF 后端状态
