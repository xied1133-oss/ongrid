---
kind: external_dependency
name: 容器镜像与 Edge 制品分发：CNB 制品库
slug: cnb-cool
category: external_dependency
category_hints:
    - vendor_identity
    - client_constraint
scope:
    - '**'
source_files:
    - deploy/install/README.md
---

Ongrid 发布包不内嵌容器镜像和大型二进制，安装时从 CNB 制品库拉取：Manager/Web 镜像来自 `docker.cnb.cool/ongridio/ongrid`，Edge 安装制品来自 `https://cnb.cool/ongridio/ongrid-edge/-/releases/download/`。国内 ECS 默认走该源无障碍；若需私有化或代理，可通过 `ONGRID_EDGE_ARTIFACT_BASE_URL` 覆盖下载根地址。公共组件位于安装包锁定的不可变 `edge-deps-*` Release，只有组件版本或布局变化才重新上传。完全离线场景可构建时开启 `ONGRID_BUNDLE_EDGE_ASSETS=1 make package` 将 Edge 制品打包进发布包。