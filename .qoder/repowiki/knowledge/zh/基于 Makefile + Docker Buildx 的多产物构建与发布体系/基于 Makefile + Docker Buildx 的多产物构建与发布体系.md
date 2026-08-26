---
kind: build_system
name: 基于 Makefile + Docker Buildx 的多产物构建与发布体系
category: build_system
scope:
    - '**'
source_files:
    - Makefile
    - deploy/Dockerfile.ongrid
    - deploy/Dockerfile.ongrid-edge
    - deploy/Dockerfile.web
    - scripts/publish-release-image.sh
    - scripts/publish-release-image-platform.sh
    - scripts/merge-release-image-manifest.sh
    - scripts/ensure-cnb-release.sh
    - scripts/publish-cnb-release-attachments.sh
    - scripts/verify-release-images.sh
    - scripts/test-release-package.sh
    - dist/package.sh
    - dist/build-edge-bundle.sh
    - dist/package-k8s-chart.sh
    - db/migrations/20260729070000_add_k8s_workload_rollout_metadata.up.sql
    - api/buf.yaml
    - api/buf.gen.yaml
    - deploy/docker-compose.yml
    - deploy/kubernetes/ongrid-edge/Chart.yaml
---

## 1. 构建系统总览

仓库采用 **单一 Makefile（根目录 `Makefile`）** 作为所有构建、测试、打包、发布的唯一入口，并在文件首行以注释强制约束："所有 CI / Dockerfile / README 都应只调 make target，禁裸 go build / docker build"。该 Makefile 统一编排 Go 云端服务 (`cmd/ongrid`)、边缘 Agent (`cmd/ongrid-edge`)、Web 前端 SPA (`web/`)、数据库迁移 (`db/migrations/`)、Helm Chart (`deploy/kubernetes/ongrid-edge/`) 以及 CNB Release 附件的构建与发布。

版本来源为根级 `VERSION` 文件，若不存在则回退到 `git describe --tags --always --dirty`，最终通过 `-ldflags "-X main.version=$(VERSION)"` 注入二进制。所有镜像标签均基于该 VERSION。

## 2. 核心工件与构建目标

| 工件 | 构建方式 | 关键目标 |
|---|---|---|
| `ongrid` 云端管理器 | `go build -trimpath -ldflags ... ./cmd/ongrid` | `build-ongrid`、`docker-ongrid`、`docker-build`、`docker-push-cloud-manager` |
| `ongrid-edge` 边缘 Agent | `CGO_ENABLED=0 go build ... ./cmd/ongrid-edge` | `build-ongrid-edge`、`build-edge-linux-amd64|arm64|darwin-*`、`docker-push-k8s-edge` |
| Web SPA | `cd web && npm ci && npm run build` | `build-web`、`docker-build-web`、`docker-push-cloud-web` |
| 多架构镜像 | `docker buildx build --platform linux/amd64,linux/arm64` | `docker-push-cloud-images`、`docker-push-release-images` |
| Helm Chart | `bash dist/package-k8s-chart.sh` | `package-k8s-chart`、`publish-k8s-chart` |
| Edge 依赖附件 (otelcol/node_exporter/process_exporter/db exporters) | 从上游 release 下载并校验 checksum | `fetch-otelcol`、`fetch-node-exporter`、`fetch-db-exporters`、`build-edge-deps-attachments` |
| 安装包 tarball | `dist/package.sh` 生成 `ongrid-$(VERSION)-$(TARGET_OS)-$(TARGET_ARCH).tar.xz` | `package`、`package-all` |

## 3. 容器镜像架构

- **`deploy/Dockerfile.ongrid`**：两阶段构建。Builder 使用 `golang:1.25-bookworm`（启用 CGO 以支持 fastembed-go 的 onnxruntime_go），Runtime 使用 `debian:bookworm-slim`，内嵌 `libonnxruntime.so`、Python 解释器（供 cloud_bash sandbox）、skills/agents 资源包，并以非 root 用户 `nonroot` (uid/gid 65532) 运行，暴露 8080 (HTTP API) 和 9100 (/metrics)。
- **`deploy/Dockerfile.ongrid-edge`**：五阶段构建。Stage 1 用 `golang:1.25-alpine` 编译纯 Go 的 ongrid-edge；Stage 2-4 分别拉取 node_exporter、process_exporter、otelcol-contrib 并校验 SHA256；Stage 5 使用 `gcr.io/distroless/base-debian12:nonroot` 仅包含二进制与内置插件，暴露本地 9101 /metrics。
- **`deploy/Dockerfile.web`**：两阶段构建。Builder 用 `node:20-alpine` 执行 `npm ci && npm run build`，Runtime 用 `nginx:1.27-alpine` 静态托管 `web/dist/`，nginx.conf 与 TLS 证书通过 docker-compose bind-mount 覆盖。

## 4. 交叉编译与多架构发布

- 默认本地构建走 `linux/amd64`；release 通过 `CLOUD_IMAGE_PLATFORMS ?= linux/amd64,linux/arm64` 并行构建多架构镜像。
- 发布流程拆分为三阶段：`docker-push-cloud-manager-platform` 按平台单独 push digest → `merge-release-image-manifest.sh` 将 amd64/arm64 digest 聚合为不可变版本标签 → `verify-release-images.sh` 校验 manifest 完整性。
- Edge 二进制通过 `build-edge-linux-{amd64,arm64,darwin-amd64,darwin-arm64}` 四目标交叉编译，并通过 `EDGE_PLUGIN_ARCHES` 控制下游 fetch 目标。
- 公共依赖（otelcol、node_exporter、process_exporter、mysqld/postgres/redis/mongodb exporter）通过 `EDGE_DEPS_TAG` 生成不可变 tag（形如 `edge-deps-layout2-o0.157.0-n1.8.2-pr0.8.4-my0.19.0-pg0.19.1-r1.86.0-m0.51.0`），一次性发布到 CNB Release，安装时由 `install.sh`/`upgrade.sh` 直链下载并校验。

## 5. 测试与质量门禁

- 单元测试：`make test`（`go test ./...`）、`make test-race`（加 `-race`）。
- 集成测试：`make test-integration`（`-tags=integration`）。
- E2E：`make test-e2e`（默认 fakes，无外部凭证）、`make test-e2e-live`（设置 `E2E_LIVE_ALL=1` 打通真实 LLM/Slack/Prom 等）。
- 架构 lint：`make arch-lint` 调用 `go-arch-lint check` 校验 BC 边界。
- 发布前验证：`test-release-package` 串联 `test-public-url.sh`、`test-upgrade-data-permissions.sh`、`test-install-asset-modes.sh`、`test-compose-release-package.sh`、`test-apply-pending-upgrade.sh` 等脚本。

## 6. 数据库迁移

通过 `migrate` CLI 管理 MySQL schema，迁移文件位于 `db/migrations/`，命名约定为 `<timestamp>_<description>.up.sql` + `.down.sql`。Makefile 提供 `migrate-up`/`migrate-down` 目标，DSN 通过 `DB_DSN` 环境变量覆盖。

## 7. 关键约束与约定

- **禁止裸命令**：Makefile 头注释明确要求 CI/Dockerfile/README 只能通过 `make target` 触发构建，不得直接调用 `go build` 或 `docker build`。
- **版本号单一来源**：VERSION 文件优先，否则 git tag，再回退 `v0.0.0-dev`；所有镜像、Helm chart、CNB Release 均基于此值。
- **Edge 二进制不打包进 Compose 安装包**：`package` 目标刻意排除 `build-linux` 与 `build-web`，Compose 安装时从 CNB Release 直链下载 ongrid-edge 与依赖，避免安装包膨胀。
- **安全基线**：所有镜像均以非 root 用户运行；第三方二进制下载后强制校验 SHA256；onnxruntime 固定 ABI 版本。
- **缓存策略**：Dockerfile 使用 `--mount=type=cache` 挂载 `/go/pkg/mod`、`/root/.cache/go-build`、`/root/.npm`、`/app/node_modules/.vite` 实现增量构建。
- **Frontier 代理**：通过 `FRONTIER_VERSION` 固定上游 broker 版本，本地可配置 `FRONTIER_SRC` 从源码重建。

## 8. 关键文件

- `Makefile` — 全部构建/测试/发布入口
- `deploy/Dockerfile.ongrid` — 云端管理器镜像
- `deploy/Dockerfile.ongrid-edge` — 边缘 Agent 镜像
- `deploy/Dockerfile.web` — Web 前端 Nginx 镜像
- `scripts/publish-release-image.sh` / `publish-release-image-platform.sh` / `merge-release-image-manifest.sh` — 多架构镜像发布流水线
- `scripts/ensure-cnb-release.sh` / `publish-cnb-release-attachments.sh` — CNB Release 附件上传
- `scripts/test-*-*.sh` — 各阶段发布/安装/兼容性测试
- `dist/package.sh` / `dist/build-edge-bundle.sh` / `dist/package-k8s-chart.sh` — 安装包/Helm/Edge Bundle 打包脚本
- `db/migrations/*.sql` — 版本化数据库迁移
- `api/buf.yaml` / `api/buf.gen.yaml` — Proto 生成配置（buf 优先，回退 protoc）
- `deploy/docker-compose.yml` — 本地开发编排
- `deploy/kubernetes/ongrid-edge/Chart.yaml` — Helm Chart 定义