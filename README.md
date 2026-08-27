# SUMA

**语言：[简体中文](README.md) | [English](doc/README.en.md)**

SUMA 是一个面向多节点 Docker 管理的单体控制平面：通过一个 Web 界面统一管理多个 Docker 引擎，无需在服务器上部署任何 Agent，也不依赖 SSH 登录或 Swarm/Kubernetes。适合个人服务器、HomeLab、NAS、VPS 与小型团队。

## 功能特性

### 多节点引擎接入

- 无 Agent 接入：挂载 Unix socket 直连本机/宿主机引擎，或添加远程 Docker TCP 端点
- TCP 默认强制双向 TLS（mTLS），明文 TCP 仅允许回环地址；TLS 凭据加密存储并按节点授权
- 全局节点选择器：Header 一键切换当前操作节点，资源、Compose、任务全部跟随
- 自动状态探测：每 30 秒探测所有节点在线状态与延迟，异常自动降级显示
- 远端 bind 目录白名单：TCP 节点上的 Compose 挂载源必须在节点允许列表内

### Fleet 总览

- 控制平面层：全局指标卡片、按节点的容器/镜像/CPU/内存汇总、CD 项目发布与漂移状态
- 单节点层：主机资源使用、容器明细、引擎版本与在线时长，点击行即切换到该节点

### 容器与资源管理

- 密度列表 + 行内快捷操作：启动/停止/重启/删除均带破坏性确认
- 容器详情：Inspect（敏感环境变量自动掩码）、实时日志流、xterm.js 可交互终端（支持 resize）、ECharts 实时统计
- 镜像：拉取进度流、标签、删除；支持私有 Registry 凭据认证
- 网络 / 存储卷：完整生命周期；卷删除前检查占用并在用时不允许删除

### Compose 项目

- 本地 Compose 项目：compose.yml 与 .env 在 Monaco 编辑器中编辑，保存前校验
- Git 来源只读展示：交付过来的 Compose 文件不可篡改，与 CD 域隔离
- 批量操作：多选后一次性 start/stop/restart/up/down，逐项目汇报结果
- 展开即看运行态：服务列表、容器状态、单容器日志与终端入口

### 持续交付（CD）

- 对接任意 HTTPS/SSH Git 仓库（GitLab、GitHub 或自建），凭据 AES-GCM 加密存储
- Webhook 自动触发：兼容 GitHub / GitLab 推送头，通用签名兜底；也可定时轮询同步
- 每次交付是一个不可变 Release：精确 commit + 渲染后的 Compose 配置指纹，全程可审计
- 交付模式：观察 / 手动审批 / 自动；支持多节点并行部署与仅失败节点回滚
- 部署健康门禁与漂移检测，自动模式下可与仓库期望状态对账

### 认证中心

- 统一管理 Git 凭据（token / basic / SSH deploy key）、Registry 凭据、Docker TLS 材料与自定义 CA
- 凭据默认拒绝所有节点访问，必须显式授权给项目或节点；使用中的凭据不可删除

### 运维与安全

- 任务中心：长耗时操作（拉取、prune、部署等）持久化为任务，WebSocket 实时推送进度与日志
- 审计日志：关键变更操作全部留痕（操作者、动作、对象、时间）
- 系统清理：磁盘空间预估 + 二次确认
- 首次运行引导创建管理员；bcrypt 密码哈希、HttpOnly SameSite 会话 Cookie
- 中英双语界面、深色/浅色/跟随系统三档主题、`Ctrl/Cmd+K` 全局命令面板

## 快速开始

前置要求：一台装有 Docker Engine 的 Linux 主机；如需管理其它节点的引擎，见下方「接入更多节点」。

### 方式一：Docker Compose（推荐）

将以下内容保存为 `docker-compose.yml`：

```yaml
services:
  suma:
    image: ghcr.io/afteryuwei/suma:stable # 固定版本请改用具体 tag，如 :0.1.0
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      # 容器内数据根目录固定为 /Data（镜像内置 SUMA_DATA_ROOT=/Data），无需显式设置环境变量。
      # 挂载两侧保持同名路径，保证交付项目的相对 bind mount 在宿主机 daemon 可解析。
      - /Data:/Data
      - /var/run/docker.sock:/var/run/docker.sock
```

启动：

```bash
mkdir -p /Data && docker compose up -d
```

如需把数据放到其它磁盘：将 `/Data` 做成指向目标分区的符号链接，或直接把该分区挂载到 `/Data`；容器内外路径保持 `/Data` 不变。

### 方式二：docker run

```bash
mkdir -p /Data

docker run -d --name suma \
  --restart unless-stopped \
  -p 8080:8080 \
  -v /Data:/Data \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/afteryuwei/suma:v0.1.0
```

### 首次使用

浏览器打开 `http://<主机IP>:8080`，创建管理员账号并登录即可。

**常用环境变量**

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SUMA_DATA_ROOT` | `/Data`（生产镜像内置；裸机运行默认 `./data`） | 数据与凭据根目录，其余路径默认派生 |
| `SUMA_ADDRESS` | `:8080` | 服务监听地址（改端口需同步映射宿主端口） |
| `SUMA_COOKIE_SECURE` | `false` | HTTPS 部署时设为 `true` |
| `SUMA_DOCKER_HOST` | `unix:///var/run/docker.sock` | 首次引导默认节点的引擎地址 |

**镜像标签约定**

| 标签 | 说明 |
| --- | --- |
| `0.1.0`、`v0.1.0` | 由 git tag 构建的正式版本，永久保留 |
| `stable` | 跟随最新正式版，自动覆盖更新 |
| `abc1234`（短 commit） | main 分支每次提交构建的预览版，永久保留 |
| `pre` | 跟随最新预览版，自动覆盖更新 |

### 从源码构建

```bash
git clone https://github.com/AfterYuWei/suma.git
cd suma
make install       # 安装前后端依赖
make dev           # 本地开发模式（前端 5173 / 后端 8081）
make docker-up     # 构建并后台启动生产容器
```

质量检查：`make check`（= 后端 `go test ./...` + `go build ./...`，前端 lint/typecheck/build）。

## 接入更多节点

1. 进入「节点」页面，点击添加节点：
   - **Unix Socket**：把目标机的 `/var/run/docker.sock` 挂载进 SUMA 容器的某个路径后填入该路径；
   - **Docker TCP**：填写远端端点，例如 `tcp://192.168.1.99:2376`，选择 mTLS 并绑定 Docker TLS 凭据。
2. 点击「测试连接」验证连通性与延迟。
3. 为 TCP 节点配置 bind 目录白名单，约束其 Compose 项目能挂载的宿主目录范围。

> 重要安全提醒：永远不要在网络上暴露无认证的 Docker API（明文 2375）。远程接入一律使用 mTLS。公网部署请务必置于 HTTPS 反向代理之后，并通过设置开启 `SUMA_COOKIE_SECURE=true`。

## 数据与备份

- `/Data` 目录包含 SQLite 数据库、本地 Compose 项目、Delivery Project 的 Git 工作区与凭据加密密钥 `secret.key`
- 升级或迁移前先备份整个数据目录；`secret.key` 丢失将导致已存凭据无法解密

## 文档

- [PLANS.md](PLANS.md)：功能进度与上线验证记录
- [ARCHITECTURE.md](ARCHITECTURE.md)：架构说明
- [API.md](API.md)：REST / WebSocket API 参考
- [CD-DESIGN.md](CD-DESIGN.md)：持续交付设计模型
