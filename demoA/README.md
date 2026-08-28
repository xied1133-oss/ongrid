# demoA — inventory-svc（库存服务）

RCA 跨项目根因回溯演示的**故障源**项目。真实电商形态：Redis 作为库存 Source of Truth，
对外提供查询/扣减 API，被 demoB（order-svc）在下单链路中同步调用。

## 端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查（含当前 chaos 模式） |
| GET | `/metrics` | Prometheus 指标 |
| GET | `/api/v1/inventory/{sku}` | 查库存 |
| POST | `/api/v1/inventory/deduct` | 扣库存 `{sku, qty}`，超卖 409 |
| GET/POST | `/chaos` | 故障注入：`ok` / `slow`（睡 5s）/ `error`（500） |

## 可观测性

- traces：OTLP HTTP → 主机 otelcol `127.0.0.1:4318`（service.name=inventory-svc，含 redis client span）
- metrics：`inventory_requests_total` / `inventory_request_duration_seconds` / `inventory_dependency_errors_total{dep="redis"}` / `inventory_stock{sku}`
- logs：stdout 单行 JSON（带 trace_id）→ journald → Loki

## 部署

主机侧由 `demoB/deploy.sh` 统一拉起（host 网络，端口 18001，redis 16379）。
