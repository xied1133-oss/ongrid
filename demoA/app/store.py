"""Redis 库存仓库：Redis 作为库存 Source of Truth，DECRBY 原子扣减。"""
import redis

from . import config


class OutOfStock(Exception):
    """库存不足（业务拒绝，非基础设施故障）。"""


class InventoryStore:
    def __init__(self, url: str):
        self._conn = redis.Redis.from_url(
            url, socket_connect_timeout=1.0, socket_timeout=1.0, decode_responses=True)

    def seed(self) -> None:
        """初始库存写入；NX 语义保证重启不覆盖现有库存。"""
        for sku, qty in config.SEED_STOCK.items():
            self._conn.set(sku, qty, nx=True)

    def get(self, sku: str) -> int | None:
        raw = self._conn.get(sku)
        return None if raw is None else int(raw)

    def deduct(self, sku: str, qty: int) -> int:
        """扣减并返回剩余库存；超卖时回补并抛 OutOfStock。"""
        left = int(self._conn.decrby(sku, qty))
        if left < 0:
            self._conn.incrby(sku, qty)
            raise OutOfStock(sku)
        return left
