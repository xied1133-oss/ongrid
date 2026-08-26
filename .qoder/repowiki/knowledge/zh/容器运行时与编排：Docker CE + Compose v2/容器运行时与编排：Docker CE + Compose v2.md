---
kind: external_dependency
name: 容器运行时与编排：Docker CE + Compose v2
slug: docker
category: external_dependency
category_hints:
    - client_constraint
scope:
    - '**'
source_files:
    - deploy/docker-compose.yml
    - deploy/install/README.md
    - deploy/one-click-deploy.sh
---

Ongrid 云端以 Docker Compose 形态部署，依赖 Docker CE ≥ 24.0 及 `docker compose` 子命令（v2），不支持旧版 `docker-compose`。在 Alibaba Cloud Linux 3 上需通过阿里云镜像源安装（`yum install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin`），并手动把仓库 `$releasever` 固定为 8；CentOS 7 等老系统会因 OpenSSL 1.0.2k 无法生成 ED25519 密钥而失败，官方支持列表为 Ubuntu 22.04+ / Debian 12+ / Rocky 9。

Compose 文件启动 MySQL、manager、frontier、nginx、Prometheus、Grafana、Loki、Tempo、qdrant、SearXNG 等容器，数据卷 bind-mount 到宿主机 `/var/lib/ongrid`，日志输出到 `/var/log/ongrid`。Edge Agent 不在容器中运行，而是作为 systemd 服务安装在被管主机上，通过反向隧道主动外联云端 40012 端口。