# PRD-005 WebSSH Edge 直连模式（Agent PTY）

- 状态：已实现
- 日期：2026-08-27
- 涉及端：manager（server/webshell、service/frontierbound）、edge agent（webshell）、前端（DeviceShell 页）

## 背景

WebSSH 原有唯一路径是「manager 侧 SSH 客户端 → tunnel 字节流 → edge 转发到本机
127.0.0.1:22」，连接弹窗强制输入 OS 用户名 / 密码。大量企业环境的主机只允许
堡垒机登录、不下发 OS 账号密码，导致 WebSSH 在这些主机上完全不可用——而
Edge Agent 本身就以 root 常驻在主机内，具备直接起交互终端的条件。

## 目标

新增「Edge Agent 直连」模式：edge 直接以进程身份（通常 root）拉起 PTY
shell，经既有隧道 RPC 双向传输，用户无需提供任何 OS 凭据。原 SSH 模式保留
为可选项，行为不变。

## 方案要点（复用既有通道，不新增协议族）

- manager→edge：复用 `shell_open / shell_input / shell_resize / shell_close`
  RPC（`ShellOpenRequest.Mode="agent"` 区分模式）
- edge→manager：复用 `shell_output / shell_exit` 推送，经
  `WebshellRouter.DispatchOutput/DispatchExit` 路由到会话 WebSocket
- edge 侧新增 `internal/edgeagent/webshell/agent_shell.go`：PTY 生命周期、
  每主机会话上限 5、主机关停开关
- manager 侧 `server/webshell` 新增 agent 分支：鉴权 / 审计 / 空闲超时 /
  admin 强踢全部复用现有设施

## 安全模型

| 维度 | 策略 |
| --- | --- |
| 鉴权 | 复用 casbin `device:shell exec`（平台账号体系），与 SSH 模式一致 |
| 审计 | 复用 `webshell_sessions` 表；agent 会话 `ssh_user` 记为 `edge-agent`，edge 回报的真实 OS 用户仅用于终端横幅展示 |
| 主机侧关停 | env `ONGRID_EDGE_AGENT_SHELL_DISABLED=true`（写入 /etc/ongrid-edge/ongrid-edge.env，主机管理员可控），每次 open 实时读取 |
| 并发 | edge 侧 agent 会话上限 5；manager 侧每用户 / 每设备上限、空闲超时、admin 强踢复用 |
| 信任边界 | shell 权限 = edge 进程权限（root），与 AI 助理 `host_bash` 工具同模型；连接弹窗文案明示 |

## 交互

- 连接弹窗顶部新增模式单选（默认「Edge Agent 直连」）；选直连时隐藏
  OS 用户 / 密码 / 端口输入
- 连接成功横幅：`-- Agent 直连已连接 (os_user@设备名) --`
- 失败提示：旧版 edge（未注册 shell_open）→「edge 版本过旧，不支持 Agent
  直连，请升级 edge」；主机开关关闭 → 透传主机策略提示

## 验收标准

1. 设备页打开终端，默认直连模式无需填写任何凭据即可进入交互终端，
   `whoami` 输出 edge 进程用户（root）
2. resize、`exit`、浏览器关闭、设置页 admin 强踢均正常结束会话
3. 审计列表出现该会话，`ssh_user = edge-agent`，含字节数 / 退出码 / 终止原因
4. 主机设置 `ONGRID_EDGE_AGENT_SHELL_DISABLED=true` 并重启 edge 后，
   直连被拒绝并给出策略提示；SSH 模式不受影响
5. 旧版 edge 上选择直连收到升级提示；SSH 模式行为不变
6. `go test -race`（edge/manager webshell 包）与前端 `tsc` 通过

## 兼容与回滚

- 协议向后兼容：`Mode` 为空走原 SSH 路径；新字段均带 `omitempty`
- 回滚：前端单选切回「SSH 账号密码」即回到旧路径；edge 新 handler 对旧
  manager 无副作用（不会被调用）
