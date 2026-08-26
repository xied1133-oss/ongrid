---
kind: logging_system
name: 结构化日志与多后端日志查询系统（slog + logquery）
category: logging_system
scope:
    - '**'
source_files:
    - internal/pkg/logger/logger.go
    - cmd/ongrid/main.go
    - internal/pkg/logquery/search.go
    - internal/pkg/logquery/client.go
    - internal/pkg/logquery/loki_search.go
    - internal/pkg/logquery/elasticsearch.go
    - internal/pkg/logquery/runtime_client.go
    - internal/pkg/logquery/level.go
    - docs/design/HLD-001-log-pipeline-backend-abstraction.md
    - docs/rfc/RFC-003-otel-elasticsearch-logs.md
    - db/migrations/20260818160000_create_log_backends.up.sql
---

## 1. 使用的系统与框架

仓库包含两套相互独立但协作的日志子系统：

- **应用运行时日志**：基于 Go 标准库 `log/slog`，通过 `internal/pkg/logger` 统一初始化，输出 JSON 行到 `os.Stderr`。所有业务代码通过该包获取 `*slog.Logger`，禁止直接调用 `fmt.Println` / `log.Printf`。
- **采集日志查询系统**：`internal/pkg/logquery` 提供后端无关的日志检索 API，当前实现同时支持 **Loki**（`loki_search.go`）和 **Elasticsearch**（`elasticsearch.go`），并通过 `Searcher` 接口暴露统一的 Search/Count/Histogram/FieldValues 能力，被 Manager 的 Logs UI、告警评估器、AIOps 工具复用。

## 2. 关键文件与包

| 路径 | 职责 |
|---|---|
| `internal/pkg/logger/logger.go` | 封装 `slog.NewJSONHandler(os.Stderr)`，提供 `WithService(name)` 注入服务名 |
| `cmd/ongrid/main.go` | 启动时 `logger.WithService(logger.New(slog.LevelInfo), "ongrid")`，作为 manager 进程唯一日志入口 |
| `internal/pkg/logquery/search.go` | 定义 `Searcher` 接口、`SearchRequest`/`Record`/`Scope`/`FieldDefinition` 等跨后端共享模型，维护允许字段白名单 `LookupField` |
| `internal/pkg/logquery/client.go` | Loki HTTP 客户端，封装 `/loki/api/v1/query_range`、`/label/*`，默认超时 30s，强制 `X-Scope-OrgID: ongrid` |
| `internal/pkg/logquery/loki_search.go` | Loki 后端对 `Searcher` 的实现，含 LogQL 编译、游标分页、直方图、分组计数 |
| `internal/pkg/logquery/elasticsearch.go` | Elasticsearch 后端实现，使用 PIT（Point-in-Time）+ `search_after` 分页，索引模式限定为 `logs-ongrid.*.otel-*` |
| `internal/pkg/logquery/runtime_client.go` | 运行时可切换 Loki 端点（URL/BasicAuth/TLS）的 `RuntimeClient`，带连接缓存 |
| `internal/pkg/logquery/level.go` | 级别归一化：从 JSON `severity_text`/`level`、klog 前缀、文本正则推断级别，并映射 OTel `severity_number` |

## 3. 架构与设计约定

### 3.1 应用日志（slog）
- 所有日志必须为结构化键值对；注释明确禁止记录原始用户内容（聊天消息、请求体、密钥）。
- 通过 `slog.String("service", name)` 区分进程来源（manager vs edge）。
- trace_id / org_id 在调用点以属性形式注入，不在 logger 层自动附加。
- 日志级别由启动参数决定（`cmd/ongrid/main.go` 中 `slog.LevelInfo`）。

### 3.2 日志查询抽象（logquery）
- `Searcher` 是后端无关契约：`Search(ctx, req) (*SearchResult, error)`、`Count`、`Fields`、`FieldValues`、`Histogram`，可选 `GroupedCounter` 与 `CursorCloser`。
- 产品级字段通过 `LookupField` 白名单暴露（`device_id`、`cluster_id`、`namespace`、`workload`、`pod`、`container`、`node`、`service_name`、`source_id`、`level`、`file`、`unit`、`trace_id`、`span_id`、`message`），每个字段维护 `LokiName` 与 `ElasticsearchPath` 的双向映射。
- 查询限制集中定义：`DefaultSearchLimit=200`、`MaxSearchLimit=1000`、`MaxSearchWindow=30*24h`、`MaxCountGroups=10000`、`MaxKeywordCount=20`、`MaxFilterCount=20`、`MaxScopeValueCount=100`、`MaxKeywordLength=512`。
- 游标采用 base64 RawURLEncode 的 JSON 结构，Loki 用 `{backend,timestamp,skip}`，Elasticsearch 用 `{backend,pit_id,sort,direction}`，由 `encodeCursor`/`decodeCursor` 统一编解码。
- 级别检测：当后端未提供 `severity_text` 时，按顺序尝试 JSON 字段 → klog `[IWEF]nnnn` 前缀 → 文本正则 `(trace|debug|info|notice|warn|error|critical|fatal|panic)`，未知返回 `unknown`。

### 3.3 后端选择与路由
- Manager 侧通过 `biz/logs` 根据配置选择 Loki 或 Elasticsearch 实现；`runtime_client.go` 支持运行时切换 Loki 端点而不重启。
- Elasticsearch 要求版本 ≥ 8.16，API Key 必须存在且具备指定权限，通过 `ProbeInfo`/`RequirePrivileges` 校验。
- Loki 查询默认添加 `X-Scope-OrgID: ongrid` 头适配单租户部署。

## 4. 约定与约束

- **结构化日志强制**：`internal/pkg/logger` 注释声明 “structured logs only, JSON handler, never log raw user content”，这是项目红线。
- **禁止任意索引/DSL**：Elasticsearch 客户端只接受固定 index pattern `logs-ongrid.*.otel-*`，不接收调用方传入的索引名或原始 DSL；Loki 查询通过 `compileLogQL` 生成，同样不暴露原始 LogQL。
- **字段白名单**：所有过滤/聚合字段必须经 `LookupField` 注册，新增维度需在此处登记 Loki/Elasticsearch 映射。
- **时间窗口上限**：`NormalizeAndValidate` 拒绝超过 30 天的查询范围，防止长尾查询拖垮后端。
- **响应大小保护**：Loki 客户端限制响应体 8 MiB，Elasticsearch 客户端限制 16 MiB（搜索）/ 32 MiB（硬上限），避免 OOM。
- **级别标准化**：`normalizeLevel` 将 `i/info/information`→`info`、`w/warn/warning`→`warn`、`e/err/error`→`error`、`crit`→`critical`、`f/fatal`→`fatal`，保证跨后端一致。
- **游标安全**：Loki 游标校验 `cursor.Backend == lokiBackendName` 且 `skip ≤ 10000`；Elasticsearch 游标校验 `PITID` 非空且排序字段完整，否则返回 `errInvalidCursor`。
- **审计日志独立**：Manager 启动时还初始化独立的 audit 模块（append-only），与上述日志系统并行，用于“谁做了什么”的不可变审计轨迹。

## 5. 部署与集成

- 云端部署通过 `deploy/install/docker-compose.yml`、`deploy/kubernetes/` Helm Chart 编排 Grafana/Loki/Tempo/Prometheus，Grafana 数据源在 `deploy/grafana/provisioning/datasources/loki.yml` 中预配。
- 边缘侧通过 OTLP 协议将日志写入 Loki 或 Elasticsearch（见 `docs/design/HLD-001-log-pipeline-backend-abstraction.md` 与 `docs/rfc/RFC-003-otel-elasticsearch-logs.md`）。
- 数据库迁移 `db/migrations/20260818160000_create_log_backends.up.sql` 持久化日志后端配置，供 Manager 启动时动态选择。
