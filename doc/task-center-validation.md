# 统一任务中心：验证记录

日期：2026-11-13。对象：基于 `bea8204` 的任务中心统一改动；验收在提交前的 `feat/task-center` 工作区执行，没有部署生产。需求不变，以 [最终设计](task-center-final-design.md) 为准，完成状态以 [PLANS.md](../PLANS.md) 为准。

## 已执行

| 检查 | 结果及边界 |
| --- | --- |
| Web lint、typecheck | 通过；保留原有 4 项共享 UI lint 警告。 |
| Web 完整单测 | 196 项通过，包括真实 Demo API 的四类规则、双入口版本、清理授权、候选检查、发布记录、父子关联、普通长操作和不存在 Task 的 404。 |
| Web production／Demo build | 均通过；保留原有滚动条 CSS 和 chunk 大小警告。 |
| Linux 后端构建 | `GOOS=linux GOARCH=amd64 go build ./...` 通过。 |
| 所有 Go 测试编译 | `GOOS=linux GOARCH=amd64 go test -exec /usr/bin/true ./...` 通过。这只编译，不执行用例。 |
| 无数据库 Go 定向测试及 race | Task 参数／摘要 SQL、Cron 时区与秒级预览、停止门禁、镜像引用及完整 JSON 脱敏用例实际通过，相关 `-race` 通过。不代表完整服务并发验收。 |
| PostgreSQL 历史查询及升级测试 | 在新测试机的 PostgreSQL 18.6 随机隔离 schema 下实际通过，见下方 Linux 验收记录。 |

任务中心 17 项 Edge 专项与真实 Demo API 3 项最终复验全部通过，覆盖中英文、深浅主题、390／1440px、Select 实际菜单几何、三视图范围、301 条分页、搜索／来源／归档规则筛选、409 草稿、触发快照按需读取、父子深链和原业务结果。新增 Demo 流程验证候选检查后发布仍保留原检查的 Release ID、发布的原业务记录 ID 和节点子任务。Demo 状态在模块内存中，页面重载会重新初始化示例；生产深链持久性仍需后端数据库验收。

四套既有浏览器完整首轮共 468 项：443 通过、25 失败、0 跳过。主站旧任务入口／历史断言与通知预设已修正，完整主站最终 179 项通过；项目服务 153 项、概览 49 项全部通过。容器模块的 7 项失败在干净 HEAD 对照中均能复现，随后逐项修复并重新运行完整容器套件，87 项全部通过（6.1 分钟）：

- 图表悬停用同一批刷新后的数据比较，保留曲线像素／宽度断言与空闲不重绘断言。
- 历史过期用例固定浏览器时钟；资源性能条目缓冲区扩为 2000，保留长名称 ECharts 提示内容断言。
- 五位端口映射的第二行标签适当压缩内边距并约束宽度，同时断言完整端口文字可见。

最终四套既有浏览器共 **468 项通过**，加任务中心专项／Demo **20 项通过**。使用本机已安装 Edge，没有下载本地浏览器或工具二进制。完整主站输出为 `/private/tmp/suma-task-center-main-final.log`，容器最终输出为 `/private/tmp/suma-container-gate-full.log`；早期失败／干净快照对照只用于定位问题，不作最终通过证据。临时浏览器配置已移除。

## Linux／PostgreSQL 实际验收

用户已授权在其提供的 CNB 测试机下载本次所需镜像、构建 Agent，并同步明确范围的源码与后续修复。测试目录为 `/tmp/suma-task-center.ymxcl64t`；没有同步本地 `.env`、凭据、真实私钥或业务数据。

- Go 1.26.11、PostgreSQL 18.6；数据库不发布端口，凭据随机生成并只保存在测试机私密配置中。全部测试仍创建和删除各自的随机 schema。
- **完整 `go test ./...` 与 `go build ./...` 实际通过**，已经覆盖原先无法执行的数据库、服务和 HTTP 断言。首轮发现并修正两处必填 JSON 夹具、一处未计入 Raw/Scan 的查询计数，以及一处缺 JSON Content-Type 的鉴权请求；未放宽业务约束。
- **12 个相关包的 race 实际通过**：notification、database、app、task、taskcenter、cleanup、cd、imageupdate、redact、compose、projectcd、api。新增转换冲突保护后，taskcenter 及调用方再次通过相关 race，完整 Go 门禁也再次通过。并发扫描／人工运行只启动一个 Task，以及认领后暂停阻止自动业务工作但保留人工运行，两项新增回归各重复 5 轮 race 通过。
- 四类规则的真实业务转换、关闭状态、旧新周期及 Webhook 对照、缺授权／缺执行时间停摆、原密钥解密、原文件与历史保留、账本防重、冲突整体回滚通过。增加只读 [升级核对 SQL](task-center-upgrade-check.sql) 与 [切换／恢复说明](task-center-upgrade.md)。
- **真实备份恢复演练通过**：专用 PostgreSQL 容器对随机测试 schema 执行 pg_dump／pg_restore，测试原密钥与 Compose 文件经 tar 归档后移除、恢复，再运行相同升级检查。两次核对均为 8 条规则、0 项配置不一致、2 条按预期停摆；未伪造旧快照或清理授权。

日志已归档在测试机 `/tmp/suma-task-center.ymxcl64t/artifacts/logs/`：`go-test-final.log`、`go-build-final.log`、`go-race.log`、`go-race-final.log`、`business-upgrade.log`。这些是本次测试机的实际结果，不代表生产数据库、文件或备份已经检查或改变。

## 验收状态

- [x] PostgreSQL 18.6 下完整 Go test／build 和相关业务 race，包括查询、调度、双入口冲突及升级证据。
- [x] 既有浏览器失败收敛，最终四套 468 项通过，任务中心／Demo 另有 20 项通过。
- [x] Unix／mTLS TCP／HTTPS-WSS Agent 的隔离真实 Docker 四类自动化、连续镜像更新和取消验收。
- [x] 隔离升级／备份恢复演练、转换清单与停摆核对；兼容修复版本恢复边界已明确。生产切换前仍需核对生产备份位置与可恢复性，当前未执行生产切换。

macOS 本机仍无 Docker CLI，原生完整后端门禁受现有 Linux-only monitor 实现限制；完整后端验收已经使用上述 Linux 环境。生产数据、已发布迁移及生产应用密钥均未修改。真实 Docker 缺口按以下记录单独说明。


## 真实 Docker 结果与边界

测试机为 rootless Docker 29.9.2、cgroup v1 且没有 cgroup 委派。隔离 Engine 使用 Docker 27.5.1 的 rootless／vfs 模式，测试目录与 runner 共享绝对路径；仅在一次性引擎中去掉宿主能力边界不支持的 NET_RAW，未修改宿主 Docker 或生产行为。Registry 使用受信私有 CA 的真实 HTTPS，Agent 使用校验服务端证书的 HTTPS／WSS；没有关闭 TLS 校验。测试 Agent 使用当前服务器源码及原 Dockerfile.agent 构建。

- **三传输任务中心专项全部通过（301.9 秒）**：每种传输分别运行 image_update、project_cd、global_cd 子用例，共 9 项业务用例。实际完成镜像 D1→D2→D3 后继续跟踪原标签；拉取和重建等待阶段取消均等待子 Task 退出，并保留已改变的 Engine 状态。两套 CD 均验证人工检查只生成候选、明确发布、真实容器重建、30 秒规则通过中心派发、版本不随 tick 漂移、原业务 ID 及父子 Task 追溯。
- **三传输真实清理全部通过（最终补验 47.9 秒）**：保护资源、活动／停止引用、冻结预览及重新检查、卷名确认、原清理记录、审计和统一触发／Task 关联均通过。最终每种传输均真实回收 4,194,355 字节 BuildKit 缓存；缓存由 COPY 真实数据生成，不依赖构建时运行容器命令。最终补验同时覆盖未授权启用被拒绝、明确授权后由中心定时触发、真实删除新建停止容器并保留运行中容器、授权与原运行证据保留、同一到期不重放，以及配置版本不随 tick 改变。
- 冒烟网关从实际 Docker bridge 获取，证书包括相应地址；镜像配置带独立夹具标识，避免前一个传输的缓存使拉取取消断言失效。
- **旧 Project 弹窗完整冒烟尚未通过**：该用例要求非零容器指标，测试机无 cgroup 统计，因此三种传输均返回指标不可用。原断言完整保留，已与任务中心业务分为独立子用例；没有用零值、mock 或跳过断言伪造通过。上述 9 项任务中心用例与清理测试均实际执行并成功。原完整 Project 冒烟仍保留在 CI 的标准 Linux Docker 环境中，本次未运行远程 CI。

真实 Docker 日志为 `task-center-transports.log`、`cleanup-smoke.log`、最终补验 `cleanup-scheduled-smoke.log`；旧完整 Project 失败证据为 `operations-smoke.log`。测试只操作新建的独立 Engine 和夹具 Agent，不执行宿主机全局 prune，不访问生产业务资源。


## 验收源码与证据保存

服务器源码 337 个文件的聚合 SHA-256 为 `76ccab7f0bc8b1909002a9cac9352f74df08a141fd8839071b63a34895d9826f`，本地最终源码与实际测试 runner 一致。指纹按排序后的相对路径、NUL 分隔符及每个文件的 SHA-256 汇总，不包含配置文件或凭据。

原始日志已归档到测试机 `/tmp/suma-task-center.ymxcl64t/artifacts/logs/`，同级包含源码指纹与临时 rootless 运行时适配脚本。批量下载这些未经逐项审查的日志被自动审批拒绝，原因是可能含敏感信息；没有改用其他方式导出原始日志。测试机归档保留在私有目录，本地仅记录已核验的非敏感结论。

验收结束后，已核对随机测试 schema 为零，并移除全部本次拥有明确标签的容器、临时数据库凭据、TLS／Agent 夹具及上传源码；测试机私有证据归档与已授权镜像缓存保留，没有执行宿主全局 prune。
