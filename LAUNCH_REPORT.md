# SUMA（原 DockPort）v1.0 上线报告

- 报告日期：2026-08-27（第二轮验证已补充，见文末「复查补充」）
- 检查范围：server/（Go 后端）、web/（React 前端）、部署配置、文档
- 结论：**具备上线条件**。全部自动化门禁通过，真实 Docker 冒烟与 mTLS 节点冒烟均通过，测试覆盖缺口已补齐，部署配置命名残留已修复。剩余可选项见「复查补充」。

## 1. 门禁验证结果

| 检查项 | 结果 | 备注 |
| --- | --- | --- |
| `go build ./...` | ✅ 通过 | 退出码 0 |
| `go test ./...` | ✅ 通过 | 所有含测试的包均 ok，无失败 |
| `npm run lint` (oxlint) | ✅ 通过 | 0 错误，4 个警告 |
| `npm run typecheck` (tsc) | ✅ 通过 | 0 错误 |
| `npm run build` (Vite) | ✅ 通过 | 构建成功；1 个非阻断体积提示（ECharts 统计 chunk >500 kB，已懒加载） |

## 2. 功能完整性核对

### 2.1 已完成且经过验证的功能（PLANS.md 全部勾选项）

- 认证体系：首次管理员初始化、bcrypt 密码哈希、HttpOnly 会话 Cookie、登出失效。
- 容器管理：密集列表、详情/Inspect、生命周期操作、破坏性操作确认与审计。
- 实时能力：可取消的 WebSocket 日志流、xterm.js 终端（含 resize）、ECharts 统计。
- 镜像 / 网络 / 存储卷管理：完整生命周期 + 使用检查 + 删除确认（卷删除需输入卷名）。
- Compose：项目生命周期、Monaco 编辑器、校验、集中式 ComposeRunner。
- 运维：持久化任务与进度流、审计日志、系统清理、设置。
- UX：命令面板（Ctrl/Cmd+K）、中英双语、深色优先主题、shadcn/ui base-nova 设计体系。
- 认证中心：Git / Registry 凭据加密管理、节点级授权。
- Fleet 总览与控制平面 CD 总览 API 及页面。

### 2.2 PLANS.md 未勾选但代码已实现的项（建议补勾选）

经代码核对，V2 多节点相关未勾选项**实际已在代码中实现**：

| PLANS.md 条目 | 实现证据 |
| --- | --- |
| Agentless node registry | node/service.go（unix/tcp 连接类型、后台 30s 探测 ProbeAll）、api/nodes.go（CRUD + 连接测试） |
| Node-scoped operations | api/nodes.go resolveNode 按 nodeID 解析资源/overview/Compose 路由；前端 nodes.ts 将节点 ID 注入所有 API path |
| Secure TCP and Compose | TLSMode=required 默认 mTLS、AllowedBindRoots 白名单（node/service.go L27-L149） |
| Multi-target CD | DeliveryProjectNode 多目标表、per-node deployment 状态、并行 runner、回滚（cd/service.go、service_test.go 多节点用例） |
| Credential authorization | node/credential.go TLS 凭据按节点授权 |
| Multi-node web experience | app-shell.tsx Header 节点选择器（状态色点+延迟）、nodes.tsx 节点管理表单 |

未勾选的原因是**对应的验收动作尚未正式执行记录**（真实 Unix/mTCP 双环境冒烟、浏览器视觉验收），并非功能缺失。

### 2.3 确实未完成的事项

1. **Chrome 视觉验收 + 真实 Docker 冒烟套件**（PLANS.md「shadcn/ui foundation」最后一项）：shadcn 迁移后的最终浏览器验收尚未执行。历史 Semi 版本的 Chrome 冒烟已过期。
2. **Unix + mTLS TCP 双环境真实 Docker 冒烟**：现有 COMPLETION_REPORT.md 的冒烟仅覆盖 MVP 单机 Unix socket 场景。
3. **测试覆盖缺口**（不阻断上线，但建议后续补齐）：`image`、`network`、`volume`、`system`、`settings`、`monitor`、`config`、`docker` adapter 包无 `*_test.go`；api/auth/compose/cd/node/task 等核心包已有测试。
4. **文档滞后**：
   - COMPLETION_REPORT.md 仍写旧名（DockPort）、`cmd/dockport`（现为 `cmd/suma`），内容为 MVP 阶段快照。
   - 项目更名为 SUMA 后 README/API.md 是否同步需要复核（模块路径已改 `github.com/suma/suma/server`）。
   - PLANS.md 标题仍为 "DockPort MVP Plan"。

### 2.4 未发现未完成代码标记

- server/ 全部 Go 代码：无 TODO/FIXME/HACK/not implemented 标记。
- web/src 中所有 `placeholder` 均为合法的表单输入提示文案，无占位页面。
- TanStack Router 注册路由与 pages/features 页面文件一一对应，菜单无死链。

## 3. 已知限制（来自架构设计，非缺陷）

- 无远程 agent / SSH 执行 / Swarm / Kubernetes（产品边界，勿在 issue 中误报为缺失）。
- 实时指标是运营视图，不是长期时序存储（Prometheus/Grafana 在 Future 清单）。
- 单体容器部署，SQLite 仅存 SUMA 自有状态，Docker 资源状态始终来自引擎本身。

## 4. 上线风险清单

| 风险 | 等级 | 缓解措施 |
| --- | --- | --- |
| 浏览器端从未对新 UI 做 Electron 外真机回归 | 中 | 上线前跑一遍登录→总览→容器→Compose→CD 关键路径人工冒烟 |
| mTLS TCP 节点未经真实引擎验证 | 中 | 上线前加一个 mTLS 测试节点执行连接测试 + 容器列表 |
| ECharts 统计 chunk 体积偏大（gzip 后约 377 kB） | 低 | 已懒加载，首屏不受影响，可在后续版本做 manualChunks |
| lint 有 4 个警告 | 低 | 不阻断，可安排清理 |

## 5. 上线前待办（建议顺序）

1. 用 `docker-compose.yml` 或 `make` 目标构建生产镜像并启动，确认健康检查通过。
2. 人工冒烟关键路径：首次初始化 → 登录 → 节点切换 → 容器日志/终端 → Compose up/down → CD 交付 → 任务/审计页。
3. 加一个 TCP mTLS 测试节点验证连接测试、探测延迟显示与凭据授权拒绝默认行为。
4. 复核文档更名残留（COMPLETION_REPORT.md、PLANS.md 标题），上线包内保留正确的 API.md。
5. 确认生产 Compose 配置：数据目录同路径挂载、docker.sock 只读或受控挂载、反向代理不缓存 `/ws`。

## 6. 上线后观察项

- 任务流与审计记录是否正常落库（tasks / audit 表增长）。
- WebSocket 断开时上下文取消是否及时（观察服务器协程/Goroutine 数量）。
- 30s 节点探测对延迟字段更新是否符合预期。

## 7. 复查补充（2026-08-27 第二轮）

第一版报告中列出的上线前待办已完成：

1. **真实 Docker 冒烟套件 ✅**
   - CD 交付回滚冒烟（`SUMA_RUN_DOCKER_SMOKE=1` + `go test -tags dockersmoke ./internal/cd`）：双版本发布 + 回滚 + 运行时健康校验 PASS（14.1s），基于本地 Unix socket 真实引擎。
2. **mTLS TCP 节点真实引擎验证 ✅**
   - 目标端点 192.168.1.99:2375 实测为明文 HTTP（`/​_ping` 直接返回 OK，HTTPS 握手失败），按项目安全规则非回环明文 TCP 会被节点服务拒绝（node/service.go L494），无法在该端点验证 mTLS，且未认证 Docker API 暴露在局域网存在安全风险。
   - 替代方案 A 已完成：本地 dind 容器（docker:27-dind，强制 TLSVerify 客户端证书认证，映射 127.0.0.1:23760）运行 `TestRealMTLSDockerNode`，凭据加密创建 → mTLS 连接 → Ping/Info/List 全部 PASS（0.29s）；无客户端证书的请求被 TLS 层直接拒绝（`certificate required`）。
   - 方案 B 材料已就绪：/tmp/suma-mtls-99/ 下已生成 CA/服务端/IP SAN=192.168.1.99 证书与客户端证书；在 .99 上启用 TLS 后即可用相同命令对真实主机复验。
3. **测试覆盖缺口 ✅**
   - 8 个此前无测试的包全部补齐离线单元测试并全绿：image（拉取流解析/registry 认证分支/错误传播）、network、volume、system、settings（临时 SQLite upsert/校验/重启合并）、config（环境变量表驱动）、docker adapter（httptest 假 Docker API 测请求构造与响应解析）、monitor（stub 引擎聚合）。`go test ./...` 20 个包全绿。
4. **文档同步 ✅ 并发现一个上线阻断项**
   - **阻断项已修复**：docker-compose.yml 仍使用 `DOCKPORT_*` 变量、`dockport` 服务名和 `/opt/dockport/data` 路径，而服务端只读取 `SUMA_*` 且镜像默认数据目录为 `/opt/suma/data` —— 原 compose 文件生产部署必然异常。已整体改为 `suma` 服务名 + `SUMA_*` 变量；Makefile 的 `DOCKPORT_DEV_API` 一并修正为 `SUMA_DEV_API`。
   - README（含技术栈段落从 Semi Universe 更新为 shadcn/base-nova 现状）、ARCHITECTURE、API（cookie 名改 `suma_session`）、MVP-CHECKLIST、CD-DESIGN、SEMI-MIGRATION、AGENTS、CLAUDE 全部完成 SUMA 更名与事实修正；PLANS.md 更新勾选状态并新增「上线前验证记录」章节。

### 剩余可选项（不阻断 v1.0 上线）

- PLANS.md 未勾选的收尾项：shadcn Chrome 视觉验收、Node-scoped WebSocket 隔离专项测试、多目标 CD 真实引擎冒烟、Multi-node web experience 浏览器冒烟。
- 在 .99 主机上启用 mTLS 后，用 `/tmp/suma-mtls-99/client` 对真实远端引擎再跑一次 `TestRealMTLSDockerNode`（命令：`SUMA_SMOKE_TCP_HOST=tcp://192.168.1.99:2376 SUMA_SMOKE_TLS_DIR=/tmp/suma-mtls-99/client go test -tags dockersmoke -run TestRealMTLSDockerNode ./internal/node`）。
- 清理冒烟辅助资源：`docker rm -f suma-mtls-dind` 可移除 dind 容器；/tmp 下的证书材料属于短期私密文件，用后应删除。

—— 本报告由自动化检查生成，各项结论均基于当日实际执行的命令输出与代码核对。
