# 通知中心与 AI 运维

入口：**设置 → 通知渠道**、**设置 → AI 运维**、**运维 → AI 工作台**。顶部铃铛打开站内消息，待审核入口打开操作列表。容器、托管 Project、镜像和任务详情提供“交给 AI 分析”。命令面板支持通知渠道、通知规则、诊断和待审核操作。

## 添加渠道

向导按“选择平台 → 配置必要信息 → 校验／测试 → 选择订阅”接入。渠道可以停用保存。Token、App Secret、Webhook URL、Authorization 和签名密钥使用现有 AES-GCM 存储，读取接口只返回已配置标记；编辑时留空保留原值。配置平台或机器人身份变更后，原聊天绑定和审批按钮失效。

- **Telegram**：从 BotFather 获取专用 Bot Token。开启渠道及聊天能力后，私聊机器人，或在群里 @ 机器人，页面会显示已识别的会话，选定后再次保存再发测试。使用长轮询；已有 Webhook 的 Bot 不会被 SUMA 擅自重置，应先在 Telegram 侧切换成轮询。绑定只支持私聊。
- **飞书自定义机器人**：填写 `https://open.feishu.cn/open-apis/bot/v2/hook/...` 地址及可选签名密钥。支持通知和站内详情链接。无需应用身份的消息读取权限。
- **飞书应用机器人**：填写企业自建应用的 App ID 和 App Secret，开启机器人能力，配置可用范围并发布应用版本。权限说明始终出现在添加及编辑表单中。
- **通用 Webhook**：填写 HTTPS 地址，可选 Authorization 与签名密钥。内网目标必须明确开启“允许内部网络地址”；HTTP 仅允许内网或回环地址。禁止重定向、元数据／链路本地目标，连接重新解析 DNS 并核验实际 IP。

### 飞书权限与事件

| 权限／配置 | 用途 |
| --- | --- |
| `im:message:send_as_bot` | 必需：以机器人身份发送通知、回复及操作卡片 |
| `im:message.p2p_msg:readonly` | 聊天必需：读取用户发送给机器人的私聊消息 |
| `im:message.group_at_msg:readonly` | 可选：读取群内 @ 此机器人的消息 |
| 事件 `im.message.receive_v1` | 接收自然语言请求与绑定码 |
| 回调 `card.action.trigger` | 接收查看预览、批准和拒绝按钮 |
| 事件与回调 → 使用长连接接收 | 官方 SDK 建立出站 WebSocket；无需公网回调 URL |

权限以应用身份开通，发布新版本后生效。将机器人加入专用测试群，或在可用范围内的用户私聊机器人，页面才会识别会话。只校验 App Secret 成功不能证明消息发送、权限或卡片回调配置正确，应继续发送测试卡片和验证绑定。SUMA 不要求读取全部群消息或通讯录。

参考：[飞书发送消息和权限](https://open.feishu.cn/document/server-docs/im-v1/message/create)、[官方 Go SDK 长连接配置](https://github.com/larksuite/oapi-sdk-go/blob/v3_main/doc/channel.zh.md)、[Telegram getUpdates](https://core.telegram.org/bots/api#getupdates)。

### Webhook 格式与签名

接收 JSON：`schema_version: 1`、`message`、`events`、`operation_id`、`url`。每个事件包含 ID、类型、时间、严重程度、范围、节点／资源以及关联 Task、Release、AI Run／Operation。

启用签名时，`X-SUMA-Timestamp` 是 Unix 秒，`X-SUMA-Signature` 为 `sha256=` 加十六进制 HMAC-SHA256，签名内容是 `timestamp + "." + 原始请求正文`。接收方应校验签名、限制时间偏差，并按事件 ID 去重。外部投递为至少一次语义，超时可能造成重复消息。

## 事件、规则与发送记录

事件目录覆盖账户安全、节点与 Agent、容器与 Project、镜像更新、持续交付、存储清理、任务、审批及渠道／模型自身故障。密码登录必须完成 MFA 才产生成功通知。首次登录 IP 独立记录；同一身份或 IP 五分钟五次登录失败触发安全事件。

规则提供账户安全、重要异常、镜像更新、发布动态和清理报告预设，可按事件、严重程度、节点／节点组、Project 选择多个渠道及备用渠道。支持即时、五分钟合并、按 IANA 时区的每日摘要、跨午夜静默时段、临时静默和恢复通知。严重安全事件及待审批事件绕过静默。消息模板只替换固定字段，不执行脚本。

同一资源同类异常十分钟内合并，镜像事件按目标 digest 去重，每项 AI 提案独立通知。Docker 事件流辅以状态核对；主动生命周期操作和对应 Project／CD 重建按资源关联，避免隐藏同节点的其他异常。节点连续两次离线观察后通知，恢复单独记录。主机磁盘容量不在首版范围内。

SQLite 保存事件历史、每用户已读状态和投递队列。队列最多四个并发发送、单次 40 秒截止、一分钟租约，最多五次重试，指数退避并遵守平台 `Retry-After`。重启恢复未完成投递；失败可手动重发。单个摘要最多 200 个事件，正文最多 12 KiB，完整事件仍可在站内查看。备用渠道失败不递归生成备用任务，渠道失败事件不递归通知自身。

## 配置 AI 与发起诊断

工作台的会话采用官方 shadcn [Message](https://ui.shadcn.com/docs/components/base/message)、[Bubble](https://ui.shadcn.com/docs/components/base/bubble) 和 [MessageScroller](https://ui.shadcn.com/docs/components/base/message-scroller)，输入框复用 InputGroup。问题与回答按轮次排列，连续追问保留会话历史；桌面通过侧栏选择会话，移动端通过历史抽屉选择。布局沿用设置页的间距分组、项目详情的 muted 背景和共享圆角，不使用侧栏、标题、证据或输入区的线条分隔。输入框位于对话底部，Enter 发送、Shift + Enter 换行，中文输入法确认候选不会发送。阅读旧消息时保持位置，可点击“回到最新回复”；诊断证据与事件时间线按需展开，建议操作仍进入完整预览逐项审核。

AI 默认关闭；启用时必须明确授权节点。填写模型名称、API 基础地址和密钥，选择 Responses 或兼容 Chat Completions 协议。服务地址默认要求 HTTPS，内网地址需明确允许。点击“测试已保存的连接”，分别验证文本和注册工具调用；不支持工具的模型只能摘要。参考：[OpenAI 工具调用](https://developers.openai.com/api/docs/guides/function-calling)。

默认最多两个并发诊断、每天二十次自动诊断、同一事件十分钟内不重复自动分析。每次最多八次只读工具调用，日志仅最近十五分钟，最多 500 行／64 KiB；诊断最长五分钟。模型输出和日志均有限制及脱敏，配置摘要不包含环境变量值或命令内容。会话最多携带四轮历史摘要，同节点的新诊断重新收集证据。

镜像证据包含已有更新检查、检查时间、目标 digest 和当前受影响服务；诊断读取不会发起注册表检查，未检查或过期的结果会明确标注。清理证据包含当前候选、保护／保留原因和最近实际清理结果；卷与构建缓存不进入 AI 可申请集合。任务日志与容器日志沿用相同时间和大小限制。

容器证据包含退出码、OOM 标志、重启次数及健康状态／连续失败次数；环境变量、启动命令和健康检查原始输出不进入状态摘要。

结果展示采集来源、时间、脱敏证据、缺失信息、建议和关联提案。模型仅可调用 `read_status`、`read_logs` 和 `create_proposal`。日志中的指令、虚构工具、自然语言“批准”均不会执行变更。用量耗尽或模型故障不影响常规通知。

## 每项变更单独审核

允许动作：容器启动／停止／重启，固定 `sha256` digest 镜像拉取，托管 Project 的现有配置更新，指定节点的 CD 发布／回滚，以及受保护清理。模型不能修改凭证、Compose 文本、自动策略，不能执行任意命令、删除卷或清理构建缓存。

审批预览含固定节点和资源、参数、当前状态、配置和镜像身份、影响、停机与删除风险、回退方式；用户勾选已阅读完整预览后才能批准。默认十五分钟失效。审批时及执行时再次核验运行时、资源状态、配置和镜像；变化后需重新提案。重复按钮、并发请求和平台重复事件只能启动一次 Task。

镜像拉取与容器重建分别审核。私有镜像使用 Images 中现有的节点授权注册表映射；凭证身份也被冻结，凭证或授权改变后原审批失效。模型看不到注册表秘密。

Project／CD 使用统一 ComposeRunner，把既有渲染配置冻结到私有临时文件，固定每个服务的本地镜像 ID，执行 `up --no-build --pull never --wait`。本地 config／secret 文件内容也冻结及校验，预览只保留哈希。外部或环境型 config／secret 来源无法冻结时拒绝 AI 部署，用户可使用既有人工入口处理。AI 发布没有自动回滚，失败后的重试或回滚需新审批。

清理只能删除审核中冻结的停止容器、未引用镜像和网络；每次删除重新检查引用、保护规则与策略版本，引用或保护变化则跳过或中止，不扩展集合，不强制删除。实际结果和回收量沿用 CleanupRun。

关闭 AI 会取消新诊断、使未开始操作失效并请求取消运行中 AI Task。已生效变更保留实际结果。重启将运行中操作标记为中断，按仍有效的授权核对实际状态并记入审计；不会自动重放。

## 聊天绑定与审批

站内确认完成且仍有效的账号绑定就是操作白名单。未绑定、尚未确认或已撤销的身份只可查询管理员在 AI 设置授权节点的安全摘要，包括可用状态、容器状态数量、已有镜像检查数量和最近清理统计。AI 关闭或节点未授权时不提供查询；查询只在已开启聊天功能的渠道可用。

非白名单查询不调用模型、不读取原始诊断记录，不返回节点／资源名称和 ID、地址、镜像引用／digest、配置、环境变量、日志、任务输出、错误详情、密钥或凭证，也不回显请求内容。回答从固定数字和布尔字段生成，不依赖模型遵守提示。每个平台身份十秒内最多一次查询，最多两个摘要采集并发；越权节点或内部错误只返回固定说明。查询使用 `/node NODE_ID status`，只有一个授权节点时可直接提问。审批预览、批准、拒绝以及任何变更入口均要求有效白名单身份；“好的”、伪造昵称和 SUMA 用户编号不会授予权限。

1. 站内 AI 设置选择已启用的聊天渠道，生成十分钟、一次性的绑定码。
2. 在机器人私聊发送 `/bind CODE`。
3. 返回站内核对稳定平台用户 ID，再确认绑定。昵称和群成员身份不能授予权限。
4. 单个授权节点可直接提问；多个节点时使用 `/node NODE_ID 问题`。连续请求沿用同一绑定、会话及节点的历史。
5. 事件卡片的“查看并审核”或 `/approve OPERATION_ID` 只打开完整预览。之后使用预览卡片的明确批准／拒绝按钮。按钮使用绑定到当前平台身份、会话、操作和过期时间的一次性令牌。文字“好的”“继续”不会批准。

站内与聊天使用同一审批服务。撤销绑定、停用渠道、替换 Bot 身份后，原操作权限和按钮立即失效；渠道仍启用时，撤销身份只能查询安全摘要。白名单身份的每一项变更也必须单独审核。审计包含来源、请求者、读取工具、资源、提案、审核人、平台身份、入口、Task 和结果，不记录秘密或未脱敏日志。

## 全局审计与 AI 子集

审计日志是总入口，默认显示所有节点与控制平面的审计，包含常规操作、AI 诊断与读取、提案、批准／拒绝、审批过期、执行结果，以及聊天安全查询／访问拒绝。可按当前节点或控制平面筛选，并跳转到关联 AI 记录和 Task。关联任务按具体任务 ID 加载详情与日志，不受当前节点列表筛选影响。

AI 工作台仅展示这份统一日志中的 AI 部分；两处使用同一记录 ID，不分别写入两份历史。旧版独立 AI 审计在启动时按旧 ID 唯一导入，保留发生时间和关联，不因重启重复导入。批准状态、待执行 Task 与统一审计在同一 SQLite 事务中提交；审计无法写入时审批会回滚。

安全查询审计仅记录平台身份、目标授权节点及成功／拒绝结果，不保存提问、审批令牌、密钥、原始日志或查询输出。查询错误不进入审计详情。

## 验证与复现

```bash
GOCACHE=/tmp/suma-notification-go-cache go -C server test ./...
GOCACHE=/tmp/suma-notification-go-cache go -C server build ./...
GOCACHE=/tmp/suma-notification-go-cache go -C server test -race ./internal/audit ./internal/app ./internal/ai ./internal/notification ./internal/outbound ./internal/redact ./internal/task
npm --prefix web run lint
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
npm --prefix web run build:demo
npm --prefix web run test:browser -- ai-chat.spec.ts notification-ai.spec.ts operations.spec.ts
SUMA_PROJECT_SMOKE_GO_CACHE=/tmp/suma-notification-go-cache bash doc/operations-smoke.sh
```

Docker 脚本使用可清理的隔离 Docker 27 daemon，覆盖 Unix、mTLS TCP、HTTPS/WSS Agent；需要本地已有 `docker:27-dind`、`alpine:3.24` 和测试 Agent 镜像，不删除用户资源。包含真实固定镜像 Compose 应用、通知事件、AI 提案、审批、容器重启及重复审批拒绝。

真实飞书验收用私有、0600 的 JSON 文件提供 `app_id` 和 `app_secret`，不将秘密写入仓库或命令行：

```bash
SUMA_LIVE_FEISHU_CREDENTIALS=/path/to/private-test-credentials.json \
GOCACHE=/tmp/suma-notification-go-cache \
go -C server test ./internal/notification -run '^TestLiveFeishuApplication$' -count=1 -timeout 50s -v
```

另设 `SUMA_LIVE_FEISHU_CHAT_ID=oc_...` 才发送专用测试卡片。完整聊天验收还需在实际 SUMA 实例完成绑定、撤销、卡片回调和逐项审批。仅机器人凭证和 WebSocket 测试不代表完整聊天验收通过。

截至 2026-10-03，飞书测试应用凭证、机器人和官方出站 WebSocket已通过真实验证。真实消息／回调／绑定验收仍需用户指定测试 `chat_id` 或私聊机器人；Telegram 仍需专用测试 Bot 和会话。进度及未完成项见 `PLANS.md`。
