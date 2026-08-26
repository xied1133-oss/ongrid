# PR 330 日志后端本地测试计划

## 1. 目标与范围

验证 PR 330 在当前本地测试环境中的完整行为：OTel 日志采集、Loki/Elasticsearch
互斥选择、设备连接检查、结构化日志查询、游标释放、日志告警迁移、
`query_logql` 双后端行为，以及安全、性能和回归边界。

不把 CI 通过等同于运行时验收；每个结论必须对应自动化测试、API 返回、后端数据、
进程/网络状态或页面行为中的至少一种直接证据。

## 2. 测试环境

- 代码：`feature/logs-elasticsearch-otel`，HEAD `a1727fbc` + 本轮本地修复
- 运行制品：Manager/Host Edge/Kubernetes Edge `v0.13.3-pr330-fix1`
- Manager/Web：macOS 原生进程，HTTPS 入口 `https://localhost:8443`
- Edge：OrbStack kubeadm 集群 `ongrid-k8s-vm`，2 个 Ready 节点
- 当前日志后端：Elasticsearch 8.16.3，backend id 6，generation 5
- 可切换后端：内置 Loki 3.4.0
- 在线 Edge：5；其中 4 个启用了 Host 日志采集并进入连接检查分母；另有 1 个历史离线 Edge

## 3. 进入与退出条件

进入条件：

- PR CI 全绿；本地 Manager、Frontier、HTTPS 代理、ES、Loki 正常。
- kubeadm 节点 Ready，当前运行的 ongrid-system Pod Ready。
- 已记录测试前当前后端、backend id、generation 和在线设备数。

退出条件：

- P0 用例全部通过；P1 无阻塞缺陷。
- 测试结束恢复到测试前的 Elasticsearch 选中状态。
- 不泄露 API Key，不删除日志或业务数据，不清理历史 Pod。

## 4. 测试矩阵

| ID | 优先级 | 场景 | 操作与关键断言 | 证据 |
|---|---|---|---|---|
| A01 | P0 | Go 回归 | `make test-race`；日志服务、查询适配器、告警迁移无竞态 | 命令结果 |
| A02 | P0 | Web 回归 | Logs/Integrations API 与页面测试、typecheck、build 全通过 | 命令结果 |
| A03 | P1 | 发布链路 | chart、release workflow/package/publish 检查通过 | CI/命令结果 |
| C01 | P0 | 读取 ES 配置 | 当前后端为 ES；读取 API 不返回 API Key 明文 | API 返回 |
| C02 | P0 | ES 连接测试 | 测试成功且不改变 selected/generation | 前后 API 快照 |
| C03 | P0 | 非法 ES 配置 | 不安全索引、共享 Key 未显式允许、无效 endpoint 被拒绝 | Go 测试 |
| C04 | P0 | Loki 直接选择 | 选择成功，不触发设备检查，不产生中间状态 | API 返回/DB 状态 |
| C05 | P0 | ES 直接选择 | Manager 探测成功后直接选中，不等待离线 Edge | API 返回/耗时 |
| C06 | P0 | 互斥性 | 任一时刻只有 Loki 或 ES 一个当前后端 | API/Edge 配置 |
| C07 | P1 | 保存不切换 | 保存 ES 配置不改变当前后端 | Go 测试 |
| D01 | P0 | ES 设备检查 | 进度从 0 开始；最终 verified/online，离线设备不进分母 | 轮询 API |
| D02 | P0 | Loki 设备检查 | 同 D01，探针仅出现在 Loki | 轮询 API/查询 |
| D03 | P1 | 检查独立性 | 检查失败/重试不改变 selected；重试生成新 generation/probe | Go 测试 |
| P01 | P0 | ES 实时采集 | Host journald/file 与 K8s CRI 日志能在 ES 查询 | API/ES 记录 |
| P02 | P0 | Loki 实时采集 | 切换后新探针和正常日志能在 Loki 查询 | API/Loki 记录 |
| P03 | P0 | 单 exporter | Edge 当前配置只包含选中后端 exporter，不双写 | Edge 配置/两端查询 |
| P04 | P0 | 元数据 | device/cluster/namespace/pod/container/node/source/level 映射正确 | 搜索记录 |
| P05 | P1 | 凭证边界 | 写 Key 不进入普通插件快照、argv、日志/API；Manager 不接收 ES 正文 | 自动化/进程/网络 |
| Q01 | P0 | 关键字 | 包含任一、全部、短语、排除均返回符合条件的记录 | `/logs/search` |
| Q02 | P0 | Scope/字段 | device、cluster、namespace、pod、container、source、level、file/unit 筛选 | `/logs/search` |
| Q03 | P0 | 字段目录 | API 不出现 backend、attributes_*、severity_*、extracted 噪声字段；页面额外隐藏 service_name | `/logs/fields`/Web 测试 |
| Q04 | P0 | 直方图 | 1m bucket 与搜索时间范围一致，返回计数 | `/logs/histogram` |
| Q05 | P0 | 上下文 | 给定记录时间可取得前后相邻日志 | `/logs/context` |
| Q06 | P0 | 分页/PIT | ES 首屏返回 cursor，续页无重复；主动 close 成功 | search/close API |
| Q07 | P0 | 跨切换 cursor | ES cursor 在切到 Loki 后仍关闭原 ES PIT；不能用于错误后端续页 | Go 测试/API |
| Q08 | P1 | 刷新降级 | histogram 失败不清空成功日志；search 失败显示错误 | Web 测试 |
| Q09 | P1 | 导出与限制 | limit、窗口、字段/index allowlist 生效，不接受原始 ES DSL | API/Go 测试 |
| T01 | P0 | `query_logql`/ES | 安全 selector + line filter 返回 ES `records`，不伪装 Loki | 集成测试/会话 |
| T02 | P0 | `query_logql`/Loki | 原生 LogQL 原样执行并返回 `resultType/result` | 集成测试/会话 |
| T03 | P0 | 不安全语法 | ES 拒绝 metric/不可移植 LogQL 并返回明确错误 | 集成测试 |
| R01 | P0 | 旧告警迁移 | 选择 ES 前旧规则原子迁移到 `log_search`，保留 `group_by` | Go 测试/DB |
| R02 | P0 | Host 告警语义 | host scope 强制 `device_id` exists 与 group_by | Go 测试 |
| R03 | P0 | 迁移冲突 | 不安全旧 LogQL 阻止 ES 选择且当前后端不变 | Go 测试 |
| R04 | P0 | Loki 告警 | 选择 Loki 不执行迁移；`log_search` 仍正常评估 | Go 测试/运行时 |
| F01 | P0 | ES 不可用 | 查询/选择返回错误，不自动切换 Loki | Go 测试/故障注入 |
| F02 | P0 | Loki 不可用 | 查询返回错误，不改变选择状态 | Go 测试/故障注入 |
| F03 | P1 | 重启恢复 | Manager 重启保留后端；Edge/Collector 重启继续 checkpoint | API/日志 |
| S01 | P0 | 密钥保密 | DB、API、进程参数和日志中无 encoded API Key 明文 | 受控扫描 |
| S02 | P0 | 查询边界 | 只能访问 `logs-ongrid.*.otel-*`，响应/时间/limit 有硬上限 | Go 测试 |
| PERF01 | P0 | 查询性能 | 15 分钟 search 与 histogram 各执行 10 次，P95 < 3s | 采样结果 |
| PERF02 | P0 | 可见延迟 | 设备探针写入到可查询 P95 < 10s | 连接检查时间 |
| LONG01 | P0 | 断网队列 | 断网 30 分钟后续传，队列有界且有告警 | 专项人工压测 |
| NET01 | P0 | ES 直写抓包 | Edge→ES 有正文流量，Manager 8080/Frontier 无日志正文 | 专项抓包 |
| UI01 | P1 | 页面交互 | 搜索、字段、分页、切换、连接检查进度可用 | 人工页面验收 |
| UI02 | P1 | 时间钻取 | 柱体点击、拖拽、自定义时间、范围回退反馈正确 | 人工页面验收 |
| UI03 | P1 | 视觉与可访问性 | 中英文、深浅主题、键盘时间输入无回归 | 人工页面验收 |

## 5. 2026-08-24 本地执行结果

### 5.1 第一轮结论

**第一轮未达到退出条件。** Loki 与 Elasticsearch 的选择、设备验证、采集、结构化查询、
游标、故障恢复和性能主路径通过，但发现 2 个 P0 功能缺陷：

1. Elasticsearch 选中时，`log_search` 告警试算会因亚毫秒时间对齐失败返回 500；
   同一路径也影响实际告警评估。
2. Host journald 的 `ongrid_source` 实际成为 `attributes_ongrid_source`，结构化查询清理
   transport 字段后没有提升为 `source_id`，导致 Host 日志的来源展示和筛选缺失。

测试结束时已恢复 Elasticsearch backend id 6、generation 5；Manager、HTTPS 代理、
5 个在线 Edge、2 个 kubeadm 节点和 7 个运行中 `ongrid-system` Pod 均健康。

### 5.2 通过项与证据

| 范围 | 结果 | 直接证据 |
|---|---|---|
| 自动化与构建 | 通过 | `make test-race`；27 个 Web 定向用例；typecheck、ESLint、Go vet、Manager/Web build；PR 330 五项 CI 全绿 |
| 后端选择 | 通过 | Loki 直接选择；ES 重新选择耗时 284ms；离线历史 Edge 未阻塞；任一时刻仅一个当前后端 |
| ES 连接测试 | 通过 | ES 8.16.3；测试前后 backend/generation 不变；API 无 API Key 字段 |
| ES 设备检查 | 通过 | `0/4 -> 1/4 -> 2/4 -> 4/4`，failed=0 |
| Loki 设备检查 | 通过 | `0/4 -> 1/4 -> 2/4 -> 3/4 -> 4/4`，failed=0 |
| ES 采集 | 通过 | 最近 200 条覆盖 device 122/123/650/690；K8s 与 Host 日志持续写入 |
| Loki 采集 | 通过 | 结构化查询返回 Loki records；连接探针与正常日志可查 |
| 结构化搜索 | 通过 | any/all/phrase/exclude、device scope、level values、上下文、60 个 ES buckets、10 个 Loki buckets 均符合断言 |
| 字段边界 | 通过 | extracted/attributes/resource_attributes/severity 噪声字段不在目录；非法 `service_name_extracted` 返回 400 `LOG_QUERY_INVALID` |
| ES 分页/PIT | 通过 | limit=1 续页记录不同；主动 close=200；切到 Loki 后旧 cursor 续页=400、close=200 |
| 原生 Loki 查询 | 通过 | `{device_id=~".+"} |= "reconcile snapshot"` 返回 streams，20 条日志 |
| `query_logql` | 自动化通过 | ES portable query、Loki native query、不可移植语法拒绝、响应字段裁剪定向测试通过 |
| 告警迁移 | 自动化通过 | legacy -> `log_search`、Host device constraint/group_by、冲突回滚定向测试通过 |
| Loki `log_search` 试算 | 通过 | HTTP 200，5 个 points、2 次 firing |
| ES 短时不可用 | 通过 | search=502 `LOG_BACKEND_ERROR`；仍选中 ES、无 Loki fallback；约 20s 恢复后 search=200 |
| 重启恢复 | 通过 | Manager 重启后仍为 ES backend id 6、generation 5 |
| 密钥边界 | 通过 | 进程 argv 无 Key；API 不返回 Key；4 个相关 vault blob 均为 139 字节密文，无 JSON/API-Key 明文特征 |
| 性能 | 通过 | ES search P95 152.093ms、histogram P95 84.782ms；Loki search P95 94.750ms、histogram P95 45.454ms，均远低于 3s |

### 5.3 第一轮失败项

| 缺陷 | 关联用例 | 复现结果 | 根因位置 |
|---|---|---|---|
| ES 告警 histogram 亚毫秒错位 | Q04/告警运行时 | `/alert-rules/preview` HTTP 500：ES bucket `...28.931` 无法对齐 request start `...28.931323` | `internal/pkg/logquery/elasticsearch.go` 未处理 ES `date_histogram` 只有毫秒精度的边界 |
| journald source 丢失 | P04/Q02 | 原生 Loki 标签为 `attributes_ongrid_source=journald`；结构化 Loki/ES Host record 的 `source_id` 为空 | `internal/edgeagent/plugins/logs/render.go` 把 Stanza resource 写成了嵌套 `attributes` 字段 |

### 5.4 未执行或仅自动化覆盖

- LONG01：未执行 30 分钟断网队列压测；本轮只执行了约 20 秒 ES 停机恢复。
- NET01：未抓包，不能把“Manager 不接收 ES 正文”标记为运行时通过。
- F02：Loki 不可用仅有自动化覆盖，未停止共享 Loki 容器。
- PERF02：未单独采集“产生日志到可见”的 10 轮时间戳样本；连接检查总耗时不等同于单条日志可见延迟。
- UI01/UI02/UI03：交互逻辑有 Web 自动化覆盖；未执行浏览器人工视觉、中英文、深浅主题和键盘验收。
- 自定义 filelog source：当前环境没有配置独立应用文件源，只验证了 journald 与 K8s CRI。

### 5.5 修复后第二轮结果

**本轮发现的 2 个 P0 缺陷均已关闭，达到本次问题闭环的退出条件。** 未扩大范围执行
5.4 中已标记的专项压测、抓包和人工 UI 项目。

| 范围 | 结果 | 直接证据 |
|---|---|---|
| 根因修复 | 通过 | ES 查询起点先按后端毫秒精度下推，返回 bucket 再映射到产品精确时间网格；journald Stanza 字段改为 `resource["device_id"]` / `resource["ongrid_source"]` |
| 自动化 | 通过 | 两个定向回归测试通过；受影响 5 个包 `go test -race` 通过；全量 `make test-race`、`go vet ./...`、`git diff --check` 通过；本机未安装 `golangci-lint`，未伪报为通过 |
| 部署 | 通过 | Manager、两台 Host Edge、K8s controller/scraper/gateway/node 主容器及 node initContainer 均运行 `v0.13.3-pr330-fix1`；当前副本 Ready，Host systemd active |
| ES journald 来源 | 通过 | `device_ids=[650,690]` + `source_ids=["journald"]` 返回 20 条，records 的 `resource_attributes.ongrid_source` 均为 `journald` |
| ES 纳秒直方图 | 通过 | 请求起点后缀 `.123456789Z`；HTTP 200，45 个 bucket，首桶精确保持 `.123456789Z` |
| ES `log_search` 试算 | 通过 | 原复现请求从 HTTP 500 恢复为 HTTP 200；12 个 series points、12 次 firing，无 error/skipped_reason |
| Loki 应用与设备检查 | 通过 | 应用 HTTP 200、26.861ms；连接检查 `0/4 -> 1/4 -> 2/4 -> 4/4`，failed/offline=0 |
| Loki 查询与告警 | 通过 | journald source 过滤返回 20 条；纳秒起点直方图 HTTP 200、20 buckets；`log_search` 试算 HTTP 200、12 points |
| ES 回切 | 通过 | 应用 HTTP 200、99.092ms；连接检查 `0/4 -> 1/4 -> 2/4 -> 4/4`；最终 ES 试算 HTTP 200 |
| 最终状态 | 通过 | Elasticsearch backend id 6、generation 5、version 8.16.3 选中；2 个 kubeadm 节点 Ready；4 个启用日志的在线 Edge 全部验证通过 |

## 6. 执行顺序

1. 记录 ES 基线并完成自动化回归。
2. 在 ES 下验证配置、连接检查、采集、查询、分页、字段、性能和工具。
3. 选择 Loki，验证互斥、设备检查、采集、查询、工具和告警。
4. 重新选择 ES，确认恢复、设备收敛和旧 cursor 释放语义。
5. 执行受控故障注入；需要 30 分钟断网或抓包的专项用例单独标记，避免影响现有会话。
6. 恢复 ES 为当前后端，复核健康、设备在线数和工作区状态。

## 7. 结果判定

- **通过**：直接证据覆盖关键断言。
- **失败**：结果与断言冲突，记录请求、时间、后端和影响范围。
- **未执行**：缺少所需时长、权限或会影响现有数据；不得写成通过。
- **自动化覆盖**：只证明代码路径，不代替真实网络、进程、页面或性能验收。
