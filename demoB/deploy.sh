#!/usr/bin/env bash
# 主机侧一键部署：起 demoA（inventory）与 demoB（order）两套 compose 并自检。
# 前提：镜像已经过 docker load（inventory-svc / order-svc / redis:7-alpine）。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "== 启动 inventory-svc + inventory-redis =="
docker compose -f "$ROOT/demoA/compose.yml" up -d

echo "== 启动 order-svc + order-loadgen =="
docker compose -f "$ROOT/demoB/compose.yml" up -d

sleep 5

echo "== 健康自检 =="
curl -fsS http://127.0.0.1:18001/healthz && echo
curl -fsS http://127.0.0.1:18002/healthz && echo

echo "DEPLOY-OK"
