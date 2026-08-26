---
kind: external_dependency
name: 关系型数据库：MySQL（GORM）
slug: mysql
category: external_dependency
category_hints:
    - vendor_identity
scope:
    - '**'
source_files:
    - go.mod
    - db/migrations/README.md
---

后端使用 GORM 连接 MySQL（driver `gorm.io/driver/mysql`），schema 由 gorm AutoMigrate 在 ongrid 启动时自动处理，迁移脚本位于 `db/migrations/`。生产环境推荐将 `mysql/` 数据卷放在独立 SSD/NVMe 上，并通过 `mysqldump --single-transaction` 做热备。SQLite 仅用于开发/测试（`glebarez/sqlite`）。