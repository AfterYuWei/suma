# 持续交付配置

「持续交付 → 新建项目」打开完整配置页面 `/continuous-delivery/new/project`，名称与配置在同一页填写，不再弹出独立的名称／节点表单。页面参考 [Portainer 的 Git 仓库 Stack 配置](https://docs.portainer.io/user/docker/stacks/add#option-3-git-repository)，保留 SUMA 已有的 Git 持续交付范围。

填写项目名称、Git Clone URL、Branch／Tag／Commit、按顺序合并的 Compose 文件及可选环境变量文件；选择 Git 认证、多个目标节点、已授权镜像仓库凭据、交付策略、轮询间隔、超时、自动回滚和 Webhook。文件路径相对于仓库根目录，禁止绝对路径和越界。认证中心凭据须授权给全部目标节点。新建时输入的项目凭据默认仅归该项目所有，也可在页面中勾选保存到认证中心并授权给所选节点。

新建页面默认手动交付。保存配置本身不会排队同步或部署；自动交付项目由既有调度器按所选策略处理。手动项目创建后在详情页同步，并通过现有 Release 审批和发布流程执行。全局节点或 Group 筛选变化不重置创建草稿，既有选中的筛选外节点仍保留；离开有未保存更改的页面须确认。

完整配置与项目、节点和凭据在一个事务中保存。校验或写入失败保留表单并回滚新增记录，可修正后直接重试，不会留下空项目。生成的 Webhook Secret 在创建页提供一次复制入口，存储和后续 GET 均不返回明文；复制后点击「打开交付项目」继续。已有项目的「设置」复用同一配置表单。

验证命令：

```bash
GOCACHE=/tmp/suma-project-go-cache go -C server test ./internal/cd -run '^TestCreateConfiguredProject'
GOCACHE=/tmp/suma-project-go-cache go -C server test ./internal/api -run '^TestDelivery(FullConfigurationCreation|CreationFailureAndLegacy)HTTP$'
npm --prefix web run test:browser -- e2e/continuous-delivery-create.spec.ts
```
