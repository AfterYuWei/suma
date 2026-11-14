# 发布重设计：远端验收记录

日期：2026-11-14。业务代码已完成，远端 Go／Web 门禁、浏览器回归、数据库升级／恢复以及真实 Docker 发布业务验收已通过。未在本地运行测试或构建，未部署生产；提交与推送状态以 Git 历史为准。

## 范围与环境

代码在用户提供的 CNB 测试机 `/tmp/suma-release-20261114-review` 验收，没有覆盖 `/workspace` 源码。本地源码归档排除了私有环境文件和私钥，数据库配置直接引用测试机已有配置；测试只使用随机隔离 schema 和临时项目。

环境为 Go 1.26.10、PostgreSQL 18.6、Node v26.13.0、npm 11.20.0、Chromium 153、宿主 rootless Docker 29.9.2、独立 Docker 27 rootless/vfs 测试引擎及 Compose 5.6.1。用户明确授权下载 Chromium 和 docker:27-dind／alpine:3.24／nginx:alpine；Agent 由当前源码构建。

## Go、Web 和数据库

| 验证 | 实际结果 |
| --- | --- |
| 完整 Go | `go test -p 2 ./... -count=1 -timeout=15m`、`go build -p 2 ./...` 通过。 |
| 发布并发检测 | deployment／cd／projectcd 的 ReleasePlan、Prepared、FirstRelease 相关 `-race` 用例通过。 |
| Web 门禁 | lint、typecheck、production build、Demo build 通过，最终镜像详情调整后已复验；仍有原共享组件 4 项 lint 警告及大 chunk 提示。 |
| Web 单元测试 | 16 个文件、183 项通过。 |
| 增量迁移 | 空库、0012→0013、既有升级测试通过；保留 ID、密文、设置、任务及历史／待发送通知，旧记录不补造 digest。 |
| 实际备份恢复 | 合成数据的随机 schema 完成 0012 备份、0013 升级及新增证据、pg_dump／pg_restore 回到旧基线、再次升级。原 ID／通知保留，原密钥仍可解密凭据，应用文件不变，旧基线恢复后不出现新证据。 |

Git 测试创建临时提交时，测试机全局 CNB 签名器缺少签名环境；仅在测试子进程设置 `commit.gpgsign=false` 后，全门禁通过。未改用户 Git 配置或创建项目提交。

## 完整浏览器回归与视觉检查

六套共 **490 个唯一用例均有通过结果**。下表记录完整首跑和相关修复后的复验，不能把有失败的首跑说成一次全绿。

| 套件 | 结果 |
| --- | --- |
| 主站 | 180 项完整重跑通过。 |
| 项目服务 | 完整执行 154 项，首跑 153 项通过；历史页旧 Records 标签修正后，13 项历史回归全部通过。 |
| 容器 | 87 项全部通过。 |
| 概览 | 49 项全部通过。 |
| 任务中心 | 17 项完整复验通过。 |
| 任务中心 Demo | 3 项全部通过。 |

发布专项覆盖双入口、首次预览不部署、同 Commit 镜像更新、按快照重试、部分节点恢复、收起／重开同一任务、无变化显式重建、方案冲突／过期、子集确认、秘密隐藏及配置草稿。最终公共面板 48 项中，34 项通过后修正新增断言的相对定位，再完成其余 14 项；功能断言没有删除或跳过。

已查看真实浏览器生成的中英文、深浅主题、320／390／1440px 页面截图及既有菜单几何断言。视觉检查发现完整 digest 挤占窄屏，现默认短标识、展开读取完整镜像身份，展开／收起和发布确认均通过复验。截图使用可控测试数据，不是生产截图或可用性计时实验。

## 独立真实 Docker

- Unix、mTLS TCP、HTTPS/WSS Agent 各执行项目 CD 和全局 CD，共 6 项业务子用例通过。实际完成准备不改容器、固定 digest 发布、确认重放返回原 Task、节点子任务和原候选证据、周期后续发布、项目失败自动恢复及切回手动时容器不变。
- 两个独立 Engine 的 `TestRealDockerMultiNodeConsistency` 通过：真实成功节点执行，受控失败节点保留失败；仅失败节点重试，随后逐节点恢复各自前序版本，成功节点不重做。
- `TestRealDockerCDDeliveryRollback` 通过：目标 Engine 主机上的实际只读绑定文件、固定版本恢复及重复恢复确实重建容器。测试可用 `SUMA_SMOKE_UNIX_HOST` 指向隔离 Unix 引擎，`SUMA_SMOKE_BIND_SOURCE` 指向目标主机上已有的公开夹具文件。

两个引擎身份独立，没有把同一 Engine 当作两个节点。Agent 是真实当前源码进程，通过验证服务端证书的 HTTPS/WSS 工作；没有关闭 TLS 校验。Git 拉取来源受控，Compose、registry digest 查询、容器重建、健康和传输均实际执行。

测试机不支持标准嵌套容器的 sysfs／NET_RAW／cgroup 条件。仅在一次性引擎中使用只读 sysfs 绑定、rootless/vfs，并在创建及健康检查 exec 的 OCI 参数中去掉宿主明确缺失的 NET_RAW；没有改产品或宿主 Docker。原测试镜像内 Compose 2.33 不支持既有导出参数，测试容器改用测试机已有 Compose 5.6.1。只读绑定夹具放到实际目标 Engine 文件系统，没有假定控制平面与 Docker 主机共享文件。

**保留的限制：旧完整 Project 统计冒烟未通过。** 原 `project_configuration` 子用例要求非零 cgroup 指标，测试机实际返回指标不可用；三传输均保留失败，未改断言、填零或 mock。其所在的完整旧冒烟整体失败；本次发布业务的 6 项子用例已全部通过，并另以限定业务范围的命令独立执行，180.46 秒整体通过。生产和标准 cgroup 环境的完整 Project 统计验收不由本次结果代替。

## 源码一致性与产物

当前本地源码与远端验收源码的 605 个文件逐项匹配，聚合 SHA-256：

`0d30336ff15b5ea9b8061fc0d9bfebe59d5c73d6bba0d096cef70391d1abfef6`

证据位于远端测试目录的 `validation/`。关键日志为 `go-full-test-env`、`go-release-race`、`web-final-*`、`browser-main-updated`、`browser-project`、`browser-history-fixed`、`browser-release-final`、`browser-release-digest-final`、`docker-release-transports-final`、`docker-cd-final`、`database-recovery-drill` 及 `source-parity.json`。保留旧 Project 指标失败证据 `docker-transports-current-cli.log`。

最终清理结果见 cleanup.json：隔离 schema 为 0，两个 Engine 和 Runner 共 3 个容器已移除，本次标签容器剩余 0；私有测试转发已停止，临时凭据／证书已移除，没有执行全局 prune。用户授权的镜像和浏览器缓存保留。没有修改业务 schema、原应用密钥或生产资源。

真实拉取中的取消未作为本轮单独 Docker 实测；本次复用既有 Task 取消实现并通过完整服务门禁。未进行真实用户访谈、生产压测或生产部署，不据演示和测试数据宣称业务效率提升。

已核验的最终日志归档已取回本地 `/tmp/suma-release-20261114-final-validation.tar`，包含成功门禁、相关复验、旧统计失败及清理证据，不含私有配置、密钥或证书。最终中英文深浅主题截图位于 `/tmp/suma-release-final-preview/`。
