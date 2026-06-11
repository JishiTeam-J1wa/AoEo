"""AoEo Privacy Filter Sidecar.

AI-based PII detection via HuggingFace NER models.

API:
  POST /detect  -> {"text": "..."}     -> {"text": "...", "spans": [...]}
  GET  /health  ->                       -> {"status": "ok"}

Environment variables:
  MODEL_PATH   - HuggingFace model name or local path (required)
  DEVICE       - "cpu" or "cuda" (default: cpu)
  MAX_LENGTH   - max input length in tokens (default: 512)
  PORT         - listen port (default: 8080)
  LOG_LEVEL    - debug|info|warning|error (default: info)
"""

import logging
import os
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException, Request
from pydantic import BaseModel
from starlette.responses import JSONResponse

from model import PrivacyModel, Span

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

MODEL_PATH = os.environ["MODEL_PATH"]
DEVICE = os.getenv("DEVICE", "cpu")
MAX_LENGTH = int(os.getenv("MAX_LENGTH", "512"))
PORT = int(os.getenv("PORT", "8080"))
LOG_LEVEL = os.getenv("LOG_LEVEL", "info").upper()

logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger("privacy-sidecar")

# ---------------------------------------------------------------------------
# Lifecycle
# ---------------------------------------------------------------------------

_model: PrivacyModel | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _model
    logger.info("Loading model from %s (device=%s)", MODEL_PATH, DEVICE)
    start = time.time()
    try:
        _model = PrivacyModel(MODEL_PATH, device=DEVICE, max_length=MAX_LENGTH)
        logger.info("Model ready in %.2fs", time.time() - start)
    except Exception as exc:
        logger.error("Failed to load model: %s", exc)
        raise
    yield
    logger.info("Shutting down privacy sidecar")


app = FastAPI(title="AoEo Privacy Filter Sidecar", lifespan=lifespan)


@app.middleware("http")
async def log_requests(request: Request, call_next):
    start = time.time()
    response = await call_next(request)
    latency = time.time() - start
    logger.info(
        "%s %s %d %.3fs",
        request.method,
        request.url.path,
        response.status_code,
        latency,
    )
    return response


# ---------------------------------------------------------------------------
# Endpoints
# ---------------------------------------------------------------------------


class DetectRequest(BaseModel):
    text: str


class DetectResponse(BaseModel):
    text: str
    spans: list[dict]


class BatchDetectRequest(BaseModel):
    texts: list[str]


class BatchDetectResult(BaseModel):
    spans: list[dict]


class BatchDetectResponse(BaseModel):
    results: list[BatchDetectResult]


@app.post("/detect", response_model=DetectResponse)
async def detect(req: DetectRequest):
    if not _model or not _model.ready:
        raise HTTPException(status_code=503, detail="Model not ready")

    start = time.time()
    spans = _model.detect(req.text)
    latency = time.time() - start

    logger.info("detected %d spans in %.3fs (text_len=%d)",
                len(spans), latency, len(req.text))
    for s in spans:
        logger.info("  span: label=%s text=%s score=%.3f",
                    s.label, s.text, s.score)

    return DetectResponse(
        text=req.text,
        spans=[s.to_dict() for s in spans],
    )


@app.post("/detect/batch", response_model=BatchDetectResponse)
async def detect_batch(req: BatchDetectRequest):
    if not _model or not _model.ready:
        raise HTTPException(status_code=503, detail="Model not ready")

    start = time.time()
    results = []
    for text in req.texts:
        spans = _model.detect(text)
        results.append(BatchDetectResult(
            spans=[s.to_dict() for s in spans],
        ))
    latency = time.time() - start

    logger.info("batch detected %d texts in %.3fs", len(req.texts), latency)
    return BatchDetectResponse(results=results)


@app.get("/health")
async def health():
    ok = _model is not None and _model.ready
    return JSONResponse(
        content={"status": "ok" if ok else "error"},
        status_code=200 if ok else 503,
    )


@app.get("/")
async def root():
    return {
        "service": "AoEo Privacy Filter Sidecar",
        "model": MODEL_PATH,
        "ready": _model.ready if _model else False,
    }


# ---------------------------------------------------------------------------
# Entrypoint
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=PORT)
