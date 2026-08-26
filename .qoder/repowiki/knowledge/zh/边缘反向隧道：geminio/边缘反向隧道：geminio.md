---
kind: external_dependency
name: 边缘反向隧道：geminio
slug: geminio-tunnel
category: external_dependency
category_hints:
    - sdk_real_api
scope:
    - '**'
source_files:
    - go.mod
---

Edge Agent 通过 geminio 与云端建立反向隧道，实现零入站端口——被管主机无需开放 22/80/443，所有管理流量由 edge 主动外联云端 40012 端口发起。浏览器 SSH、远程执行、抓包等功能均基于此隧道。