# AoEo Privacy Filter Sidecar

基于 HuggingFace Transformers 的 PII 检测服务，暴露 HTTP `/detect` 和 `/health` 接口供 AoEo 隐私网关调用。

## 快速开始

```bash
pip install -r requirements.txt
MODEL_PATH=ckiplab/bert-base-chinese-ner python main.py
```

测试：
```bash
curl -X POST http://localhost:8080/detect \
  -H 'Content-Type: application/json' \
  -d '{"text": "我叫张三，手机号13800138000"}'
```

## Docker

```bash
docker build -t aoeo-privacy-sidecar .
docker run -p 8080:8080 \
  -e MODEL_PATH=/app/models \
  -v /path/to/your/model:/app/models:ro \
  aoeo-privacy-sidecar
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MODEL_PATH` | `/app/models` | HuggingFace 模型名或本地路径 |
| `DEVICE` | `cpu` | `cpu` 或 `cuda` |
| `MAX_LENGTH` | `512` | 最大输入 token 数 |
| `PORT` | `8080` | 监听端口 |
| `LOG_LEVEL` | `info` | 日志级别 |
