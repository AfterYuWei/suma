# 项目聚合日志

镜像更新功能（检测、策略、候选发布与渠道发布）已于 2026-11-07 整体移除：迁移 `0010_remove_image_updates.sql` 删除其专属表，任务、审计与通知投递历史完整保留，通知规则中引用已移除事件的订阅不再触发。本文仅覆盖保留的项目聚合日志功能。

## 使用

- Project 服务页：按服务分组展示镜像、运行／健康、资源及端口；单副本直接显示容器，多副本展开实例，不同实际镜像版本明确标注。未创建服务保留，一次性容器单列；容器名称进入完整详情，日志／文件／终端使用带页签 Dialog。项目列表与弹窗说明见 `project-list.md`。
- Project 日志页：已接管和外部 Compose 项目均支持。默认聚合普通服务的最新 200 行；可选服务、实例、one-off、stdout/stderr/TTY，设置 100–5000 行、实时或历史时间范围，并搜索、暂停、清空、返回最新、下载当前已加载且符合筛选的文本。

## 判定与边界

项目日志只用 Docker 精确项目标签发现源，不访问远端 Compose 文件，也不执行 Agent 主机命令。已停止容器可读取仍保留的日志；已删除或已轮转数据无法恢复。历史结果按真实时间合并，实时记录短批次合并，跨容器不保证严格全局顺序。实时每 5 秒重新发现实例，服务筛选跟随重建，实例筛选保持具体容器身份。连接有心跳与退避重连，离页、筛选变化、节点变化、断开和关闭服务均释放流。

每连接最多 64 个实例、5000 行及 8 MiB 缓存；单记录最多 64 KiB，截断与淘汰显示提示。暂停冻结视图并继续有界接收，恢复显示最新内容。下载仅导出已加载记录，包含时间、服务、实例和流。日志正文不写入应用日志、PostgreSQL 或 Audit。

## 界面时区

「设置 → 常规设置 → 时区」默认跟随浏览器所在设备，可输入或选择 IANA 时区，例如 `Asia/Shanghai`。保存后项目、镜像、日志、任务、审计等界面的时间立即统一更新；「使用系统时区」恢复设备自动识别。已有明确保存的时区继续保留。服务端设置 `general.timezone` 使用 `system` 表示设备默认，其他值必须为有效 IANA 时区；时间戳存储和 API 仍使用绝对时间。

项目日志的自定义起止时间按界面所示时区解释，导出同样包含该时区。夏令时跳变时不存在的时间会拒绝查询；重复时段选择较早的一次。日志正文保持原样。存储清理的节点菜单为「管理清理」，面板提供「清理策略」「清理预览」「执行历史」；已有策略的执行时区保持原值，下一次执行及历史时间按界面时区显示。

## 接口

HTTP 前缀为 `/api/v1/nodes/:nodeID`：

| 方法 / 路径 | 用途 |
| --- | --- |
| `GET /projects/compose/:name/log-sources` | 所有可选源，含 one-off/孤立标记 |
| `GET /projects/compose/:name/logs/history` | 有界结构化历史查询 |

日志查询支持 `tail`（默认 200）、`services` / `containers`（逗号分隔）、`include_one_off=true`、RFC3339 `since` / `until`。实时路径为 `/ws/nodes/:nodeID/projects/compose/:name/logs`，支持相同筛选但不能指定 `until`，消息为 `snapshot`、`entries`、`sources`、`source_error`。服务端验证项目归属、会话、Origin 和节点，旧文本日志 GET 保留。

## 验证与复现

```bash
cd /Projects/yuweinfo/dockport
GOCACHE=/tmp/suma-project-go-cache go -C server test ./...
GOCACHE=/tmp/suma-project-go-cache go -C server build ./...
GOCACHE=/tmp/suma-project-go-cache go -C server test -race ./internal/projectlogs ./internal/api ./internal/credential ./internal/docker ./internal/node
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run test:browser
npm --prefix web run test:project-services
npm --prefix web run build
npm --prefix web run build:demo
bash doc/operations-smoke.sh
```

冒烟要求本机已有 `docker:27-dind`、`alpine:3.24` 和 `suma-agent:env-only-smoke`（Agent 镜像可由 `SUMA_AGENT_SMOKE_IMAGE` 指定），Docker 支持 privileged DinD，主机可用桥接地址 `172.17.0.1`。脚本创建一次性独立 daemon，通过 Unix、mTLS TCP、HTTPS/WSS Agent 分别验证 HTTP 拉取及外部项目日志的多服务/副本、停止实例、stdout/stderr/TTY、空行、全局 tail 与 WebSocket 重建跟随；并运行现有 Project 部署验收。完成或失败均清理隔离 daemon、临时证书、Agent 容器及测试资源，不修改现有业务容器。

## 验收记录（2026-10-03）

- 后端完整测试与构建通过，聚合日志、应用装配、HTTP/WebSocket、认证凭据、Docker adapter 与节点服务的 race 检查通过。
- 33 项 Web 单元测试、31 项完整浏览器回归通过；最后的日志容量提示与缓存调整后，11 项功能浏览器用例再次通过。覆盖中英文、深浅主题、390px 移动端与 1440px 桌面、外部项目、暂停与恢复、搜索、流与服务筛选、历史/自定义时间、清空、下载内容与节点切换。
- Web lint/typecheck、生产与 demo 构建通过；生产 JS 扫描确认排除了 demo 会话、固定登录凭据和日志样例。仓库原有共享 UI lint 提示、CSS scrollbar 与较大 chunk 构建提示仍存在，均未造成门禁失败。
- 隔离真实 Docker 的 Unix、mTLS TCP、HTTPS/WSS Agent 冒烟全部通过，已清理测试 daemon 与 Agent。

实现依据：时间范围、行数和跟随映射到 Docker 原生日志参数，见 [Docker 日志查询文档](https://docs.docker.com/reference/cli/docker/container/logs/)。

## 界面时区验收（2026-10-03）

- 后端完整测试、构建及 settings/API race 检查通过；验证默认设备模式、PostgreSQL 重启持久化、HTTP 保存读取、非法时区拒绝且其他设置不被部分保存。
- 39 项 Web 单元测试、40 项完整浏览器回归通过；覆盖跨日期显示、不同设备与选择时区、半小时／45 分钟偏移、夏令时缺失／重复时段、中英文、深浅主题及移动端。复用有界时间格式化器后，9 项时区浏览器用例再次通过。
- Web lint/typecheck、生产/demo 构建通过；隔离 Docker 的 Unix、mTLS TCP、HTTPS/WSS Agent 冒烟通过并清理测试资源。存储清理入口和开启开关、设置即时生效／刷新保留／恢复系统时区、日志自定义时间查询和导出均经浏览器验证。
