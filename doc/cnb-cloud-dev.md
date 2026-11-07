# CNB 云原生开发环境（单容器模式）

项目通过 [cnb.cool](https://cnb.cool) 提供云原生开发环境：WebIDE、VS Code、Cursor 均可接入。开发环境为官方推荐的**单容器模式**——开发工具链与 code-server 运行在同一容器中（镜像内已预装 code-server，避免插件功能受限的双容器降级）。

## 配置文件

- `.cnb.yml`：云原生开发启动流水线。`$:` 通配所有分支，`vscode` 事件在打开开发环境时执行 stages。
- `.ide/Dockerfile`：自定义开发环境镜像，`docker.image` 指定 `cnbcool/default-dev-env:latest` 作为构建失败时的回退镜像。

## 预装内容

| 组件 | 版本 / 来源 | 用途 |
| --- | --- | --- |
| Go | 1.26（`golang:1.26-trixie` 基础镜像，GOPROXY 已指向 goproxy.cn） | `server/` 后端 |
| Node.js | 26（NodeSource） | `web/` 前端（Vite 8 / React 19） |
| PostgreSQL | 18（PGDG apt 源） | 本地开发与 `go test` 测试库 |
| code-server | 官方安装脚本 | 单容器模式 WebIDE |
| Docker CLI | docker-ce-cli + Compose 插件 | 守护进程由 CNB `docker` 服务提供 |

## 启动流程

打开任意分支的云原生开发环境时，`.cnb.yml` 的 stages 依次执行：

1. 启动容器内 PostgreSQL 18，幂等创建 `suma` 角色（密码 `suma`）与 `suma` 数据库。
2. 生成根目录 `.env.local`（已被 gitignore，权限 0600），写入 `SUMA_DATABASE_DSN` 与 `SUMA_TEST_DATABASE_DSN`，指向 `127.0.0.1:5432`。原生 Go 命令与测试会自动读取该文件。
3. `web/` 安装 npm 依赖（`node_modules` 已存在时跳过），`server/` 预下载 Go 模块。

## 常用命令

```sh
make dev      # 同时启动前后端开发服务（前端 5173，API 8081）
make check    # 前端 lint/typecheck/build + 后端 go test/build
make build    # 构建前端生产资源与后端二进制
make help     # 查看全部命令
```

测试数据库要求与本地开发一致，见 `.env.local.example` 与 `doc/postgresql.md`。
