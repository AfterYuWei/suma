# 发布重设计：实施与升级说明

日期：2026-11-14。状态：远端 Go／Web 门禁、183 项 Web 单元测试、490 个唯一浏览器用例、发布并发检测、真实 CD Docker 业务及隔离数据库升级／恢复演练已通过；旧 Project 统计指标限制见验收记录。未部署生产；提交状态以 Git 历史为准。用户要求不本地测试，所有验证均在测试机执行，详见 [实际验收记录](cd-release-validation-2026-11-14.md)。

## 已编写的变更

- 全局持续交付与节点项目共用发布面板：准备、逐节点预览、确认、执行结果及日志。节点项目固定当前节点，创建后可以直接准备首次发布，保存和预览不改变容器。
- 准备记录固定 Commit、配置、加密变量、目标 Engine 身份及每个服务的平台镜像 digest。ComposeRunner 使用短期私有镜像／平台覆盖文件执行，不修改 Git。Git 不变时仍可检查标签更新并产生独立发布记录。
- 十分钟有效期、配置版本和前序成功记录校验；方案认领与 Task 创建在同一事务内完成。已确认方案重送返回原 Task；不因网络重送重复部署。
- 失败节点重试沿用原输入，原失败保留；失败节点恢复逐个选择其前序成功快照。恢复切换为人工确认，周期和 Webhook 可继续检查。历史缺少固定镜像证据只允许显式重新应用旧配置，不声称恢复原镜像。
- 列表区分 Docker 运行、最近操作和自动化；检查记录默认不挤占项目发布历史。人工检查始终只读，自动触发继续复用 Task Center。

主要实现位于 `server/internal/{deployment,cd,projectcd}/release_plans.go`、`deployment/frozen_images.go`、`compose/image_overrides.go`、`api/release_plans.go` 和 `web/src/features/delivery/release-flow.tsx`。产品契约见 [需求与设计](cd-release-redesign-2026-11-14.md)，接口见根目录 `API.md`。

## 0013 增量迁移

`0013_release_plans.sql` 通过现有迁移事务、advisory lock 和台账运行，不改写 0001–0012。

| 位置 | 新增数据及旧数据处理 |
| --- | --- |
| `release_plans` | 所属类型／ID、加密输入、脱敏预览、准备 Task、确认 Task、业务记录关联及有效期。 |
| `delivery_records` | `frozen_input_ciphertext`、`images_pinned`；旧记录后者为 false，不依据当前标签补造历史镜像。 |
| `project_cd_records` | `images_pinned`，旧记录为 false，保留原加密快照。 |
| `delivery_projects` | `initial_application_required`；旧项目为 false，保持已有自动化行为；应用中新建或变更来源范围的项目要求首次人工确认。 |

不转换旧身份，不删除用户、节点、凭据、设置、任务／审计／通知历史、队列证据、工作树或应用密钥。旧记录仍可读取，恢复资格明确区分完整配置与固定镜像证据。此迁移已在远端隔离测试 schema 执行，未在业务库或生产执行。

升级前停止新的自动派发，完成或取消正在执行的发布，保留可恢复的数据库、应用文件及原密钥一致副本。随后使用新程序迁移；启动后先检查原项目、节点、凭据授权、自动化及历史关联，再恢复测试环境自动触发。不要把生产数据复制到临时 schema 后输出敏感字段。

以下只读查询不返回变量或凭据，可用于核对旧数据及台账：

```sql
SELECT key FROM schema_migrations ORDER BY key;
SELECT count(*) FROM release_plans;
SELECT images_pinned, count(*) FROM delivery_records GROUP BY images_pinned;
SELECT images_pinned, count(*) FROM project_cd_records GROUP BY images_pinned;
SELECT id, name, initial_application_required FROM delivery_projects ORDER BY id;
```

迁移失败由事务回滚。新程序产生方案或固定镜像记录后，不能让旧程序继续交付：旧程序不识别固定镜像覆盖及首次保护。需要程序回退时先停自动触发和控制平面，使用升级前完整数据库／文件状态与原密钥恢复；保留历史备份，不删除迁移台账、不重置生产库、不替换密钥。恢复版本只恢复应用配置和镜像，保留数据卷，不回退数据库业务内容。

## 本次验收环境

用户已提供 CNB 测试机并授权 Chromium 和指定镜像下载；复用既有 Go／Node／PostgreSQL，Agent 从当前源码构建。实际环境与测试证据见验收记录。只操作随机 schema、临时项目和有明确标签的容器，未改业务数据或生产。

## 验收清单

下列验收已在远端完成。实际命令和边界以验收记录为准；不把旧 Project 统计失败或未单独验证的场景计为通过。

- [x] 空库初始化、从 0012 保留数据升级和再次启动幂等；保留 ID、凭据密文、设置、历史与待发送通知，旧镜像标记为 false。
- [x] 服务／HTTP：方案归属、脱敏与授权、标签移动后的 digest 固定、配置及有效期冲突、重复确认、首次自动发布保护。
- [x] 部分失败：服务测试及真实两 Engine 验证仅失败节点重试、原输入保留和逐节点前序恢复；原失败在自动恢复后可重试及旧快照限制由服务回归验证。
- [x] Web 门禁及六套浏览器回归：双入口、中英文、深浅主题、320／390／1440px、菜单几何、草稿／秘密／后台任务、恢复与退出 CD；失败夹具修正后相关用例复验通过。
- [x] 独立真实 Docker：三传输准备无副作用、固定镜像执行、确认重放、健康、自动恢复、退出 CD 及父子任务；另验证真实多节点部分失败、重试、逐节点恢复和重复恢复重建。无健康检查语义和取消继续复用 Task／运行态服务门禁，未把真实拉取中取消计为本轮 Docker 实测。
- [x] 隔离测试数据升级与 pg_dump／pg_restore 一致恢复演练；原 ID、通知、密钥解密和应用文件保留。生产发布仍需独立核对实际备份与运行环境。

在测试机的 `server` 目录执行 `go test ./...`、`go build ./...`。测试数据库使用私有 `.env.local` 或进程环境的 `SUMA_TEST_DATABASE_DSN`，不得用 shell 求值加载秘密文件；现有测试工具为各测试分配隔离 schema。

在测试机的 `web` 目录执行 `npm run lint`、`npm run typecheck`、`npm run build`、`npm test`，并按既有配置运行主站、项目服务、容器、任务中心和任务中心 Demo 的完整 Playwright 回归。使用测试机已准备的浏览器，不自动下载。

真实 Docker 用例以 `dockersmoke` 构建标签和现有 `SUMA_RUN_DOCKER_SMOKE=1` 门禁启用；Unix／mTLS 双节点配置沿用 `SUMA_SMOKE_TCP_HOST`、`SUMA_SMOKE_TLS_DIR`，三传输项目 CD 沿用现有 Project Docker 冒烟夹具。只有指向测试引擎并确认临时命名空间后才执行。具体镜像、连接和执行范围以用户提供的测试环境为准。

实际命令、截图与限制已记录到验收文档，PLANS.md 已按此次发布功能验收结果勾选；未修改旧 Project 指标断言，未部署生产。
