"""AoEo Privacy Filter Sidecar — OPF Proxy.

Lightweight proxy that bridges AoEo's /detect API to OpenAI Privacy Filter's
/redact API. No ML model loading required — all detection is delegated to OPF.

API (backward-compatible with legacy sidecar):
  POST /detect       -> {"text": "..."}       -> {"text": "...", "spans": [...]}
  POST /detect/batch -> {"texts": [...]}       -> {"results": [{"spans": [...]}]}
  GET  /health       ->                        -> {"status": "ok"}

  Also exposes OPF-native endpoints:
  POST /redact       -> {"text": "..."}       -> full OPF response
  POST /redact/batch -> {"texts": [...]}      -> full OPF batch response

Environment variables:
  OPF_ENDPOINT  - URL of the OPF service (default: http://opf:8000)
  PORT          - listen port (default: 8080)
  LOG_LEVEL     - debug|info|warning|error (default: info)
  TIMEOUT       - upstream request timeout in seconds (default: 30)
"""

import logging
import os
import time
from contextlib import asynccontextmanager

import httpx
from fastapi import FastAPI, HTTPException, Request
from pydantic import BaseModel, Field
from starlette.responses import JSONResponse

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

OPF_ENDPOINT = os.environ.get("OPF_ENDPOINT", "http://opf:8000")
PORT = int(os.environ.get("PORT", "8080"))
TIMEOUT = float(os.environ.get("TIMEOUT", "30"))
LOG_LEVEL = os.environ.get("LOG_LEVEL", "info").upper()

logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger("privacy-sidecar")

# ---------------------------------------------------------------------------
# Lifecycle
# ---------------------------------------------------------------------------

_http: httpx.AsyncClient | None = None


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _http
    _http = httpx.AsyncClient(
        base_url=OPF_ENDPOINT,
        timeout=httpx.Timeout(TIMEOUT, connect=5.0),
        limits=httpx.Limits(max_connections=100, max_keepalive_connections=20),
    )
    logger.info("Proxy ready — forwarding to %s", OPF_ENDPOINT)

    # Verify OPF is reachable at startup (non-blocking).
    try:
        resp = await _http.get("/health")
        if resp.status_code == 200:
            logger.info("OPF backend reachable: %s", resp.json())
        else:
            logger.warning("OPF health returned %d", resp.status_code)
    except Exception as exc:
        logger.warning("OPF backend not reachable at startup: %s", exc)

    yield
    await _http.aclose()
    logger.info("Shutting down privacy sidecar proxy")


app = FastAPI(
    title="AoEo Privacy Filter Sidecar (OPF Proxy)",
    description="PII detection proxy for OpenAI Privacy Filter",
    version="2.0.0",
    lifespan=lifespan,
)


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
# Shared models
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


# ---------------------------------------------------------------------------
# OPF label normalization (mirrors Go-side normalizeOPFLabel)
# ---------------------------------------------------------------------------

_LABEL_MAP = {
    "NAME": "person",
    "PERSON": "person",
    "PER": "person",
    "EMAIL_ADDRESS": "email",
    "EMAIL": "email",
    "PHONE_NUMBER": "phone",
    "PHONE": "phone",
    "TEL": "phone",
    "IP_ADDRESS": "ip",
    "IP": "ip",
    "CREDIT_CARD": "secret",
    "CRYPTO": "secret",
    "IBAN_CODE": "secret",
    "US_BANK_NUMBER": "secret",
    "US_DRIVER_LICENSE": "idcard",
    "US_SSN": "idcard",
    "SSN": "idcard",
    "IDCARD": "idcard",
    "ID": "idcard",
    "MEDICAL_LICENSE": "secret",
    "URL": "url",
    "DATE_TIME": "date",
    "DATE": "date",
    "LOCATION": "address",
    "ADDRESS": "address",
    "ADDR": "address",
    "NRP": "secret",
    "DOMAIN": "domain",
    "SECRET": "secret",
}


def _normalize_label(label: str) -> str:
    return _LABEL_MAP.get(label.upper(), "secret")


def _opf_span_to_legacy(span: dict) -> dict:
    """Convert an OPF detected_span to the legacy sidecar span format."""
    return {
        "label": _normalize_label(span.get("label", "")),
        "text": span.get("text", ""),
        "start": span.get("start", 0),
        "end": span.get("end", 0),
        "score": 1.0,  # OPF does not provide per-span scores
    }


# ---------------------------------------------------------------------------
# Legacy endpoints (/detect, /detect/batch) — backward compatible
# ---------------------------------------------------------------------------


@app.post("/detect", response_model=DetectResponse)
async def detect(req: DetectRequest):
    """Detect PII in a single text. Proxies to OPF /redact."""
    if _http is None:
        raise HTTPException(status_code=503, detail="Not ready")

    start = time.time()
    try:
        resp = await _http.post("/redact", json={"text": req.text})
    except httpx.TimeoutException:
        raise HTTPException(status_code=504, detail="OPF upstream timeout")
    except httpx.HTTPError as exc:
        raise HTTPException(status_code=502, detail=f"OPF error: {exc}")

    if resp.status_code != 200:
        raise HTTPException(status_code=resp.status_code, detail=resp.text)

    data = resp.json()
    spans = [_opf_span_to_legacy(s) for s in data.get("detected_spans", [])]
    latency = time.time() - start

    logger.info(
        "detected %d spans in %.3fs (text_len=%d, opf_latency=%.1fms)",
        len(spans), latency, len(req.text), data.get("latency_ms", 0),
    )

    return DetectResponse(text=req.text, spans=spans)


@app.post("/detect/batch", response_model=BatchDetectResponse)
async def detect_batch(req: BatchDetectRequest):
    """Detect PII in multiple texts. Proxies to OPF /redact/batch."""
    if _http is None:
        raise HTTPException(status_code=503, detail="Not ready")

    start = time.time()
    try:
        resp = await _http.post("/redact/batch", json={"texts": req.texts})
    except httpx.TimeoutException:
        raise HTTPException(status_code=504, detail="OPF upstream timeout")
    except httpx.HTTPError as exc:
        raise HTTPException(status_code=502, detail=f"OPF error: {exc}")

    if resp.status_code != 200:
        raise HTTPException(status_code=resp.status_code, detail=resp.text)

    data = resp.json()
    results = []
    for r in data.get("results", []):
        spans = [_opf_span_to_legacy(s) for s in r.get("detected_spans", [])]
        results.append(BatchDetectResult(spans=spans))

    latency = time.time() - start
    logger.info(
        "batch detected %d texts in %.3fs (opf_latency=%.1fms)",
        len(req.texts), latency, data.get("total_latency_ms", 0),
    )
    return BatchDetectResponse(results=results)


# ---------------------------------------------------------------------------
# OPF-native endpoints (/redact, /redact/batch) — pass-through
# ---------------------------------------------------------------------------


@app.post("/redact")
async def redact_passthrough(req: DetectRequest):
    """Pass-through to OPF /redact. Returns full OPF response."""
    if _http is None:
        raise HTTPException(status_code=503, detail="Not ready")
    try:
        resp = await _http.post("/redact", json={"text": req.text})
    except httpx.TimeoutException:
        raise HTTPException(status_code=504, detail="OPF upstream timeout")
    except httpx.HTTPError as exc:
        raise HTTPException(status_code=502, detail=f"OPF error: {exc}")

    return JSONResponse(content=resp.json(), status_code=resp.status_code)


@app.post("/redact/batch")
async def redact_batch_passthrough(req: BatchDetectRequest):
    """Pass-through to OPF /redact/batch. Returns full OPF response."""
    if _http is None:
        raise HTTPException(status_code=503, detail="Not ready")
    try:
        resp = await _http.post("/redact/batch", json={"texts": req.texts})
    except httpx.TimeoutException:
        raise HTTPException(status_code=504, detail="OPF upstream timeout")
    except httpx.HTTPError as exc:
        raise HTTPException(status_code=502, detail=f"OPF error: {exc}")

    return JSONResponse(content=resp.json(), status_code=resp.status_code)


# ---------------------------------------------------------------------------
# Health & Info
# ---------------------------------------------------------------------------


@app.get("/health")
async def health():
    """Proxy health check. Returns ok if both proxy and OPF are healthy."""
    opf_ok = False
    if _http is not None:
        try:
            resp = await _http.get("/health")
            opf_ok = resp.status_code == 200
        except Exception:
            pass
    status = "ok" if opf_ok else "degraded"
    code = 200 if opf_ok else 503
    return JSONResponse(
        content={
            "status": status,
            "model_loaded": opf_ok,
            "opf_endpoint": OPF_ENDPOINT,
        },
        status_code=code,
    )


@app.get("/")
async def root():
    return {
        "service": "AoEo Privacy Filter Sidecar (OPF Proxy)",
        "opf_endpoint": OPF_ENDPOINT,
        "version": "2.0.0",
    }


# ---------------------------------------------------------------------------
# Entrypoint
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=PORT)
