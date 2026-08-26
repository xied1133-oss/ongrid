---
kind: dependency_management
name: Go + npm 双语言依赖管理（go.mod/go.sum、npm lockfile、Makefile 统一编排）
category: dependency_management
scope:
    - '**'
source_files:
    - go.mod
    - go.sum
    - web/package.json
    - web/package-lock.json
    - Makefile
    - api/buf.yaml
    - api/buf.gen.yaml
    - deploy/Dockerfile.ongrid
    - deploy/Dockerfile.ongrid-edge
    - deploy/Dockerfile.frontier
    - deploy/install/edge/install.sh
    - deploy/install/edge/upgrade.sh
    - scripts/publish-cnb-release-attachments.sh
    - scripts/ensure-cnb-release.sh
    - VERSION
---

## 1. 使用的系统与工具

- **Go 模块**：仓库根目录使用单一 Go module `github.com/ongridio/ongrid`，通过 `go.mod` 声明直接依赖、`go.sum` 锁定所有间接依赖版本。Go 版本固定为 `go 1.25.0`。
- **前端依赖**：Web 工程位于 `web/`，使用 `package.json` + `package-lock.json`（由 `npm ci` 在构建时解析），框架为 Vite + React + TypeScript。
- **构建编排**：所有 Go 与前端依赖的拉取、编译、打包均通过根 `Makefile` 暴露 target，禁止 CI/Dockerfile/README 直接使用裸 `go build` / `docker build`（见 Makefile 首行注释）。
- **Proto 生成**：API 契约位于 `api/`，优先使用 `buf generate`（`api/buf.yaml`、`api/buf.gen.yaml`），回退到 `protoc` + `protoc-gen-go/grpc`。
- **数据库迁移**：使用 `migrate` CLI（`make migrate-up/migrate-down`）对 `db/migrations/*.sql` 进行版本化迁移。
- **外部二进制依赖**：OpenTelemetry Collector、node_exporter、process-exporter、mysqld/postgres/redis/mongodb exporter 等通过 Makefile 中的 `fetch-*` target 从 GitHub Release 下载并校验 checksum 后缓存至 `bin/<os>-<arch>/`。
- **镜像与 Helm Chart**：Docker 镜像推送到 CNB 私有镜像仓库（`docker.cnb.cool/ongridio/*`），Helm Chart 推送到 OCI 仓库 `helm.cnb.cool/ongridio`。

## 2. 关键文件

| 文件 | 作用 |
|---|---|
| `go.mod` / `go.sum` | Go 模块声明与依赖锁定 |
| `web/package.json` / `web/package-lock.json` | 前端依赖声明与锁定 |
| `Makefile` | 统一构建/测试/发布入口，集中定义所有第三方依赖拉取策略 |
| `api/buf.yaml` / `api/buf.gen.yaml` | Protocol Buffers 生成配置 |
| `deploy/Dockerfile.ongrid` / `deploy/Dockerfile.ongrid-edge` / `deploy/Dockerfile.frontier` | 容器镜像中设置 GOPROXY |
| `deploy/install/edge/install.sh` / `upgrade.sh` | 安装/升级脚本，按不可变 tag 从 CNB Release 拉取 Edge 二进制与公共依赖 |
| `scripts/publish-cnb-release-attachments.sh` / `ensure-cnb-release.sh` | 将 Edge 附件上传到 CNB Release |
| `VERSION` | 项目版本号，驱动 Edge Release 标签 |

## 3. 架构与约定

### Go 依赖
- 单一模块聚合云端 `cmd/ongrid` 与边端 `cmd/ongrid-edge`，所有业务代码位于 `internal/`，无 vendor 目录，依赖通过 `go mod tidy` 管理。
- 依赖版本全部显式写入 `go.mod` require 段，包括大量 indirect 依赖；未使用 `replace` 指令指向本地或私有模块。
- 私有代理仅在构建阶段生效：`deploy/Dockerfile.frontier` 设置 `GOPROXY=https://goproxy.cn,https://goproxy.io,https://proxy.golang.org,direct`，`deploy/Dockerfile.ongrid-edge` 设置 `ARG GOPROXY=https://proxy.golang.org,direct`，用于在镜像构建时加速 Go 模块下载。
- 没有发现 `.golangci.yml` 或 `go.work`，lint 通过 `make lint` 调用 `golangci-lint run`。

### 前端依赖
- `web/package.json` 使用语义化版本范围（如 `^18.3.1`），实际锁定版本由 `package-lock.json` 保证；构建脚本使用 `npm ci` 确保可重复安装。
- 构建产物 `web/dist/` 被嵌入到 `deploy/Dockerfile.web` 生成的 nginx 镜像中。

### 外部二进制依赖（Edge 运行时）
- OpenTelemetry Collector contrib bundle、各类 exporter 的版本以 Makefile 变量形式集中声明（`OTELCOL_VERSION`、`NODE_EXPORTER_VERSION`、`PROCESS_EXPORTER_VERSION`、`MYSQLD_EXPORTER_VERSION`、`POSTGRES_EXPORTER_VERSION`、`REDIS_EXPORTER_VERSION`、`MONGODB_EXPORTER_VERSION`）。
- 每个 `fetch-*` target 会先检查 `bin/<os>-<arch>/<binary>` 是否已存在，若存在则跳过下载，实现幂等缓存。
- 下载时使用 curl 带重试参数（`--retry 3 --retry-all-errors --retry-delay 3 --connect-timeout 15 --speed-time 60 --speed-limit 1024 --show-error`），并对 otelcol-contrib 使用上游发布的 checksums 文件做完整性校验。
- 这些二进制不随 Go 模块发布，而是作为 CNB Release 附件（`edge-deps-<target>.tar.xz`）单独发布，安装时由 `install.sh` / `upgrade.sh` 根据 `EDGE_DEPS_TAG` 拉取。

### 发布与版本策略
- 公共 Edge 依赖使用不可变 tag `EDGE_DEPS_TAG`（由 OTELCOL/NODE_EXPORTER/PROCESS_EXPORTER/MYSQLD_EXPORTER/POSTGRES_EXPORTER/REDIS_EXPORTER/MONGODB_EXPORTER 版本拼接而成），一旦发布不再修改。
- ongrid-edge 二进制使用项目 `VERSION` 作为 Release 标签，每次发布前通过 `make verify-edge-version-release` 校验 CNB Release 是否已完整。
- Docker 镜像通过 `make docker-push-cloud-images` / `docker-push-k8s-edge` 推送到 CNB 镜像仓库，并使用 buildx 多平台 manifest 同时产出 linux/amd64 与 linux/arm64。
- Helm Chart 通过 `make publish-k8s-chart` 打包并推送到 `oci://helm.cnb.cool/ongridio`。

## 4. 约定与约束

- **唯一构建入口**：Makefile 首行注释明确要求“所有 CI / Dockerfile / README 都应只调 make target，禁裸 go build / docker build”，这是强制约束。
- **Go 依赖必须经 go.mod/go.sum 管理**：仓库无 vendor 目录，所有 Go 依赖通过 `go mod` 管理，间接依赖也需保持 `go.sum` 同步。
- **外部二进制依赖必须通过 Makefile fetch-* target 获取**：所有非 Go 依赖（otelcol、exporter 等）都通过 Makefile 目标下载并校验，禁止在 Dockerfile 中硬编码 wget/curl 命令绕过校验。
- **Edge 安装器仅信任 CNB Release 附件**：`install.sh` / `upgrade.sh` 在安装/升级时会先校验 CNB Release 附件的 sha256，再替换 `/usr/local/lib/ongrid-edge/` 下的二进制，拒绝任意路径注入。
- **镜像仓库私有化**：所有项目自身镜像推送到 `docker.cnb.cool/ongridio/*`，Helm Chart 推送到 `helm.cnb.cool/ongridio`，通过环境变量 `CNB_API_ENDPOINT`、`CNB_HELM_USERNAME` 控制认证。
- **Proto 生成优先 buf**：`make proto` 优先尝试 `buf generate`，失败才回退到 protoc，因此新增 API 应优先维护 `api/buf.yaml`。
- **数据库迁移必须成对提供 up/down**：`db/migrations/` 下每个变更都有对应的 `.up.sql` 和 `.down.sql`，命名格式为 `YYYYMMDDHHMMSS_<描述>.sql`。
- **GOPROXY 仅在构建期生效**：生产运行环境不依赖 GOPROXY，Go 模块已在镜像构建阶段下载到缓存层；只有 `Dockerfile.frontier` 和 `Dockerfile.ongrid-edge` 设置了 GOPROXY 环境变量。
- **前端构建使用 npm ci**：`make build-web` 执行 `cd web && npm ci && npm run build`，要求 `package-lock.json` 与 `package.json` 严格一致以保证可重复构建。