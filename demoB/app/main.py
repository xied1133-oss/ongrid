"""order-svc 入口：下单/查询路由 + 上游对账 worker。"""
import asyncio
import time
from contextlib import asynccontextmanager

from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.httpx import HTTPXClientInstrumentor
from prometheus_client import generate_latest
from pydantic import BaseModel, Field

from . import client, config, store, telemetry
from .telemetry import get_logger

telemetry.init()
log = get_logger("order-svc")

inventory = client.InventoryClient(config.INVENTORY_URL)


async def _reconcile_loop() -> None:
    """对账 worker：周期性查上游库存，提供稳态流量；上游故障时持续产出证据。"""
    await asyncio.sleep(2)
    while True:
        for sku in config.RECONCILE_SKUS:
            try:
                stock = await inventory.get_stock(sku)
                if stock is not None:
                    log.info("reconcile ok", fields={"sku": sku, "stock": stock})
            except client.UpstreamError as exc:
                telemetry.UPSTREAM_ERRORS.labels(
                    upstream="inventory-svc", reason=exc.reason).inc()
                log.error("upstream reconcile failed", fields={
                    "upstream": f"{config.INVENTORY_URL}/api/v1/inventory/{sku}",
                    "reason": exc.reason, "sku": sku})
        await asyncio.sleep(config.RECONCILE_INTERVAL)


@asynccontextmanager
async def lifespan(_: FastAPI):
    store.init_db()
    worker = asyncio.create_task(_reconcile_loop())
    log.info("order-svc started", fields={
        "inventory_url": config.INVENTORY_URL, "database_url": config.DATABASE_URL})
    yield
    worker.cancel()
    await inventory.aclose()


app = FastAPI(title=config.SERVICE_NAME, version=config.SERVICE_VERSION, lifespan=lifespan)
FastAPIInstrumentor.instrument_app(app)
HTTPXClientInstrumentor().instrument()


class DeductRequest(BaseModel):
    sku: str
    qty: int = Field(ge=1)


def _observe(endpoint: str, code: int, started: float) -> None:
    telemetry.REQUESTS.labels(endpoint=endpoint, code=str(code)).inc()
    telemetry.DURATION.labels(endpoint=endpoint).observe(time.monotonic() - started)


@app.get("/healthz")
async def healthz() -> dict:
    return {"status": "ok", "service": config.SERVICE_NAME}


@app.get("/metrics")
async def metrics() -> Response:
    return Response(content=generate_latest(), media_type="text/plain; charset=utf-8")


@app.post("/api/v1/orders", status_code=201)
async def create_order(req: DeductRequest) -> dict:
    started = time.monotonic()
    order_id = store.create_order(req.sku, req.qty)
    try:
        result = await inventory.deduct(req.sku, req.qty)
    except client.UpstreamError as exc:
        store.set_status(order_id, "failed", exc.reason)
        telemetry.UPSTREAM_ERRORS.labels(
            upstream="inventory-svc", reason=exc.reason).inc()
        _observe("create_order", 502, started)
        # 回溯关键日志：带上游地址与失败原因
        log.error("upstream deduct failed", fields={
            "upstream": f"{config.INVENTORY_URL}/api/v1/inventory/deduct",
            "reason": exc.reason, "order_id": order_id, "sku": req.sku})
        raise HTTPException(status_code=502, detail={
            "order_id": order_id, "reason": exc.reason}) from exc
    if "business_error" in result:
        code = result["business_error"]
        reason = "out_of_stock" if code == 409 else f"upstream_{code}"
        store.set_status(order_id, "rejected", reason)
        _observe("create_order", code, started)
        log.info("order rejected", fields={
            "order_id": order_id, "sku": req.sku, "reason": reason})
        raise HTTPException(status_code=code, detail={
            "order_id": order_id, "reason": reason})
    store.set_status(order_id, "confirmed")
    _observe("create_order", 201, started)
    log.info("order confirmed", fields={
        "order_id": order_id, "sku": req.sku, "qty": req.qty})
    return {"order_id": order_id, "status": "confirmed", "sku": req.sku, "qty": req.qty}


@app.get("/api/v1/orders")
async def list_orders(limit: int = 20) -> dict:
    started = time.monotonic()
    orders = store.list_orders(min(max(limit, 1), 200))
    _observe("list_orders", 200, started)
    return {"orders": orders}
