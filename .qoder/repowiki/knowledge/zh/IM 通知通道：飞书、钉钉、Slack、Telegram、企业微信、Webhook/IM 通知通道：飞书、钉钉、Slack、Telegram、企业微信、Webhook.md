---
kind: external_dependency
name: IM 通知通道：飞书、钉钉、Slack、Telegram、企业微信、Webhook
slug: feishu-dingtalk-channels
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - go.mod
---

通知子系统通过官方 SDK 对接飞书（`larksuite/oapi-sdk-go/v3`）、钉钉（`open-dingtalk/dingtalk-stream-sdk-go`），同时支持 Slack incoming webhook、Telegram bot、企业微信以及通用 JSON Webhook（支持 HMAC 签名）。各通道通过 `ONGRID_NOTIFY_*` 环境变量启用，默认关闭。