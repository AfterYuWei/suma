# PostgreSQL 运行说明

SUMA 使用 PostgreSQL 18.6 保存应用配置、Task、审计和历史通知证据。Docker 保持当前资源状态的唯一来源。

## 初始化与开发

本地原生启动与测试使用项目根目录 `.env.local`，无需在终端 `export`。运行 `make local-config` 创建权限为 `0600` 的私密模板，已有文件保持不变。项目内置开发数据库使用 `127.0.0.1:55432/suma`，填写对应用户名与密码：

```dotenv
SUMA_DATABASE_DSN='postgres://YOUR_USER:YOUR_PASSWORD@127.0.0.1:55432/suma?sslmode=disable'
SUMA_TEST_DATABASE_DSN=${SUMA_DATABASE_DSN}
```

模板中的 `5432` 用于连接既有本机数据库；使用项目内置实例时改为上例的 `55432`。用户名与密码中的特殊字符需要 URL 编码；单引号保持 `$` 字面值。`make dev`、`make server-check`、原生 `go run` 和 `go test` 自动读取同一文件。每个调用独立读取，不修改进程环境或执行 shell 文本；已有环境变量优先。`SUMA_ENV_FILE` 可以指定文件，设置为 `-` 时只使用进程环境。指定文件不存在、不可读或格式错误时明确失败，错误不含原始配置内容。`.env.local` 不提交 Git，生产镜像也不复制该文件。

生产 Compose 使用 `.env`：复制 `.env.example`，填写至少 32 位随机十六进制 `SUMA_POSTGRES_PASSWORD`。内置 PostgreSQL 不发布端口，SUMA 通过 `postgres:5432/suma` 连接，数据卷挂载 `/var/lib/postgresql`。如果本机没有现有数据库，可使用 `make db-up`；开发覆盖文件仅绑定 `127.0.0.1:55432`，相应修改 `.env.local` 端口。

原生服务必须在配置文件或进程环境中提供完整 `SUMA_DATABASE_DSN`。远程数据库使用 `sslmode=verify-full` 和可信 CA；本机及 Compose 内部网络使用 `sslmode=disable`。缺少 DSN、连接失败或迁移失败时启动失败，错误不打印凭据。`SUMA_DATA_ROOT` 独立用于 Compose、Git 和加密密钥，不从数据库地址推导。

首次部署使用空库初始化。项目已于 2026-10-11 上线生产，之后升级必须沿用并保留现有数据库、应用文件和加密密钥。启动执行嵌入式 `internal/database/migrations/*.sql` 中尚未登记的文件，在事务与 advisory lock 下记录版本，不运行 AutoMigrate 或自动清库。

已发布的 `0001_initial.sql` 及后续迁移文件保持不变；新增结构或数据转换必须增加有序迁移，例如 `0002_<change>.sql`。空库按顺序执行全部迁移，已有库只执行新增文件。不得删除迁移登记、重新执行初始化基线，或通过删库重建完成生产升级。`cmd/schema` 是上线前的基线生成工具，生产阶段不得用它覆盖已发布基线。

测试必须在配置文件或进程环境中提供 `SUMA_TEST_DATABASE_DSN`，账户需要创建与删除 schema 的权限。开发环境可以使用本地 `suma` 库中的独立测试 schema；生产应使用独立测试库。`testutil.Open` 为每个测试创建随机 schema，测试结束清理；没有数据库时明确失败，不跳过或回退。

## 生产数据与升级

升级保留账号、节点、凭据、渠道、规则和订阅、站内事件、发送记录、Task、审计及其原标识。加密密钥随原应用文件保留；仅恢复数据库而丢失密钥无法解密对应凭据。清库、删除持久卷或重置密钥均需针对该次操作的明确授权，开发阶段的旧授权不延续为生产清库授权。

结构变更先准备数据转换、校验和失败恢复步骤，使用隔离 PostgreSQL 验证首次初始化与从上一支持版本升级。升级测试必须验证原标识、加密凭据、业务配置、待处理任务及队列和历史证据仍可读取；不能只以空库测试代替升级测试。多条旧配置存在冲突时应明确转换语义，历史没有保存的策略／名称／版本不可按当前配置伪造。

本次通知订阅重设计在上线前修改过同名初始化文件，旧库已登记 `0001_initial.sql` 时不会再次执行新版内容，因而出现结构不兼容。用户随后表示自行重建并重新处理本次数据，取消旧库定向迁移；没有连接或修改其生产库。当前上线版本作为后续兼容升级的起点，此后不再重复修改初始化基线。

## 本机一次性恢复（2026-10-11）

此前用户授权保留现有数据，例外完成过定向迁移：暂停原生后端写入，将原 PostgreSQL 17 的 `suma` 库完整导出并恢复到独立 PostgreSQL 18.6，转换已核对的单渠道／单规则通知配置。原规则的筛选条件保留，发送策略和目标绑定转换为“全部对话”订阅；已发送记录保留原状态并补齐命中规则证据和快照，不补发历史事件。

随后用户删除并重建原 `suma` 库，明确要求复用原数据库容器。当前原生服务和隔离测试通过 `.env.local` 连接原容器 `postgres` 的 `127.0.0.1:5432/suma`，使用当前全新基线初始化；未导入此前的数据。原容器镜像未变更，当前实际版本为 PostgreSQL 17.11，标准部署要求仍为 18.6。应用加密密钥和文件目录沿用原配置。重新创建的空库需要重新初始化管理员和配置通知。

新增容器 `dockport-pg18-postgres-1` 及专属网络已移除，`55432` 不再使用。为保留此前数据找回的可能性，未删除其数据卷 `dockport-pg18_suma-postgres`、完整导出和私密配置；日常启动只使用现有 `postgres` 容器，无需启动额外数据库。

原数据库容器 `postgres`、其 `5432` 端口及 `/Data/PostgreSQL/data` 均未由本次操作删除或替换。原连接配置位于 `.env.local.before-notification-v2`，切回前的连接配置位于 `.env.local.before-original-db`；完整导出和校验报告位于 `server/.codex/notification-recovery/`。凭据和导出文件权限为 `0600`，不提交 Git 或进入镜像构建上下文。本次切换不自动恢复备份，未添加启动升级、通用历史迁移或自动数据库清理。

## `/Data` 保存内容

| 路径 | 内容 |
| --- | --- |
| `/Data/secret.key` | 应用凭据和容器文件历史的加密主密钥；丢失后不能解密对应数据库内容 |
| `/Data/compose/` | 托管 Compose 的 YAML、环境文件、配置附件和 SUMA 项目元信息 |
| `/Data/gitops/` | CD 仓库缓存及各修订工作区 |
| `/Data/backups/` | 保留目录，目前没有自动备份任务 |

应用设置、用户、节点、通知、Task 和审计在 PostgreSQL 中；数据库连接信息来自启动配置。`make dev` 默认根目录为 `server/data/`，生产镜像默认 `/Data`。PostgreSQL 的数据目录由其独立部署决定：项目内置数据库使用 Docker 数据卷；用户自行部署的 `/Data/PostgreSQL/` 是该数据库部署的文件，不是 SUMA 自动生成的配置目录。旧本地 `suma.db`／`dockport.db` 如仍存在，仅是历史遗留文件，当前服务不会读取或自动迁移它们。

## 验证命令

```bash
make local-config
make check
cd web
npm test
npm run build:demo
npx playwright test e2e/notifications.spec.ts e2e/feishu-channel.spec.ts e2e/operations.spec.ts
```

在项目根目录运行 `bash doc/operations-smoke.sh`，通过一次性 Docker Engine 验证 Unix、mTLS TCP 和 HTTPS/WSS Agent 的 Project 配置、镜像更新、聚合日志和真实异常退出通知。测试服务的通知发送使用受控替身。若同一宿主机上另有运行中的 SUMA 实例，其原有监控与订阅仍可能通知烟测临时容器的退出；本次恢复后的实例产生了两条此类新事件，历史事件没有重发。脚本需要已缓存的 `docker:27-dind`、`alpine:3.24` 和 `suma-agent:env-only-smoke` 镜像及上述测试数据库配置。
