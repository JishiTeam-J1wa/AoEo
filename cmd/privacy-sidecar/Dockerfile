FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY model.py main.py ./

# Mount your model at runtime:
#   docker run -v /path/to/model:/app/models:ro -e MODEL_PATH=/app/models ...
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/health')" || exit 1

EXPOSE 8080

ENV PYTHONUNBUFFERED=1
ENV MODEL_PATH=/app/models
ENV DEVICE=cpu
ENV PORT=8080

CMD ["python", "main.py"]
