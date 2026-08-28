"""order-svc 运行配置，全部经环境变量注入。"""
import os

SERVICE_NAME = "order-svc"
SERVICE_VERSION = "1.0.0"

PORT = 18002
INVENTORY_URL = os.environ.get("INVENTORY_URL", "http://127.0.0.1:18001")
# 默认 sqlite 落盘；生产形态可经同一变量换 MySQL；部署时 compose 显式指定 /data 卷
DATABASE_URL = os.environ.get("DATABASE_URL", "sqlite:////tmp/orders.db")
OTEL_ENDPOINT = os.environ.get("OTEL_ENDPOINT", "http://127.0.0.1:4318")

# 对账 worker：周期同步上游库存，提供稳态流量与持续故障证据
RECONCILE_INTERVAL = float(os.environ.get("RECONCILE_INTERVAL", "3"))
RECONCILE_SKUS = ["SKU-1001", "SKU-1002"]
