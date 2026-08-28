"""SQLAlchemy 订单仓库：默认 sqlite 落盘，DATABASE_URL 可换 MySQL。"""
from datetime import datetime, timezone

from opentelemetry.instrumentation.sqlalchemy import SQLAlchemyInstrumentor
from sqlalchemy import Column, DateTime, Integer, String, create_engine
from sqlalchemy.orm import declarative_base, sessionmaker

from . import config

_connect_args = {"check_same_thread": False} if config.DATABASE_URL.startswith("sqlite") else {}
engine = create_engine(config.DATABASE_URL, connect_args=_connect_args)
SQLAlchemyInstrumentor().instrument(engine=engine)
Session = sessionmaker(bind=engine, expire_on_commit=False)
Base = declarative_base()


class Order(Base):
    __tablename__ = "orders"
    id = Column(Integer, primary_key=True, autoincrement=True)
    sku = Column(String(64), nullable=False)
    qty = Column(Integer, nullable=False)
    # pending → confirmed / failed(上游故障) / rejected(业务拒绝)
    status = Column(String(16), nullable=False, default="pending")
    reason = Column(String(32), nullable=True)
    created_at = Column(DateTime, default=lambda: datetime.now(timezone.utc))


def init_db() -> None:
    Base.metadata.create_all(engine)


def create_order(sku: str, qty: int) -> int:
    with Session() as s:
        row = Order(sku=sku, qty=qty, status="pending")
        s.add(row)
        s.commit()
        return row.id


def set_status(order_id: int, status: str, reason: str | None = None) -> None:
    with Session() as s:
        row = s.get(Order, order_id)
        if row is not None:
            row.status = status
            row.reason = reason
            s.commit()


def list_orders(limit: int) -> list[dict]:
    with Session() as s:
        rows = s.query(Order).order_by(Order.id.desc()).limit(limit).all()
        return [{"id": r.id, "sku": r.sku, "qty": r.qty, "status": r.status,
                 "reason": r.reason} for r in rows]
