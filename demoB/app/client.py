"""inventory-svc 调用客户端：不重试（扣减非幂等）+ 连续失败熔断器。"""
import time

import httpx

from . import telemetry


class UpstreamError(Exception):
    """上游基础设施故障；reason 闭集：timeout|refused|http_5xx|circuit_open。"""

    def __init__(self, reason: str):
        super().__init__(reason)
        self.reason = reason


class Breaker:
    """连续失败熔断器：threshold 次连续失败后打开 cooldown 秒，到期放一个探针。"""

    def __init__(self, threshold: int = 5, cooldown: float = 10.0):
        self._threshold = threshold
        self._cooldown = cooldown
        self._fails = 0
        self._open_until = 0.0

    def allow(self) -> bool:
        if self._fails < self._threshold:
            return True
        return time.monotonic() >= self._open_until

    def record_ok(self) -> None:
        self._fails = 0
        telemetry.CIRCUIT_OPEN.set(0)

    def record_fail(self) -> None:
        self._fails += 1
        if self._fails >= self._threshold:
            self._open_until = time.monotonic() + self._cooldown
            telemetry.CIRCUIT_OPEN.set(1)


class InventoryClient:
    def __init__(self, base_url: str):
        self.base = base_url.rstrip("/")
        self._http = httpx.AsyncClient(timeout=2.0)
        self.breaker = Breaker()

    async def aclose(self) -> None:
        await self._http.aclose()

    async def deduct(self, sku: str, qty: int) -> dict:
        """成功返回上游 body；4xx 业务拒绝返回 {"business_error": code}；基础设施故障抛 UpstreamError。"""
        if not self.breaker.allow():
            raise UpstreamError("circuit_open")
        try:
            resp = await self._http.post(
                f"{self.base}/api/v1/inventory/deduct", json={"sku": sku, "qty": qty})
        except httpx.TimeoutException as exc:
            self.breaker.record_fail()
            raise UpstreamError("timeout") from exc
        except httpx.NetworkError as exc:  # ConnectError 等传输层失败
            self.breaker.record_fail()
            raise UpstreamError("refused") from exc
        if resp.status_code >= 500:
            self.breaker.record_fail()
            raise UpstreamError("http_5xx")
        self.breaker.record_ok()
        if resp.status_code >= 400:
            return {"business_error": resp.status_code}
        return resp.json()

    async def get_stock(self, sku: str) -> int | None:
        """对账用查库存；非 200 的业务态返回 None，不计基础设施错误。"""
        if not self.breaker.allow():
            raise UpstreamError("circuit_open")
        try:
            resp = await self._http.get(f"{self.base}/api/v1/inventory/{sku}")
        except httpx.TimeoutException as exc:
            self.breaker.record_fail()
            raise UpstreamError("timeout") from exc
        except httpx.NetworkError as exc:
            self.breaker.record_fail()
            raise UpstreamError("refused") from exc
        if resp.status_code >= 500:
            self.breaker.record_fail()
            raise UpstreamError("http_5xx")
        self.breaker.record_ok()
        if resp.status_code != 200:
            return None
        return int(resp.json()["stock"])
