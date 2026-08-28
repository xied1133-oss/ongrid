# demoB — order-svc（订单服务，告警方）

真实业务形态的订单服务：下单时同步调用 demoA 的 inventory-svc 扣减库存，
扣减**不重试**（扣减非幂等），连续失败触发熔断。inventory-svc 故障时，
本服务的 `order_upstream_errors_total` 与 502 日志即告警来源，供 RCA 从
订单侧回溯到库存侧根因。

## 组件

| 容器 | 端口 | 说明 |
|---|---|---|
| `order-svc` | 18002 | FastAPI + SQLAlchemy(sqlite) + httpx 调上游 + 熔断器 + 对账 worker |
| `order-loadgen` | - | stdlib 下单循环，每 2s 一单 ≈0.5 req/s |

## API

- `POST /api/v1/orders` body `{"sku":"SKU-1001","qty":1}` → 201 confirmed / 409 rejected / 502 failed（`detail.reason` 闭集 `timeout|refused|http_5xx|circuit_open`）
- `GET /api/v1/orders?limit=20`
- `GET /healthz`、`GET /metrics`

## 观测

- 指标：`order_requests_total{endpoint,code}`、`order_upstream_errors_total{upstream,reason}`、`order_duration_seconds`、`order_circuit_open`
- 日志：stdout 单行 JSON 带 `trace_id`，经 journald 进 Loki
- Trace：OTLP HTTP → 主机 otelcol 4318（FastAPI/httpx/SQLAlchemy 三层注入）
- 告警规则示例：`sum by (device_id) (rate(order_upstream_errors_total[1m])) > 0.2`

## 故障传播链

inventory-svc 慢/挂 → httpx timeout/refused → 熔断打开（`order_circuit_open=1`）→
下单 502 + 对账 ERROR → `order_upstream_errors_total` 速率越线 → 告警。

## 部署

见 `demoB/deploy.sh`（主机侧一键起两套 compose + 自检）。
