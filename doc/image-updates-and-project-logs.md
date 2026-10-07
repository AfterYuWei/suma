# 镜像更新检测与项目聚合日志

## 使用

- 镜像页：点击「检查镜像更新」检查当前节点，或使用镜像行的检查按钮限定单镜像。检查对话框可按仓库选择已授权给节点的认证中心凭据，也可以匿名访问；关闭窗口后 Task 继续运行，可在任务中心取消。详情按引用分别显示平台、远端 manifest digest、时间及受影响实例。
- Project 服务页：统一展示服务／镜像、状态、资源和端口摘要，镜像检测状态跟随对应容器；点击服务行或使用 Enter／Space 展开容器身份、创建时间、全部 IPv4／IPv6／TCP／UDP 映射及镜像平台、检测时间和摘要。多副本分别展示，未部署服务保留提示，记录超过当前每页数量后显示分页。校验／保存只在配置页显示。检查仅覆盖该项目已部署容器的原始镜像引用。发现更新后，已接管 Project 可使用顶部「拉取并重建」进入现有审阅与确认流程；展开详情保留容器与 CD 交付项目链接。
- 镜像 → 镜像更新：配置当前节点的定时检测和仓库凭据；切换顶部节点选择器即可编辑对应策略。定时检测默认关闭，默认间隔 6 小时，可选 1/6/24 小时。启用后立即尝试一次；节点离线或已有检查时跳过周期。手动检查不移动定时计划。并发编辑冲突后须重新加载策略。设置页不再提供镜像更新入口；命令面板及旧 `/settings#image-updates` 书签会进入 `/images#image-updates`。
- Project 日志页：已接管和外部 Compose 项目均支持。默认聚合普通服务的最新 200 行；可选服务、实例、one-off、stdout/stderr/TTY，设置 100–5000 行、实时或历史时间范围，并搜索、暂停、清空、返回最新、下载当前已加载且符合筛选的文本。

## 判定与边界

检测由控制端通过验证证书的 HTTPS 请求仓库 manifest 和配置元数据，不下载镜像层，不读取控制端 Docker 登录配置。按本地 OS/架构/variant 解析远端配置摘要并比较本地 Image ID；同一镜像的多个 tag 独立检测。结果区分「需要拉取」与「容器需要重建」：本地标签已更新而实例仍用旧 ID 时显示「已拉取，待重建」。digest 固定引用不提示滚动更新。无标签、原始镜像 ID、凭据失败、限流、缺失平台和仓库不可达均给出原因。

全局最多 4 个仓库请求，每项 20 秒超时；单项失败不打断其他引用，最终 Task 报告失败数量。同节点互斥，重复请求返回 409 和现有 Task ID。PostgreSQL 只保存检测策略与凭据引用；观察结果保存在有界内存中，绑定节点运行时、引用、平台和本地 Image ID。重启后重新检测，过期结果显示过期状态。被策略引用的凭据不能删除、改变仓库地址或撤回对应节点授权；允许秘密轮换。

项目日志只用 Docker 精确项目标签发现源，不访问远端 Compose 文件，也不执行 Agent 主机命令。已停止容器可读取仍保留的日志；已删除或已轮转数据无法恢复。历史结果按真实时间合并，实时记录短批次合并，跨容器不保证严格全局顺序。实时每 5 秒重新发现实例，服务筛选跟随重建，实例筛选保持具体容器身份。连接有心跳与退避重连，离页、筛选变化、节点变化、断开和关闭服务均释放流。

每连接最多 64 个实例、5000 行及 8 MiB 缓存；单记录最多 64 KiB，截断与淘汰显示提示。暂停冻结视图并继续有界接收，恢复显示最新内容。下载仅导出已加载记录，包含时间、服务、实例和流。日志正文不写入应用日志、PostgreSQL 或 Audit。

## 界面时区

「设置 → 常规设置 → 时区」默认跟随浏览器所在设备，可输入或选择 IANA 时区，例如 `Asia/Shanghai`。保存后项目、镜像、日志、任务、审计等界面的时间立即统一更新；「使用系统时区」恢复设备自动识别。已有明确保存的时区继续保留。服务端设置 `general.timezone` 使用 `system` 表示设备默认，其他值必须为有效 IANA 时区；时间戳存储和 API 仍使用绝对时间。

项目日志的自定义起止时间按界面所示时区解释，导出同样包含该时区。夏令时跳变时不存在的时间会拒绝查询；重复时段选择较早的一次。日志正文保持原样。存储清理的节点菜单为「管理清理」，面板提供「清理策略」「清理预览」「执行历史」；已有策略的执行时区保持原值，下一次执行及历史时间按界面时区显示。

## 接口

HTTP 前缀为 `/api/v1/nodes/:nodeID`：

| 方法 / 路径 | 用途 |
| --- | --- |
| `GET /image-updates?project_name=...` | 观察结果与运行 Task |
| `POST /image-updates/check` | `image_ids` 或 `project_name`，返回 202 Task；省略目标则检查全节点 |
| `GET /image-updates/policy` | 策略，默认 `version:0` / disabled / 6 小时 |
| `PUT /image-updates/policy` | `expected_version`、`enabled`、`interval_hours`、`registry_credentials` |
| `GET /projects/compose/:name/log-sources` | 所有可选源，含 one-off/孤立标记 |
| `GET /projects/compose/:name/logs/history` | 有界结构化历史查询 |

日志查询支持 `tail`（默认 200）、`services` / `containers`（逗号分隔）、`include_one_off=true`、RFC3339 `since` / `until`。实时路径为 `/ws/nodes/:nodeID/projects/compose/:name/logs`，支持相同筛选但不能指定 `until`，消息为 `snapshot`、`entries`、`sources`、`source_error`。服务端验证项目归属、会话、Origin 和节点，旧文本日志 GET 保留。

## 验证与复现

```bash
cd /Projects/yuweinfo/dockport
GOCACHE=/tmp/suma-project-go-cache go -C server test ./...
GOCACHE=/tmp/suma-project-go-cache go -C server build ./...
GOCACHE=/tmp/suma-project-go-cache go -C server test -race ./internal/imageupdate ./internal/projectlogs ./internal/registry ./internal/api ./internal/credential ./internal/docker ./internal/node
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run test:browser
npm --prefix web run test:project-services
npm --prefix web run build
npm --prefix web run build:demo
bash doc/operations-smoke.sh
```

冒烟要求本机已有 `docker:27-dind`、`alpine:3.24` 和 `suma-agent:env-only-smoke`（Agent 镜像可由 `SUMA_AGENT_SMOKE_IMAGE` 指定），Docker 支持 privileged DinD，主机可用桥接地址 `172.17.0.1`。脚本创建一次性独立 daemon，生成私有受信任 HTTPS Registry、发布同 tag 两个真实 Alpine 配置版本，通过 Unix、mTLS TCP、HTTPS/WSS Agent 分别验证检查、HTTP 拉取、待重建状态及外部项目日志的多服务/副本、停止实例、stdout/stderr/TTY、空行、全局 tail 与 WebSocket 重建跟随；并运行现有 Project 部署验收。完成或失败均清理隔离 daemon、临时证书、Agent 容器及测试资源，不修改现有业务容器。

## 验收记录（2026-10-03）

- 后端完整测试与构建通过，镜像检测、聚合日志、Registry、应用装配、HTTP/WebSocket、认证凭据、Docker adapter 与节点服务的 race 检查通过。
- 33 项 Web 单元测试、31 项完整浏览器回归通过；最后的日志容量提示与缓存调整后，11 项功能浏览器用例再次通过。覆盖中英文、深浅主题、390px 移动端与 1440px 桌面、外部项目、暂停与恢复、搜索、流与服务筛选、历史/自定义时间、清空、下载内容与节点切换。
- Web lint/typecheck、生产与 demo 构建通过；生产 JS 扫描确认排除了 demo 会话、固定登录凭据和日志样例。仓库原有共享 UI lint 提示、CSS scrollbar 与较大 chunk 构建提示仍存在，均未造成门禁失败。
- 隔离真实 Docker 的 Unix、mTLS TCP、HTTPS/WSS Agent 冒烟全部通过，已清理测试 daemon 与 Agent。

实现依据：平台选择使用库提供的 `WithPlatform` 选项，见 [go-containerregistry v0.20.6 源码](https://github.com/google/go-containerregistry/blob/v0.20.6/pkg/v1/remote/options.go)；时间范围、行数和跟随映射到 Docker 原生日志参数，见 [Docker 日志查询文档](https://docs.docker.com/reference/cli/docker/container/logs/)。

## 界面时区验收（2026-10-03）

- 后端完整测试、构建及 settings/API race 检查通过；验证默认设备模式、PostgreSQL 重启持久化、HTTP 保存读取、非法时区拒绝且其他设置不被部分保存。
- 39 项 Web 单元测试、40 项完整浏览器回归通过；覆盖跨日期显示、不同设备与选择时区、半小时／45 分钟偏移、夏令时缺失／重复时段、中英文、深浅主题及移动端。复用有界时间格式化器后，9 项时区浏览器用例再次通过。
- Web lint/typecheck、生产/demo 构建通过；隔离 Docker 的 Unix、mTLS TCP、HTTPS/WSS Agent 冒烟通过并清理测试资源。存储清理入口和开启开关、设置即时生效／刷新保留／恢复系统时区、日志自定义时间查询和导出均经浏览器验证。

镜像更新入口迁移后，19 项相关浏览器用例通过；补充保存仓库凭据映射并切换节点后，8 组中英文／深浅主题／桌面及移动端用例再次通过。验证设置页入口移除、镜像页本地列表与检测策略切换、当前节点间隔和凭据隔离、旧书签跳转、命令面板入口。39 项 Web 单元测试、前后端门禁、生产/demo 构建及三种连接的隔离 Docker 冒烟均通过（2026-10-03）。
