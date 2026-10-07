# Eino ADK 与 PostgreSQL 运行说明

SUMA 使用 Eino v0.9.21 和 PostgreSQL 18.6。Go 控制平面内运行 `OperationsAgent`；数据库保存业务状态、检查点和公开事件。不增加队列服务、独立 AI 服务或主机命令执行能力。

## 初始化与开发

本地原生启动与测试使用项目根目录 `.env.local`，无需在终端 `export`。运行 `make local-config` 创建权限为 `0600` 的私密模板，已有文件保持不变。当前本地数据库使用 `127.0.0.1:5432/suma`，填写对应用户名与密码：

```dotenv
SUMA_DATABASE_DSN='postgres://YOUR_USER:YOUR_PASSWORD@127.0.0.1:5432/suma?sslmode=disable'
SUMA_TEST_DATABASE_DSN=${SUMA_DATABASE_DSN}
```

用户名与密码中的特殊字符需要 URL 编码；单引号保持 `$` 字面值。`make dev`、`make server-check`、原生 `go run` 和 `go test` 自动读取同一文件。每个调用独立读取，不修改进程环境或执行 shell 文本；已有环境变量优先。`SUMA_ENV_FILE` 可以指定文件，设置为 `-` 时只使用进程环境。指定文件不存在、不可读或格式错误时明确失败，错误不含原始配置内容。`.env.local` 不提交 Git，生产镜像也不复制该文件。

生产 Compose 使用 `.env`：复制 `.env.example`，填写至少 32 位随机十六进制 `SUMA_POSTGRES_PASSWORD`。内置 PostgreSQL 不发布端口，SUMA 通过 `postgres:5432/suma` 连接，数据卷挂载 `/var/lib/postgresql`。如果本机没有现有数据库，可使用 `make db-up`；开发覆盖文件仅绑定 `127.0.0.1:55432`，相应修改 `.env.local` 端口。

原生服务必须在配置文件或进程环境中提供完整 `SUMA_DATABASE_DSN`。远程数据库使用 `sslmode=verify-full` 和可信 CA；本机及 Compose 内部网络使用 `sslmode=disable`。缺少 DSN、连接失败或迁移失败时启动失败，错误不打印凭据。`SUMA_DATA_ROOT` 独立用于 Compose、Git 和加密密钥，不从数据库地址推导。

使用空库初始化，不迁移旧开发数据。启动仅执行嵌入式 `internal/database/migrations/*.sql`，在事务与 advisory lock 下登记版本；不运行 AutoMigrate 或数据回填。`cmd/schema` 是开发阶段的基线生成工具：已经发布的迁移不可覆盖，后续变更应新增 SQL 版本。

测试必须在配置文件或进程环境中提供 `SUMA_TEST_DATABASE_DSN`，账户需要创建与删除 schema 的权限。开发环境可以使用本地 `suma` 库中的独立测试 schema；生产应使用独立测试库。`testutil.Open` 为每个测试创建随机 schema，测试结束清理；没有数据库时明确失败，不跳过或回退。

## `/Data` 保存内容

| 路径 | 内容 |
| --- | --- |
| `/Data/secret.key` | 凭据、检查点和 Compose 草稿的加密主密钥；丢失后不能解密对应数据库内容 |
| `/Data/compose/` | 托管 Compose 的 YAML、环境文件、配置附件和 SUMA 项目元信息 |
| `/Data/gitops/` | CD 仓库缓存及各修订工作区 |
| `/Data/backups/` | 保留目录，目前没有自动备份任务 |

应用设置、用户、节点、AI 模型／授权／会话／检查点、通知、Task 和审计在 PostgreSQL 中；数据库连接信息来自启动配置。`make dev` 默认根目录为 `server/data/`，生产镜像默认 `/Data`。PostgreSQL 的数据目录由其独立部署决定：项目内置数据库使用 Docker 数据卷；用户自行部署的 `/Data/PostgreSQL/` 是该数据库部署的文件，不是 SUMA 自动生成的配置目录。旧本地 `suma.db`／`dockport.db` 如仍存在，仅是历史遗留文件，当前服务不会读取或自动迁移它们。

## 目标与会话

Conversation、Message、Run、PlanStep、Interaction、Checkpoint、ToolCall、Event、ComposeDraft 和 Operation 分开保存。新任务初始没有节点。明确节点或经过核对的资源入口可确定目标；否则先选择节点，再开放 Docker 工具。页面顶部节点与 Group 不影响任务。节点目录仅显示允许且启用的节点，不向模型暴露端点或连接材料。

同一会话可以继承已确认上下文；新建会话清空目标。用户明确更换节点时清除不匹配的资源。显式跨节点请求冻结节点 ID 集合，新节点不会自动加入。资源名称通过实时目录解析为完整 ID；歧义使用结构化选择。

等待选择或审批时发送新消息会作废旧等待点和未执行提案。已完成步骤保留。当前 Task 已经运行时，新 Run 等待其结束后处理后续要求；取消不会自动回滚。

## Eino 中断与执行

TargetResolver → ChatModelAgent 组成 OperationsAgent。受控工具提供目录、状态、日志、计划、Compose 草稿和单项提案。工具与模型实例绑定到独立执行段，配置探针仍使用 Responses-only 传输。

Eino `Runner`、`StatefulInterrupt` 与 `ResumeWithParams` 保存并定向恢复节点选择、参数输入、审核和 Task 等待。检查点写入先缓冲，消费中断后再将加密检查点、交互、提案、步骤、Run 修订和公开事件一起提交。提交失败时不发布可操作的审核或输入点。客户端只提交业务 ID、修订和答案，不能提交 Eino 地址或检查点。

状态为 `queued → running → waiting_input / waiting_approval / waiting_task → running → completed`，另外保存 `paused / failed / canceled`。等待释放执行槽位。每次执行段最长五分钟，模型调用默认最多 40 次、操作默认最多 20 次，跨恢复累计。只读次数、日志限制、并发和自动诊断日额度沿用设置。模型瞬态错误最多重试两次；Docker 写操作不自动重试。

批准、准备 Task 与 Audit 在一个事务中提交。固定预览、资源与运行时身份、配置修订发生变化时拒绝执行。Task 完成后核对实际容器状态／启动时间、镜像 digest、资源存在性、Compose 修订和服务状态。失败、拒绝、过期或不确定结果暂停后续步骤。Task 终态通过数据库轮询补偿唤醒，重复审批和回答复用原结果。

重启保留等待点；模型段或变更执行中断则暂停核实。已发生或可能发生的 Docker 写操作不因检查点重放。应用数据事务可以去重，外部 Docker 变更不承诺 exactly-once。

## Compose 与变更目录

容器生命周期、镜像拉取／标签／删除、网络与卷创建／删除、Project 配置／生命周期／接管／清理、现有 CD Release 发布／重试／回滚和手动保护清理均通过业务服务与节点运行时执行。终端、容器文件编辑、凭据和策略修改继续使用专门界面。

镜像 tag 先通过审计过的元数据 Task 解析为 digest，再生成固定 digest 拉取提案。已有镜像检测使用原检测 Task。

Compose 草稿敏感叶子使用绑定原 YAML 路径的服务端引用。模型不能移动、改名、删除引用，也不能新增凭据。环境文件不传给模型。草稿校验、脱敏修改前后内容与基础修订保存在加密草稿中；保存配置与部署独立审批。部署冻结本地镜像并禁止隐式 pull/build，远程 bind、Compose 根目录与 Docker socket 风险规则继续生效。

卷删除必须输入完整卷名，并重新检查使用关系与保护引用。Project 删除、接管和清理需要完整名称；强制选项和 Docker socket 风险需要用户勾选。自然语言同意不会触发批准。容器与 Project 删除保留卷，卷使用独立审核步骤。

构建缓存单独审核固定 cutoff、保留空间和预览候选，执行前检查范围；Engine 决定最终可清理项并可能保留缓存。结果包含实际检查，不把容量估算当作删除承诺。

## Web 与聊天

运维菜单保留 AI 工作台，模型选择在发送按钮左侧。任务目标与来源、结构化输入、紧凑计划列表、完整审核 Sheet、配置差异和结果验证在同一会话中显示。

`/ws/ai/conversations/:id?after=SEQ` 重连补发有序公开事件。关闭观察连接仅取消订阅；业务工作流继续运行。

绑定且通过权限检查的通知身份共用运行时，会话按用户、绑定和 Chat ID 隔离。公开事件使用持久化投递游标和租约，重启后续发；发送前重新验证绑定。Telegram 与飞书应用可显示选择按钮，其他交互用 `/select CODE 编号或参数`。代码绑定身份、等待点、修订和有效期。需要名称或风险勾选的审批在工作台完成。发送成功后游标提交前中断可能导致重复通知，但旧按钮不能批准新提案。

未绑定身份继续使用安全摘要服务，不进入 Agent。自动诊断使用事件真实节点和资源；可以收集证据和创建待审核提案，不代填人类确认或执行变更。

## 验证命令

- `make check`：Go test/build，Web lint/typecheck/build。
- `go -C server test -race ./internal/ai ./internal/database ./internal/app ./internal/compose ./internal/api`。
- `cd web && npx playwright test e2e/ai-workbench.spec.ts e2e/ai-chat.spec.ts e2e/ai-settings.spec.ts`。
- `doc/operations-smoke.sh`：在一次性 Docker Engine 上覆盖 Unix、mTLS TCP 和 HTTPS/WSS Agent；需要 PostgreSQL 测试 DSN、缓存的 dind/alpine 和测试 Agent 镜像。

参考：[Eino HITL](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_hitl/)、[Eino v0.9.21](https://github.com/cloudwego/eino/releases/tag/v0.9.21)、[PostgreSQL 官方镜像与卷路径](https://hub.docker.com/_/postgres)。
