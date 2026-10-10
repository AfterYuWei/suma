# PostgreSQL 运行说明

SUMA 使用 PostgreSQL 18.6 保存应用配置、Task、审计和历史通知证据。Docker 保持当前资源状态的唯一来源。

## 初始化与开发

本地原生启动与测试使用项目根目录 `.env.local`，无需在终端 `export`。运行 `make local-config` 创建权限为 `0600` 的私密模板，已有文件保持不变。当前本地数据库使用 `127.0.0.1:5432/suma`，填写对应用户名与密码：

```dotenv
SUMA_DATABASE_DSN='postgres://YOUR_USER:YOUR_PASSWORD@127.0.0.1:5432/suma?sslmode=disable'
SUMA_TEST_DATABASE_DSN=${SUMA_DATABASE_DSN}
```

用户名与密码中的特殊字符需要 URL 编码；单引号保持 `$` 字面值。`make dev`、`make server-check`、原生 `go run` 和 `go test` 自动读取同一文件。每个调用独立读取，不修改进程环境或执行 shell 文本；已有环境变量优先。`SUMA_ENV_FILE` 可以指定文件，设置为 `-` 时只使用进程环境。指定文件不存在、不可读或格式错误时明确失败，错误不含原始配置内容。`.env.local` 不提交 Git，生产镜像也不复制该文件。

生产 Compose 使用 `.env`：复制 `.env.example`，填写至少 32 位随机十六进制 `SUMA_POSTGRES_PASSWORD`。内置 PostgreSQL 不发布端口，SUMA 通过 `postgres:5432/suma` 连接，数据卷挂载 `/var/lib/postgresql`。如果本机没有现有数据库，可使用 `make db-up`；开发覆盖文件仅绑定 `127.0.0.1:55432`，相应修改 `.env.local` 端口。

原生服务必须在配置文件或进程环境中提供完整 `SUMA_DATABASE_DSN`。远程数据库使用 `sslmode=verify-full` 和可信 CA；本机及 Compose 内部网络使用 `sslmode=disable`。缺少 DSN、连接失败或迁移失败时启动失败，错误不打印凭据。`SUMA_DATA_ROOT` 独立用于 Compose、Git 和加密密钥，不从数据库地址推导。

使用空库初始化，不迁移旧开发数据。启动仅执行嵌入式 `internal/database/migrations/*.sql`，在事务与 advisory lock 下登记版本；不运行 AutoMigrate 或数据回填。`cmd/schema` 是开发阶段的基线生成工具。当前版本按全新数据库验收，不提供旧开发数据库升级或历史数据迁移，不自动清空现有数据库。

测试必须在配置文件或进程环境中提供 `SUMA_TEST_DATABASE_DSN`，账户需要创建与删除 schema 的权限。开发环境可以使用本地 `suma` 库中的独立测试 schema；生产应使用独立测试库。`testutil.Open` 为每个测试创建随机 schema，测试结束清理；没有数据库时明确失败，不跳过或回退。

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

在项目根目录运行 `bash doc/operations-smoke.sh`，通过一次性 Docker Engine 验证 Unix、mTLS TCP 和 HTTPS/WSS Agent 的 Project 配置、镜像更新、聚合日志和真实异常退出通知。通知发送使用受控替身，不投递到真实用户。脚本需要已缓存的 `docker:27-dind`、`alpine:3.24` 和 `suma-agent:env-only-smoke` 镜像及上述测试数据库配置。
