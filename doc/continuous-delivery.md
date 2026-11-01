# 持续交付配置

「持续交付 → 新建项目」打开完整配置页面 `/continuous-delivery/new/project`，名称与配置在同一页填写，不再弹出独立的名称／节点表单。页面参考 [Portainer 的 Git 仓库 Stack 配置](https://docs.portainer.io/user/docker/stacks/add#option-3-git-repository)，保留 SUMA 已有的 Git 持续交付范围。

填写项目名称、Git Clone URL、Branch／Tag／Commit、按顺序合并的 Compose 文件及可选环境变量文件；选择 Git 认证、多个目标节点、已授权镜像仓库凭据、交付策略、轮询间隔、超时、自动回滚和 Webhook。文件路径相对于仓库根目录，禁止绝对路径和越界。认证中心凭据须授权给全部目标节点。新建时输入的项目凭据默认仅归该项目所有，也可在页面中勾选保存到认证中心并授权给所选节点。

「手动环境变量」可在新建或设置页逐项填写名称和值，也可以勾选「空值」。手动值按原文用于 Compose `${VARIABLE}` 插值，优先于所选环境变量文件（未指定文件时仍加载首个 Compose 文件目录的 `.env`）；不会自动注入全部容器。容器需要的变量须显式引用，例如：

```yaml
services:
  app:
    image: example/app:${APP_VERSION}
    environment:
      DATABASE_PASSWORD: ${DATABASE_PASSWORD}
```

点击「快速添加」可一次粘贴多行 `KEY=VALUE`，支持空行、`#` 注释、`export KEY=VALUE`、单／双引号包裹的值及 `KEY=` 空值。每个变量占一行；只去除包裹引号，不展开 `$` 或转换反斜杠转义，值中的 `=` 和不带前置空格的 `#` 按原文保留。引号内可保留首尾空格；复杂多行值应通过其他受控客户端配置。输入默认隐藏，可主动显示；预览仅列名称和新增／替换／保留数量。已有同名项默认保留，勾选「替换同名变量」才覆盖。输入内部重名、格式错误、保留名称或数量／大小超限会显示行号或具体限制，并阻止添加，不会部分更新表单。点击「添加到表单」或 `Cmd/Ctrl+Enter` 只修改草稿，最后仍需保存配置；取消／关闭会清空本次粘贴内容。

变量值加密保存在现有应用密钥下，默认隐藏输入；普通配置响应仅返回变量名和「已保存」状态。点击「显示变量值」会读取当前项目的已保存值，可直接查看和编辑，包括原先保存为空的值。仅查看不会修改草稿或重写未编辑值；点击「隐藏变量值」会清除临时读取的内容，已编辑值继续以密码形式保留。保存或离开设置页后重新隐藏，下次显示时重新读取。读取失败可重试，不影响已有草稿。

输入新值可替换，清空输入框或勾选「空值」可保存为空，删除行并保存可移除；未修改项沿用原值。已保存名称只读，改名需删除后新增。最多 100 项，名称为字母／数字／下划线且不能以数字开头，值最多 64 KiB、名称与值合计最多 256 KiB。`DOCKER_*`、`COMPOSE_*`、`HOME`、`PATH`、`TMPDIR` 保留给执行器，不能用于手动插值。

同一项目、仓库和 Git Commit 始终对应一条 Release，变量或节点变化不再产生新 Release。发布直接使用提交时设置页已保存的配置，无需为配置变化额外同步；每次操作保存独立的加密配置快照。重试沿用失败记录的快照，回滚使用明确选择的记录快照。执行器每次调用使用权限 `0600` 的临时环境文件并立即清理，不修改 Git 工作树；含手动变量时隐藏 Compose 原始输出和失败诊断，仅保留任务阶段进度及脱敏运行摘要。

升级使用增量迁移 `0005_delivery_environment.sql`，仅在项目与 Release 新增可空密文字段。旧记录保持无手动变量，不回填或推断历史值；升级前保留数据库、应用文件与现有密钥，升级后核对项目／Release 数量、原凭据、历史证据和变量为空。失败由现有迁移事务回滚；回退旧程序前停止自动触发并停止控制平面，保留新增列、迁移台账、应用文件与密钥，旧程序不会应用手动变量。恢复新程序后重新核对候选及活动版本，禁止重置生产数据库或密钥。

在「Git 认证」区域点击「测试连接」，可在保存前检查当前仓库地址、凭据和 Branch／Tag。已有项目的空白敏感字段沿用已保存值；新输入的 Token／私钥仅用于此次测试，不会被保存，也不会触发同步或发布。修改连接相关字段后，旧结果自动隐藏。测试最多等待 30 秒。

成功会显示远端引用对应的 Commit；Commit 模式仅证明仓库可访问，指定 Commit 及 Compose 文件仍在正式同步时检查。失败会显示脱敏后的 Git 原因。404 可能表示地址错误，也可能是私有仓库无权访问。SUMA 始终使用凭据中填写的用户名；留空时统一使用 `oauth2`，不会按托管平台改写。私有仓库还需确认令牌拥有目标仓库的读取权限。

新建页面默认手动交付。保存配置本身不会排队同步或部署；自动交付项目由既有调度器按所选策略处理。手动项目创建后在详情页同步，在 Release 确认后直接发布，确认同时记录批准人。全局节点或 Group 筛选变化不重置创建草稿，既有选中的筛选外节点仍保留；离开有未保存更改的页面须确认。

Release 确认发布后，版本信息旁显示执行人姓名；拒绝为红色叉号，发布者为绿色勾号，收起详情时也可查看。拒绝信息不再单独显示在详情提示条中，实际部署错误仍保留。批准人姓名来自已记录的审批人账号；账号无法读取时显示「未知用户」。

Release 详情中的节点交付表使用固定列宽和顶部对齐，任务 ID 下方预留进度空间，阶段消息单行显示，切换状态及进度条显隐时保持布局稳定。长阶段消息可悬浮或键盘聚焦查看全文；窄屏可在表格内横向滚动，错误原因及展开的尝试历史完整保留。

Release 右侧主要操作使用黑底白字按钮。候选状态为「待发布」，点击「发布」确认后同时批准并立即启动交付，不再经过独立「已批准」状态。发布使用当前设置中的 Compose 文件、手动变量、节点、凭据引用、超时和自动回滚策略，Git Commit 使用所选 Release。存在可重试节点时主要按钮显示「重试（数量）」；确认框用独立的「失败节点」区域逐行以红色列出节点，不把名单嵌入说明句。重试仅使用失败记录的配置，不应用之后修改的设置。要使用新设置则执行「发布」或「重新发布全部节点」。

「更多」按状态提供拒绝、重新发布全部节点、回滚失败节点和「回滚」。回滚失败节点仍分别恢复各节点部署前的配置；整版「回滚」打开快照选择弹窗，列出该 Release 的记录，显示操作、执行人、时间、成功／失败状态和配置指纹。所有完整快照均可选择，包括失败、部分失败记录，执行前会重新验证。历史缺失配置的记录只读展示并说明缺失原因。选定后可检查 Compose／环境文件、隐藏的变量值、节点、镜像、凭据引用和执行策略。回滚默认使用快照节点，折叠的「高级选项」允许临时覆盖；覆盖只写入本次新记录，不改项目设置或原快照，关闭弹窗即清空。节点删除／禁用、凭据授权失效或部署策略不通过时阻止执行并明确报错。

新增「记录」Tab，按一次操作统计发布、重试、手动回滚、失败节点回滚和自动回滚，支持 Release／类型／状态筛选与服务端分页。展开可查看任务入口、节点结果和配置快照；变量默认隐藏，主动显示才读取，隐藏或离开后清除临时读取结果。记录的「回滚」打开同一弹窗并预选该记录。失败发布及其自动回滚分别留证，后续重试成功不会改写原失败记录。Release 数量只随新 Git Commit 增加，列表不因这些操作新增版本。

升级使用新增 `0007_delivery_release_records.sql`（`0006` 已由项目镜像更新占用），通过现有迁移事务、锁和台账运行。重复 Git 身份以最早 Release 编号作为入口，旧编号作为映射保留，任务／审计／通知的原始关联不删除；旧节点尝试按原 ID 转换，不能确认的操作分组和未保存的策略不猜测。旧快照缺失信息时保持只读。升级前保留数据库、应用文件与现有密钥；核对旧编号、历史记录和活动节点关联后再启用交付。迁移失败由事务回滚；迁移成功并产生新记录后，恢复应使用本版本，禁止用不了解新记录的旧版本继续交付。需要恢复旧程序时，先停止控制平面并使用升级前保存的完整数据库／文件状态与原密钥恢复，不清空生产数据、不删除迁移台账。

回滚重新应用所选记录快照的 Compose 文件及环境变量，并先以 `pull --policy always` 拉取其中声明的镜像。因此 `eizhu-admin:latest` 会使用仓库中当前最新的镜像；只有固定 digest 才会恢复到相同镜像内容。回滚、失败后重发及失败节点重试会使用 `up --force-recreate --remove-orphans --wait` 替换并启动容器，即使配置与镜像未变化也会重建；拉取失败时不执行容器替换。操作保留数据卷，健康检查通过后才标记完成。

顶部保留「同步 Git」，「删除项目」与「强制删除」收进项目更多菜单。菜单说明普通删除保留运行中的容器，强制删除清理容器和网络、默认删除命名卷及数据；原项目名称确认和保留命名卷选项继续保留。

每个项目都有「自动交付触发」开关，可在交付列表、详情概览或配置表单中修改。关闭后停止轮询与 Webhook 触发，已排队但未开始同步的自动任务也会在执行前拦截；已经开始的任务正常完成。配置、凭据、Webhook URL／Secret、目标节点和发布历史保留，开关不会改变 Observe／Manual／Auto 策略。暂停期间手动同步仅生成候选版本，仍按既有策略权限执行人工审批、发布和回滚。重新开启后，轮询按原间隔恢复，并继续接受已启用的 Webhook；切换开关本身不创建交付任务。

列表与详情的开关立即保存，配置表单中的开关随「保存 CD 配置」或创建项目一并保存。暂停时签名／Token 正确的 Webhook 返回 HTTP 200 和 `ignored: true`，不占用 Delivery ID；恢复后可由发送方显式重放，SUMA 不积压或补发暂停期间的事件。无效签名仍返回 401。

API：`PUT /api/v1/delivery-projects/:name/automation`，请求为 `{"enabled": false}`（暂停）或 `{"enabled": true}`（恢复），需要登录并记录审计。项目、配置及首页交付概览返回 `automation_enabled`；完整创建／配置也接受该字段，旧客户端省略时保留当前状态，新项目默认开启。

升级使用增量迁移 `0004_delivery_automation.sql`，只新增 `automation_enabled boolean NOT NULL DEFAULT true`，已有项目保持原行为，无需转换凭据或历史记录。迁移与台账写入沿用现有事务及 advisory lock，失败会整体回滚。上线前保留原应用文件和密钥，升级后核对项目数量、开关及历史记录；可用 `SELECT name, automation_enabled FROM delivery_projects ORDER BY name;` 检查回填。恢复时保留新增列与迁移台账，禁止重置数据库或密钥；回退到旧程序前先停止控制平面，旧程序不识别暂停状态，不能依赖此开关阻止其自动交付。

完整配置与项目、节点和凭据在一个事务中保存。校验或写入失败保留表单并回滚新增记录，可修正后直接重试，不会留下空项目。生成的 Webhook Secret 在创建页提供一次复制入口，存储和后续 GET 均不返回明文；复制后点击「打开交付项目」继续。已有项目的「设置」复用同一配置表单。

本次按用户要求仅使用 mock／替代执行器验收，不在本机安装或启动数据库、Docker 等基础服务。无数据库的定向验证：

```bash
GOPATH=/tmp/suma-go GOCACHE=/tmp/suma-go-cache go -C server test ./internal/cd ./internal/compose -run '^TestManualEnvironment|^TestExecutionSpec' -count=1
npm --prefix web exec -- vitest run src/features/delivery/environment-input.test.ts
npm --prefix web run test:browser -- e2e/continuous-delivery-create.spec.ts e2e/continuous-delivery-connection.spec.ts e2e/continuous-delivery-environment.spec.ts
```

新增的 PostgreSQL HTTP／持久化／升级测试保留供具备专用测试数据库的环境运行；本机未执行这些集成验收，也未执行真实 Docker 验收。

验证命令：

```bash
GOCACHE=/tmp/suma-project-go-cache go -C server test ./internal/cd -run '^TestCreateConfiguredProject'
GOCACHE=/tmp/suma-project-go-cache go -C server test ./internal/api -run '^TestDelivery(FullConfigurationCreation|CreationFailureAndLegacy)HTTP$'
GOCACHE=/tmp/suma-project-go-cache go -C server test ./internal/git -run '^TestConnection'
GOCACHE=/tmp/suma-project-go-cache go -C server test ./internal/api -run '^TestDeliveryConnectionHTTP'
go -C server test ./internal/cd -run 'TestPause|TestPaused|TestCreateConfiguredProjectWithAutomation'
go -C server test ./internal/database -run '^TestDeliveryAutomation'
go -C server test ./internal/api -run 'TestDeliveryAutomation|TestPausedDeliveryWebhook'
npm --prefix web run test:browser -- e2e/continuous-delivery-create.spec.ts e2e/continuous-delivery-connection.spec.ts
npm --prefix web run test:project-services -- e2e-project-services/delivery-automation.spec.ts
```
