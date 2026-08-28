"""inventory-svc 运行配置，全部经环境变量注入。"""
import os

SERVICE_NAME = "inventory-svc"
SERVICE_VERSION = "1.0.0"

PORT = 18001
REDIS_URL = os.environ.get("REDIS_URL", "redis://127.0.0.1:16379/0")
OTEL_ENDPOINT = os.environ.get("OTEL_ENDPOINT", "http://127.0.0.1:4318")

# 启动时 SET NX 的初始库存（不覆盖已存在的值）
SEED_STOCK = {"SKU-1001": 10000, "SKU-1002": 5000}
