---
kind: external_dependency
name: 向量数据库：Qdrant（嵌入/RAG）
slug: qdrant
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - deploy/docker-compose.yml
---

Qdrant 作为 Compose 服务随栈启动，数据卷 bind-mount 到 `/var/lib/ongrid/qdrant`，用于 fastembed-go 生成的文本嵌入存储，支撑知识库与代码 RAG 检索。内部通过 `internal/pkg/qdrantx` 封装访问。