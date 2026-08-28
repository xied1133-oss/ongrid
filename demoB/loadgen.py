"""下单流量发生器（仅 stdlib）：每 2s 向 order-svc 下一单，约 0.5 req/s。"""
import json
import os
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone

ORDER_URL = os.environ.get("ORDER_URL", "http://127.0.0.1:18002").rstrip("/")
ORDER_BODY = json.dumps({"sku": "SKU-1001", "qty": 1}).encode()
INTERVAL = float(os.environ.get("ORDER_INTERVAL", "2"))


def _log(msg: str, **fields) -> None:
    payload = {"ts": datetime.now(timezone.utc).isoformat(timespec="milliseconds"),
               "level": fields.pop("level", "INFO"), "service": "order-loadgen",
               "msg": msg, **fields}
    print(json.dumps(payload, ensure_ascii=False), flush=True)


def place_order() -> None:
    req = urllib.request.Request(
        f"{ORDER_URL}/api/v1/orders", data=ORDER_BODY,
        headers={"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            body = json.loads(resp.read())
            _log("order placed", status=resp.status, order_id=body.get("order_id"))
    except urllib.error.HTTPError as exc:
        _log("order rejected" if exc.code == 409 else "order failed",
             level="ERROR", status=exc.code, error=exc.reason)
    except Exception as exc:
        _log("order failed", level="ERROR", error=str(exc))


def main() -> None:
    _log("loadgen started", order_url=ORDER_URL, interval=INTERVAL)
    while True:
        place_order()
        time.sleep(INTERVAL)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(0)
