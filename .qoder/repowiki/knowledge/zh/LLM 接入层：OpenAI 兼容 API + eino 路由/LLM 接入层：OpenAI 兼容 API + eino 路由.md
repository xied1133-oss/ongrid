---
kind: external_dependency
name: LLM 接入层：OpenAI 兼容 API + eino 路由
slug: openai-compatible-models
category: external_dependency
category_hints:
    - vendor_identity
    - auth_protocol
scope:
    - '**'
source_files:
    - go.mod
    - deploy/install/README.md
---

AI 能力通过 go-openai 调用 OpenAI 兼容接口，并由 eino 框架做多模型热路由，支持 Anthropic、OpenAI、GLM、DeepSeek、Gemini、Kimi 等。配置项 `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL` 写入 `.env`，留空时 AI Chat 接口返回 500。国内模型走兼容网关时可设置 `BASE_URL` 指向自定义 endpoint。