# SUMA

**语言：[简体中文](README.md) | [English](doc/README.en.md)**

SUMA 是一个面向多节点 Docker 管理的单体控制平面：通过一个 Web 界面统一管理多个 Docker 引擎。可直接连接 Docker，也可在远端部署主动回连的 Agent；不依赖 SSH 登录或 Swarm/Kubernetes。适合个人服务器、HomeLab、NAS、VPS 与小型团队。

## 功能特性

### 多节点引擎接入

- 三种节点接入：挂载 Unix socket、直连 Docker TCP，或部署主动通过 HTTPS/WSS 回连的 Agent
- TCP 默认强制双向 TLS（mTLS）；明文 TCP 仅允许回环、私有内网或 Tailscale IP，且保存时必须再次输入目标 IP 确认风险
- 全局节点选择器：Header 一键切换当前操作节点，资源、Projects、任务全部跟随
- 多归属节点 Group：节点可加入多个 Group 或不属于任何 Group；Group 统一筛选概览和节点候选，但只有显式选择节点才会切换 Docker 上下文
- 自动状态探测：每 30 秒探测所有节点在线状态与延迟，异常自动降级显示
- 远端 bind 安全校验：TCP 和 Agent 节点上的 Compose 挂载源必须使用不可插值的绝对路径

### Fleet 总览

- 控制平面层：全局指标卡片、按节点的容器/镜像/CPU/内存汇总、CD 项目发布与漂移状态
- 单节点层：主机资源使用、容器明细、引擎版本与在线时长，点击行即切换到该节点

### 容器与资源管理

- 密度列表 + 行内快捷操作：启动/停止/重启/删除均带破坏性确认
- 容器详情：Inspect（敏感环境变量自动掩码）、实时日志流、xterm.js 可交互终端（支持 resize）、ECharts 实时统计
- 镜像：拉取进度流、标签、删除；支持私有 Registry 凭据认证
- 网络 / 存储卷：完整生命周期；卷删除前检查占用并在用时不允许删除

### Projects 与 Compose 接管

- 统一 Projects 入口：当前一个 SUMA Project 对应一个 Docker Compose Project；通过 backend badge 与 capability 控制操作
- Project 级发现：按 `com.docker.compose.project` 聚合全部 Service 与 Container Instance，正确识别 scale、drift、one-off 和 orphan，不依赖运行态数据库登记
- Project 接管：Local 节点优先安全规范化完整的多文件源配置；任一文件失败则整个 Project 回退到运行态重建；TCP 与 Agent 节点始终只使用 Inspect 元数据
- 环境变量复核：逐项选择写入 compose.yml、明文 `.env` 或排除；镜像默认 ENV 自动排除，敏感值默认遮罩
- 接管前可在 Monaco 编辑并校验；完成接管只原子保存配置，不会立即拉取、停止或重建现有容器
- 可选隔离预演：仅对严格可隔离的无状态草稿创建临时 `suma-preview-*` Project，展示健康、状态和日志，接受或拒绝后清理，不切换生产流量
- Git 来源只读展示：交付过来的 Compose 文件不可篡改，与 CD 域隔离
- 批量操作：多选后一次性 start/stop/restart/up/down，逐项目汇报结果
- 展开即看运行态：服务列表、容器状态、单容器日志与终端入口

### 持续交付（CD）

- 对接任意 HTTPS/SSH Git 仓库（GitLab、GitHub 或自建），凭据 AES-GCM 加密存储
- Webhook 自动触发：兼容 GitHub / GitLab 推送头，通用签名兜底；也可定时轮询同步
- 每次交付是一个不可变 Release：精确 commit + 渲染后的 Compose 配置指纹，全程可审计
- 交付模式：观察 / 手动审批 / 自动；支持多节点并行部署、失败节点定向重试与按节点旧版本回滚
- 部署健康门禁与逐节点三态漂移聚合（健康 / 降级 / 未知），自动模式下可与仓库期望状态对账

### 认证中心

- 统一管理 Git 凭据（token / basic / SSH deploy key）、Registry 凭据、Docker TLS 材料与自定义 CA
- 凭据默认拒绝所有节点访问，必须显式授权给项目或节点；使用中的凭据不可删除

### 运维与安全

- 任务中心：长耗时操作（拉取、prune、部署等）持久化为任务，区分节点与控制平面作用域，WebSocket 实时推送进度与日志
- 审计日志：关键变更操作按节点 / 控制平面严格留痕（操作者、动作、对象、时间）
- 系统清理：磁盘空间预估 + 二次确认
- 首次运行引导创建管理员；bcrypt 密码哈希、HttpOnly SameSite 会话 Cookie
- 独立账户中心：头像、资料和密码维护，支持用户名或邮箱登录、认证器 TOTP 两步验证、一次性恢复码，以及 WebAuthn Passkey 无密码登录
- 中英双语界面、深色/浅色/跟随系统三档主题、`Ctrl/Cmd+K` 全局命令面板

## 快速开始

前置要求：一台装有 Docker Engine 的 Linux 主机；如需管理其它节点的引擎，见下方「接入更多节点」。

### 方式一：Docker Compose（推荐）

将以下内容保存为 `docker-compose.yml`：

```yaml
services:
  suma:
    image: ghcr.io/afteryuwei/suma:stable # 固定版本请改用具体 tag，如 :0.1.0
    container_name: suma
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

浏览器打开 `http://<主机IP>:8080`。首次创建管理员需要初始化密钥：在部署主机运行以下指令，它只打印容器日志中最新的密钥，然后将结果填入页面：

```bash
docker logs suma 2>&1 | sed -n 's/.*"msg":"SUMA initialization key".*"setup_token":"\([^"]*\)".*/\1/p' | tail -n 1
```

仓库提供的 Docker 和 Compose 部署都使用容器名 `suma`；自定义容器名时替换命令中的名称。密钥 30 分钟后过期；如果尚未建号，重启容器后重新运行命令即可取得新密钥。创建管理员后密钥立即失效。**能读取容器日志的人就能在初始化期间创建管理员**，请限制日志访问权限。已有管理员的实例仍直接进入登录页。

**常用环境变量**

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SUMA_DATA_ROOT` | `/Data`（生产镜像内置；裸机运行默认 `./data`） | 数据与凭据根目录，其余路径默认派生 |
| `SUMA_ADDRESS` | `:8080` | 服务监听地址（改端口需同步映射宿主端口） |
| `SUMA_COOKIE_SECURE` | `false` | 可选的强制开启开关；HTTPS 浏览器访问时自动设置 Secure Cookie |
| `SUMA_BROWSER_ORIGIN` | 空 | 可选的高级来源限制初值；通常无需设置，默认按当前请求主机和端口校验 |
| `SUMA_TRUSTED_PROXIES` | 空 | 需要识别代理后客户端 IP 时设置可信代理 IP/CIDR，逗号分隔；不能安全地自动推断 |
| `SUMA_DOCKER_HOST` | `unix:///var/run/docker.sock` | 首次引导默认节点的引擎地址 |
| `SUMA_AGENT_PUBLIC_URL` | 空 | 可选覆盖；默认使用配对时当前 HTTPS 页面来源。仅当 Agent 无法访问该地址时设置其可访问的 HTTPS 来源 |

安全 Cookie 根据直连 TLS 或同主机 HTTPS 浏览器来源自动启用。HTTP 写操作和 WebSocket 握手默认要求来源的主机及端口与请求一致；若通过环境变量或 API 指定浏览器来源，还要求协议一致。可信代理留空时，SUMA 忽略 `X-Forwarded-For` 并使用直连 IP；填写时只信任列出的代理，并由右向左解析代理链。安全项无需在设置页手动填写；已有的高级设置仍可通过 API 修改。代理应保留原始 `Host` 并正确追加或覆盖 `X-Forwarded-For`。

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

### Web 演示构建

演示站直接复用正式 React Web，通过独立构建命令注入浏览器内 Mock API，不需要 Go 服务或 Docker Engine：

```bash
cd web
npm run build:demo
```

演示账号为 `admin`，密码为 `admin123`。构建产物位于 `web/dist`；部署到 Cloudflare Pages 时使用构建命令 `npm run build:demo`、输出目录 `dist`。Pages 在没有顶层 `404.html` 时会自动按单页应用处理前端路由。

普通 `npm run build` 始终生成真实生产 Web，不包含 Mock 数据、固定演示账号或密码。演示构建只能用于公开体验，不得连接真实 Docker API 或承载生产数据。

## 接入更多节点

1. 进入「节点」页面，点击添加节点：
   - 可先创建一个或多个节点 Group，并在节点表单中多选归属；Group 只用于组织和筛选，不代表集群或批量执行目标；
   - **Unix Socket**：把目标机的 `/var/run/docker.sock` 挂载进 SUMA 容器的某个路径后填入该路径；
   - **Docker TCP**：填写远端端点，例如 `tcp://192.168.1.99:2376`，选择 mTLS 并绑定 Docker TLS 凭据。
   - **Agent**：通过 HTTPS 打开 SUMA 后，在节点页生成 10 分钟有效的一次性令牌；默认使用当前页面来源作为 Agent 地址。若 Agent 无法访问此地址，再设置 `SUMA_AGENT_PUBLIC_URL`。可选择现有 Unix/TCP 节点原位迁移，节点 ID 与关联保持不变。
2. 点击「测试连接」验证连通性与延迟。
3. TCP 与 Agent 节点的 Compose bind 源必须是目标宿主机上的不可插值绝对路径。

### Agent Docker 部署

节点页生成的 Compose 已将一次性令牌填入 `SUMA_AGENT_TOKEN` 环境变量，可直接复制到 Agent 主机保存为 `docker-compose.yml`；不要将含令牌的配置提交到代码仓库。手写配置时替换下方占位符。SUMA 地址必须是 Agent 可访问的 HTTPS 地址；反向代理需支持 WebSocket 升级与长连接。私有 CA 可只读挂载到容器并设置 `SUMA_AGENT_CA_FILE`，不能跳过证书校验。

```yaml
services:
  suma-agent:
    image: ghcr.io/afteryuwei/suma-agent:stable # 生产环境建议固定与 SUMA 相同的版本标签
    restart: unless-stopped
    environment:
      SUMA_AGENT_SERVER_URL: https://suma.example.com
      SUMA_AGENT_TOKEN: PASTE_ONE_TIME_TOKEN_HERE
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - suma-agent-data:/var/lib/suma-agent
volumes:
  suma-agent-data:
```

运行 `docker compose up -d`，确认节点在线后可从 Compose 删除 `SUMA_AGENT_TOKEN` 并再次运行该命令；持久卷保存后续重连凭据。撤销凭据后，如需重新配对，生成新令牌、更新环境变量并重新部署 Agent，旧身份认证失败时会自动用新令牌注册。已有部署的 `SUMA_AGENT_TOKEN_FILE` 文件方式继续受支持。Agent 只转发 Docker API；Compose 文件与 CLI 仍在 SUMA 控制端，远端 bind 源必须是目标主机上的明确绝对路径。

> 重要安全提醒：永远不要在网络上暴露无认证的 Docker API（明文 2375）。TCP 远程接入使用 mTLS；Agent 接入使用经验证的 HTTPS/WSS。Docker socket 即使以 `:ro` 挂载，仍授予 Agent 完整的 Docker 管理权限。公网 SUMA 请置于 HTTPS 反向代理之后；浏览器通过 HTTPS 访问时会自动使用 Secure Cookie。

## 数据与备份

- `/Data` 目录包含 SQLite 数据库、本地 Compose 项目、Delivery Project 的 Git 工作区与凭据加密密钥 `secret.key`
- 升级或迁移前先备份整个数据目录；`secret.key` 丢失将导致已存凭据无法解密

## 文档

- [PLANS.md](PLANS.md)：功能进度与上线验证记录
- [ARCHITECTURE.md](ARCHITECTURE.md)：架构说明
- [API.md](API.md)：REST / WebSocket API 参考
- [CD-DESIGN.md](CD-DESIGN.md)：持续交付设计模型
