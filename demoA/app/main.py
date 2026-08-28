"""inventory-svc 入口：库存查询/扣减 + chaos 注入端点 + 观测端点。"""
import asyncio
import time
from contextlib import asynccontextmanager

import redis as redis_lib
from fastapi import FastAPI, HTTPException, Query
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.redis import RedisInstrumentor
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest
from pydantic import BaseModel, Field
from starlette.responses import Response

from . import config, telemetry
from .store import InventoryStore, OutOfStock

telemetry.init()
log = telemetry.get_logger("inventory.main")
store = InventoryStore(config.REDIS_URL)

# chaos 注入模式：ok=正常；slow=处理前睡 5s（模拟慢查询/GC 停顿）；error=直接 500
_chaos_mode = "ok"
_VALID_MODES = ("ok", "slow", "error")


@asynccontextmanager
async def lifespan(_: FastAPI):
    try:
        store.seed()
        log.info("stock seeded", fields={"skus": sorted(config.SEED_STOCK)})
    except redis_lib.RedisError as exc:
        # redis 未就绪不阻塞启动，后续请求会正常记依赖错误
        log.warning("seed deferred: redis not ready", fields={"error": str(exc)})
    yield


app = FastAPI(title=config.SERVICE_NAME, lifespan=lifespan)
FastAPIInstrumentor.instrument_app(app)
RedisInstrumentor().instrument()


class DeductReq(BaseModel):
    sku: str
    qty: int = Field(default=1, ge=1)


async def _chaos_gate() -> None:
    """chaos 注入闸门：slow 模拟慢查询，error 模拟内部故障。"""
    if _chaos_mode == "slow":
        await asyncio.sleep(5)
    elif _chaos_mode == "error":
        raise HTTPException(status_code=500, detail="chaos injected")


def _observe(endpoint: str, code: int, started: float) -> None:
    telemetry.REQUESTS.labels(endpoint=endpoint, code=str(code)).inc()
    telemetry.DURATION.labels(endpoint=endpoint).observe(time.perf_counter() - started)


@app.get("/healthz")
async def healthz():
    return {"status": "ok", "service": config.SERVICE_NAME, "chaos": _chaos_mode}


@app.get("/metrics")
async def metrics():
    return Response(generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.get("/chaos")
async def get_chaos():
    return {"mode": _chaos_mode}


@app.post("/chaos")
async def set_chaos(mode: str = Query(...)):
    global _chaos_mode
    if mode not in _VALID_MODES:
        raise HTTPException(status_code=400, detail=f"mode must be one of {list(_VALID_MODES)}")
    old, _chaos_mode = _chaos_mode, mode
    log.warning("chaos mode changed", fields={"from": old, "to": mode})
    return {"mode": mode}


@app.get("/api/v1/inventory/{sku}")
async def get_stock(sku: str):
    started = time.perf_counter()
    await _chaos_gate()
    try:
        left = store.get(sku)
    except redis_lib.RedisError as exc:
        telemetry.DEP_ERRORS.labels(dep="redis").inc()
        log.error("redis dependency failure",
                  fields={"dep": "redis", "op": "get", "sku": sku, "error": str(exc)})
        _observe("get", 503, started)
        raise HTTPException(status_code=503, detail="inventory store unavailable")
    if left is None:
        _observe("get", 404, started)
        raise HTTPException(status_code=404, detail=f"unknown sku {sku}")
    telemetry.STOCK.labels(sku=sku).set(left)
    _observe("get", 200, started)
    log.info("stock queried", fields={"sku": sku, "left": left})
    return {"sku": sku, "stock": left}


@app.post("/api/v1/inventory/deduct")
async def deduct(req: DeductReq):
    started = time.perf_counter()
    await _chaos_gate()
    try:
        left = store.deduct(req.sku, req.qty)
    except redis_lib.RedisError as exc:
        telemetry.DEP_ERRORS.labels(dep="redis").inc()
        log.error("redis dependency failure",
                  fields={"dep": "redis", "op": "deduct", "sku": req.sku, "error": str(exc)})
        _observe("deduct", 503, started)
        raise HTTPException(status_code=503, detail="inventory store unavailable")
    except OutOfStock:
        _observe("deduct", 409, started)
        log.warning("deduct rejected: out of stock", fields={"sku": req.sku, "qty": req.qty})
        raise HTTPException(status_code=409, detail=f"out of stock: {req.sku}")
    telemetry.STOCK.labels(sku=req.sku).set(left)
    _observe("deduct", 200, started)
    log.info("deduct ok", fields={
        "sku": req.sku, "qty": req.qty, "left": left,
        "duration_ms": round((time.perf_counter() - started) * 1000, 1)})
    return {"sku": req.sku, "left": left}
