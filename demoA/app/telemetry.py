"""可观测三件套接线：OTel traces + Prometheus 指标 + 带 trace_id 的 JSON 日志。"""
import json
import logging
import sys
from datetime import datetime, timezone

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from prometheus_client import Counter, Gauge, Histogram

from . import config

# --- Prometheus 指标 ---
REQUESTS = Counter("inventory_requests_total", "inventory-svc HTTP 请求数", ["endpoint", "code"])
DURATION = Histogram("inventory_request_duration_seconds", "inventory-svc HTTP 请求耗时", ["endpoint"])
DEP_ERRORS = Counter("inventory_dependency_errors_total", "inventory-svc 依赖调用失败数", ["dep"])
STOCK = Gauge("inventory_stock", "inventory-svc 当前库存", ["sku"])


class _JsonFormatter(logging.Formatter):
    """单行 JSON 日志，自动附带当前 OTel trace_id/span_id，供日志-链路关联。"""

    def format(self, record: logging.LogRecord) -> str:
        payload = {
            "ts": datetime.now(timezone.utc).isoformat(timespec="milliseconds"),
            "level": record.levelname,
            "service": config.SERVICE_NAME,
            "msg": record.getMessage(),
        }
        fields = getattr(record, "fields", None)
        if fields:
            payload.update(fields)
        span_ctx = trace.get_current_span().get_span_context()
        if span_ctx.trace_id:
            payload["trace_id"] = format(span_ctx.trace_id, "032x")
            payload["span_id"] = format(span_ctx.span_id, "016x")
        if record.exc_info and record.exc_info[1] is not None:
            payload["error"] = str(record.exc_info[1])
        return json.dumps(payload, ensure_ascii=False)


class _FieldAdapter(logging.LoggerAdapter):
    """支持 log.info("msg", fields={...}) 的结构化字段写法。"""

    def process(self, msg, kwargs):
        fields = kwargs.pop("fields", None)
        if fields is not None:
            kwargs.setdefault("extra", {})["fields"] = fields
        return msg, kwargs


def get_logger(name: str) -> logging.LoggerAdapter:
    return _FieldAdapter(logging.getLogger(name), {})


def init() -> None:
    """初始化 TracerProvider 与根日志 handler，进程启动时调用一次。"""
    resource = Resource.create({
        "service.name": config.SERVICE_NAME,
        "service.version": config.SERVICE_VERSION,
        "deployment.environment": "demo-rca",
    })
    provider = TracerProvider(resource=resource)
    provider.add_span_processor(BatchSpanProcessor(
        OTLPSpanExporter(endpoint=f"{config.OTEL_ENDPOINT}/v1/traces")))
    trace.set_tracer_provider(provider)

    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(_JsonFormatter())
    root = logging.getLogger()
    root.handlers.clear()
    root.addHandler(handler)
    root.setLevel(logging.INFO)
    # 第三方库日志降噪，避免刷屏 journald
    for noisy in ("uvicorn.access", "httpx", "opentelemetry"):
        logging.getLogger(noisy).setLevel(logging.WARNING)
