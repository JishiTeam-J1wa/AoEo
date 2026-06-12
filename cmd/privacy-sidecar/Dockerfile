FROM python:3.12-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY main.py model.py ./

# Create non-root user
RUN useradd -m -s /bin/bash sidecar
USER sidecar

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/health')" || exit 1

EXPOSE 8080

ENV PYTHONUNBUFFERED=1
ENV OPF_ENDPOINT=http://opf:8000
ENV PORT=8080
ENV LOG_LEVEL=info

CMD ["python", "main.py"]
