---
kind: error_handling
name: 错误处理体系：sentinel errors + HTTPStatus 映射 + 中间件审计/指标 + 边界 panic/recover 防护
category: error_handling
scope:
    - '**'
source_files:
    - internal/pkg/errs/errs.go
    - internal/pkg/auth/middleware.go
    - internal/manager/server/middleware/audit.go
    - internal/manager/server/middleware/metrics.go
    - internal/manager/server/device/http.go
    - internal/manager/server/edge/http.go
    - internal/manager/server/k8s/http.go
    - internal/manager/server/metric/http.go
    - internal/manager/server/monitor/http.go
    - internal/manager/server/operatorrun/http.go
    - internal/manager/server/packetcapture/http.go
    - internal/manager/server/prometheus/http.go
    - internal/manager/server/report/dto.go
    - internal/manager/server/skill/http.go
    - internal/manager/server/topology/http.go
    - internal/manager/server/webshell/http.go
    - internal/manager/server/aiops/http.go
    - internal/manager/server/imbridge/http.go
    - internal/edgeagent/k8s/inventory.go
    - internal/edgeagent/plugins/custommetrics/plugin.go
    - internal/edgeagent/plugins/databasemetrics/plugin.go
    - internal/edgeagent/streamrouter/router.go
    - internal/manager/biz/flow/engine.go
    - internal/manager/biz/report/scheduler.go
    - internal/manager/biz/edge/upgrade_job.go
    - internal/manager/biz/logs/service.go
    - internal/pkg/tunnel/client.go
    - internal/skill/loader.go
---

## 1. 整体方案

该仓库采用 **Go sentinel error + 统一 HTTP 状态码映射** 的错误模型，配合 chi 路由的中间件链完成认证、鉴权、审计与指标采集。核心思想是：业务层只返回语义化的 `error`（sentinel），HTTP 层通过单一函数 `errs.HTTPStatus` 将 sentinel 映射为 HTTP 状态码；跨进程/插件调用使用 `recover` 做隔离，避免单个子进程崩溃拖垮主进程。

## 2. 关键文件与包

- `internal/pkg/errs/errs.go`：定义全局 sentinel errors（`ErrNotFound`、`ErrUnauthorized`、`ErrForbidden`、`ErrConflict`、`ErrInvalid`、`ErrTenantMismatch`、`ErrEdgeOffline`、`ErrBudgetExceeded`、`ErrNotWiredYet`、`ErrTooManyAttempts`）以及唯一的 HTTP 状态码映射函数 `HTTPStatus(err) int`。
- `internal/pkg/auth/middleware.go`：JWT 认证中间件，失败时直接写 401，不继续调用 next。
- `internal/manager/server/middleware/audit.go`：审计中间件，记录用户操作到 `audit_logs`，按响应状态桶分类（success / failure / denied）。
- `internal/manager/server/middleware/metrics.go`：Prometheus 指标中间件，记录请求数、耗时、状态码，未知路由归入 `route="unknown"` 控制基数。
- 各业务域 handler（如 `internal/manager/server/device/http.go`、`edge/http.go`、`k8s/http.go`、`metric/http.go`、`monitor/http.go`、`operatorrun/http.go`、`packetcapture/http.go`、`prometheus/http.go`、`report/dto.go`、`skill/http.go`、`topology/http.go`、`webshell/http.go`、`aiops/http.go`、`imbridge/http.go`）统一在出错分支调用 `errs.HTTPStatus(err)` 写出对应 HTTP 状态码。
- 边缘侧 panic/recover 集中点：`internal/edgeagent/k8s/inventory.go`、`internal/edgeagent/operator/handlers.go`、`internal/edgeagent/plugins/custommetrics/plugin.go`、`internal/edgeagent/plugins/databasemetrics/plugin.go`、`internal/edgeagent/streamrouter/router.go`。
- Manager 后台任务 recover 点：`internal/manager/biz/flow/engine.go`、`internal/manager/biz/report/scheduler.go`、`internal/manager/biz/edge/upgrade_job.go`、`internal/manager/biz/logs/service.go`、`internal/manager/biz/imbridge/provider/dingtalk/stream.go`、`internal/manager/biz/device/network_poll_scheduler.go`。
- 隧道回调 recover：`internal/pkg/tunnel/client.go`（每个回调用 defer/recover 包裹，防止处理器 panic 中断流）。
- Skill 注册 recover：`internal/skill/loader.go`，注册阶段 panic 被捕获用于校验失败场景。

## 3. 架构与约定

### 3.1 Sentinel error 分层
- **共享层**（`internal/pkg/errs`）：仅放跨领域通用的 sentinel，注释明确要求“Keep this set minimal; BC-specific errors belong in each BC's biz package”。
- **领域层**：每个 bounded context 在自己的 `biz` 包内定义专属 sentinel，例如 AIOPS 域的 `ErrMaxIterationsReached`、`ErrToolTimeout`、`ErrRateLimited`、`ErrHumanApprovalUnavailable`、`ErrReviewRejected`、`ErrReviewUndecided`、`ErrReviewerSpawn`，IMBridge Feishu 域的 `ErrBadSignature` 等。
- 业务函数通过 `errors.New` 或 `fmt.Errorf("%w", err)` 包装并向上返回，不直接构造 HTTP 响应。

### 3.2 HTTP 状态码映射
`errs.HTTPStatus` 是“唯一事实来源”（single source of truth），handler 中一律通过它取得状态码再写入响应体，禁止硬编码 `http.StatusXxx`。未匹配到的 error 默认返回 500。

### 3.3 中间件链顺序
Manager 的 chi 路由链典型顺序为：`AuditMiddleware` → `auth.Middleware` → 业务 handler → 指标/审计在 handler 结束后收集响应状态。审计中间件通过 `*auditSlot` 指针在上下文间传递，确保即使后续中间件调用 `r.WithContext` 也能看到最终租户信息。

### 3.4 Panic/Recover 策略
- **边缘侧插件/子进程**：每个外部插件执行、K8s inventory watch、stream router 回调都包裹 `if r := recover(); r != nil { ... }`，把 panic 视为可恢复故障，记录日志后继续运行。
- **后台调度器**：Flow engine、Report scheduler、Upgrade job、Logs service、DingTalk stream、Network poll scheduler 等 goroutine 入口均用 recover 保护，保证单条任务崩溃不影响其他任务。
- **隧道回调**：`internal/pkg/tunnel/client.go` 对每个消息回调单独 defer/recover，避免一个 handler panic 导致整个连接断开。
- **Skill 注册期**：`internal/skill/loader.go` 用 recover 捕获 `Register` 时的 panic，作为配置/校验失败的信号。

### 3.5 错误传播模式
- 业务层返回 `error`，上层通过 `errors.Is` 判断 sentinel（例如 `cmd/ongrid/main.go` 中用 `%w: LLM provider not configured` 包装 `errs.ErrNotWiredYet`）。
- 数据库层通过 `internal/pkg/dbx` 暴露 GORM 错误，由 usecase 层决定是否转换为领域 sentinel。

## 4. 约定与约束

| 约定 | 说明 | 证据位置 |
|---|---|---|
| 所有 HTTP handler 必须通过 `errs.HTTPStatus` 获取状态码 | 禁止在 handler 中硬编码状态码 | 多个 `server/*/http.go` 中统一调用 `errs.HTTPStatus(err)` |
| 共享 sentinel 仅放在 `internal/pkg/errs` | 领域错误应放在各自 `biz` 包 | `errs.go` 包注释 |
| 认证失败直接 401，不继续链 | auth middleware 失败即终止 | `internal/pkg/auth/middleware.go` |
| 审计仅记录显式标注的用户动作 | 未调用 `SetAuditEvent` 的请求不被审计 | `middleware/audit.go` 注释与实现 |
| 未知路由指标归入 `route="unknown"` | 控制 cardinality | `middleware/metrics.go` |
| 外部插件/子进程调用必须 recover | 防止子进程崩溃影响主进程 | `edgeagent/plugins/*`、`pkg/tunnel/client.go` |
| 后台 goroutine 入口必须 recover | 保证调度器鲁棒性 | `biz/flow/engine.go`、`biz/report/scheduler.go`、`biz/edge/upgrade_job.go` 等 |
| 错误包装使用 `%w` | 保留错误链以便 `errors.Is` 判断 | 多处 `fmt.Errorf("...: %w", err)` |

## 5. 缺失或不一致之处

- 目前未见统一的 gRPC 错误码映射（API 定义在 `api/` 下为 proto，但当前 HTTP 层为主）；若未来启用 gRPC，需补充 `status.Error` 映射规则。
- 并非所有 handler 都调用 `errs.HTTPStatus`，部分仍直接使用 `http.Error(w, ..., code)`（如 `iam/server/http.go` 第 436 行），存在混用风险。
- 没有全局的 panic 恢复中间件（仅在 goroutine 入口和插件边界做 recover），顶层 HTTP 请求无兜底 recover，依赖框架默认行为。

总体而言，该仓库的错误处理以 **sentinel error + 中央映射函数** 为核心，辅以 **中间件链** 完成认证/审计/指标，并在 **边缘侧与后台任务** 中广泛使用 **panic/recover** 做故障隔离，形成清晰的“业务返回 error → HTTP 层映射 → 中间件横切”的分层架构。