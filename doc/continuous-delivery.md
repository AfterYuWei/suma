# 持续交付配置

「持续交付 → 新建项目」打开完整配置页面 `/continuous-delivery/new/project`，名称与配置在同一页填写，不再弹出独立的名称／节点表单。页面参考 [Portainer 的 Git 仓库 Stack 配置](https://docs.portainer.io/user/docker/stacks/add#option-3-git-repository)，保留 SUMA 已有的 Git 持续交付范围。

填写项目名称、Git Clone URL、Branch／Tag／Commit、按顺序合并的 Compose 文件及可选环境变量文件；选择 Git 认证、多个目标节点、已授权镜像仓库凭据、交付策略、轮询间隔、超时、自动回滚和 Webhook。文件路径相对于仓库根目录，禁止绝对路径和越界。认证中心凭据须授权给全部目标节点。新建时输入的项目凭据默认仅归该项目所有，也可在页面中勾选保存到认证中心并授权给所选节点。

在「Git 认证」区域点击「测试连接」，可在保存前检查当前仓库地址、凭据和 Branch／Tag。已有项目的空白敏感字段沿用已保存值；新输入的 Token／私钥仅用于此次测试，不会被保存，也不会触发同步或发布。修改连接相关字段后，旧结果自动隐藏。测试最多等待 30 秒。

成功会显示远端引用对应的 Commit；Commit 模式仅证明仓库可访问，指定 Commit 及 Compose 文件仍在正式同步时检查。失败会显示脱敏后的 Git 原因。404 可能表示地址错误，也可能是私有仓库无权访问。SUMA 始终使用凭据中填写的用户名；留空时统一使用 `oauth2`，不会按托管平台改写。私有仓库还需确认令牌拥有目标仓库的读取权限。

新建页面默认手动交付。保存配置本身不会排队同步或部署；自动交付项目由既有调度器按所选策略处理。手动项目创建后在详情页同步，并通过现有 Release 审批和发布流程执行。全局节点或 Group 筛选变化不重置创建草稿，既有选中的筛选外节点仍保留；离开有未保存更改的页面须确认。

每个项目都有「自动交付触发」开关，可在交付列表、详情概览或配置表单中修改。关闭后停止轮询与 Webhook 触发，已排队但未开始同步的自动任务也会在执行前拦截；已经开始的任务正常完成。配置、凭据、Webhook URL／Secret、目标节点和发布历史保留，开关不会改变 Observe／Manual／Auto 策略。暂停期间手动同步仅生成候选版本，仍按既有策略权限执行人工审批、发布和回滚。重新开启后，轮询按原间隔恢复，并继续接受已启用的 Webhook；切换开关本身不创建交付任务。

列表与详情的开关立即保存，配置表单中的开关随「保存 CD 配置」或创建项目一并保存。暂停时签名／Token 正确的 Webhook 返回 HTTP 200 和 `ignored: true`，不占用 Delivery ID；恢复后可由发送方显式重放，SUMA 不积压或补发暂停期间的事件。无效签名仍返回 401。

API：`PUT /api/v1/delivery-projects/:name/automation`，请求为 `{"enabled": false}`（暂停）或 `{"enabled": true}`（恢复），需要登录并记录审计。项目、配置及首页交付概览返回 `automation_enabled`；完整创建／配置也接受该字段，旧客户端省略时保留当前状态，新项目默认开启。

升级使用增量迁移 `0004_delivery_automation.sql`，只新增 `automation_enabled boolean NOT NULL DEFAULT true`，已有项目保持原行为，无需转换凭据或历史记录。迁移与台账写入沿用现有事务及 advisory lock，失败会整体回滚。上线前保留原应用文件和密钥，升级后核对项目数量、开关及历史记录；可用 `SELECT name, automation_enabled FROM delivery_projects ORDER BY name;` 检查回填。恢复时保留新增列与迁移台账，禁止重置数据库或密钥；回退到旧程序前先停止控制平面，旧程序不识别暂停状态，不能依赖此开关阻止其自动交付。

完整配置与项目、节点和凭据在一个事务中保存。校验或写入失败保留表单并回滚新增记录，可修正后直接重试，不会留下空项目。生成的 Webhook Secret 在创建页提供一次复制入口，存储和后续 GET 均不返回明文；复制后点击「打开交付项目」继续。已有项目的「设置」复用同一配置表单。

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
