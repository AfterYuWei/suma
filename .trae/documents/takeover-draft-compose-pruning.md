# 接管自动补全 Compose 草稿精简方案（保守修剪，双路径）

## Summary

接管向导自动生成的 compose.yml 过于冗余。本次在不改变运行语义的前提下对草稿模型做**保守修剪**。mapped（安全源渲染）路径删除 Compose 规范化时补入的 null、默认网络和端口长格式；runtime（运行态重建）路径额外对照镜像配置剔除 Dockerfile 默认值，并剔除可证明为引擎生成的默认值。manual 模式保持操作员原文，不参与修剪。

## Current State Analysis

草稿生成入口为 `BuildTakeoverDraft`（[reconstruction.go L61-L136](file:///Projects/yuweinfo/dockport/server/internal/compose/reconstruction.go#L61-L136)）：

- **mapped 路径**：`runner.Render()`（`docker compose config --format json`）产出规范化模型；
- **runtime 路径**：`runtimeServiceModel`（L236-L339）从容器 Config 逐字段还原，`addRuntimeResources`（L345-L404）还原网络/卷。

冗余来源（均为已确认的代码事实）：

1. **`com.docker.compose.*` 运行时标签**：`runtimeServiceModel` L264 将 `config.Labels` 原样写入 `labels`，其中包含 Compose 在容器创建时注入的十余个内部标签（project、service、container-number、config-hash、oneoff、working_dir、config_files、environment_file、network、volume 等）。这些标签下次 `up` 时 Compose 会重新注入，且 `working_dir`/`config_files` 还固化了旧源路径（甚至指向接管前已不可靠的位置）。
2. **Compose 规范化默认值**：
   - `command: null`、`entrypoint: null`；
   - 仅连接 `${project}_default` 的默认 bridge 网络及空 `ipam`；
   - 简单端口映射被展开为带 `mode: ingress`、`protocol: tcp` 的长格式；
   - bind 挂载的 `rprivate`、默认 `10s` 停止宽限期和服务名自身 alias。
3. **运行态复制值**：容器 Inspect 无法直接区分服务覆盖值与镜像 Dockerfile 默认值，旧实现因此把镜像的 command、entrypoint、user、working directory、healthcheck、stop signal、exposed ports 和 image volumes 全部抄回草稿；同时还抄入容器 ID hostname、private IPC、64 MiB shm 等引擎默认值。
4. **保真缺陷**：
   - 端口条目无条件写 `protocol`（L290），tcp 为 Compose 默认；
   - 端口默认模式曾误写为 `inbox`，实际 Compose 值是 `ingress`，导致默认模式没有剔除；
   - `on-failure` 的最大重试次数被读取但未写回；
   - `.env` 出现重复 `COMPOSE_PROJECT_NAME` 时读取首个值，而 dotenv 的后值应覆盖前值。

影响：草稿 YAML 冗长难读；`labels` 还会覆盖/固化旧运行时元数据。

## Proposed Changes

### 1. `server/internal/compose/reconstruction.go` — 新增修剪函数并接入

`pruneTakeoverModel(model map[string]any)` 原地修改规范化模型，规则仅限“语义可由 Compose 默认值重建”的内容：

| 规则 | 说明 |
| --- | --- |
| 删除服务 `labels` 中所有 `com.docker.compose.` 前缀键 | 非该前缀的用户标签保留；修剪后 map 为空则删除 `labels` 键 |
| 删除 `command: null`、`entrypoint: null` | 不删除显式的空数组或字符串 |
| 端口条目删除 `protocol == "tcp"`、`host_ip ∈ {"", "0.0.0.0"}`、`mode ∈ {"", "ingress"}`，简单映射改为短格式 | 保留 `mode: host`、命名端口、`app_protocol`、udp/sctp、指定 host IP 等有意义内容 |
| bind 型挂载删除 `bind.propagation == "rprivate"`，`bind` map 为空则删除 `bind` 键 | 保留其他传播模式；`read_only` 不动 |
| 删除 `stop_grace_period == "10s"` | mapped 路径保留显式 `stop_signal`；runtime 路径仅在其等于镜像/引擎默认值时删除 |
| 服务 `networks.<key>.aliases` 恰好等于 `[该服务名]` 时删除 `aliases` | 网络接线本身保留（保守：不省略网络声明） |
| 服务仅以空配置连接默认 `${project}_default` bridge 网络时，删除服务和顶层默认网络声明 | 自定义名称、驱动、IPAM、external/internal/attachable/IPv6 或多网络接线均保留 |
| 修剪后值为空 map 的键删除（如 `labels`、`bind`）；服务 `networks` 整体为空则删除该键 | 防止 `{}` 残留 |

接入点：`BuildTakeoverDraft` 中 `if model == nil { ... }` 回退块之后、`model["name"] = name` 之前调用一次。由于 mapped 分支 `applyExpectedProject` 存入的 `ExpectedConfig` 与模型共享同一 map 指针，原地修剪对显示侧同样生效；指纹在修剪后计算，render/takeover 生命周期内保持一致。

不改动：`renderTakeoverModel`、`RenderTakeoverDraft`（消费已修剪的 `draft.model`）、`validateManagedTakeoverContent`、shadow preview（`AssessShadowPreview` 解析修剪后内容仍然成立）。

### 2. runtime 镜像与引擎默认值相减

Docker adapter 在 inspect 容器镜像时采集 command、entrypoint、user、working directory、environment、healthcheck、stop signal、exposed ports 和 image volume targets。runtime 重建只在镜像 inspect 成功且值完全相等时删除这些字段；已发布端口继续保留，只删除未绑定且来自镜像 `EXPOSE` 的端口。image volume 只在卷名是 64 位十六进制匿名卷且目标来自镜像 `VOLUME` 时删除，避免误删显式 named/external volume。

可证明为引擎生成的短容器 ID hostname、`ipc: private`、64 MiB `shm_size` 和 `init: false` 同样删除。运行态资源输出只保留仍被服务引用的网络和卷，避免匿名镜像卷被删后遗留孤立顶层声明。

### 3. 保真修复

- `restart: on-failure:N` 保留最大重试次数；
- `.env` 项目名校验采用最后一个同名赋值，并支持 `export` 后的空格或 tab；
- 默认端口模式按正确的 `ingress` 识别。

### 4. `server/internal/compose/reconstruction_test.go` — 新增覆盖

- mapped 极简 nginx 用例精确断言最终只剩 `name`、`image` 和 `ports: ["8080:80"]`；
- runtime 极简 nginx 用例覆盖镜像 command/entrypoint/environment/healthcheck/stop signal/exposed port/image volume 以及引擎 hostname/IPC/shm，精确断言同样只剩 `name`、`image` 和已发布端口；
- named volume 即使挂到镜像 `VOLUME` 目标也必须保留；
- 自定义标签、udp、host IP、自定义传播、停止设置、重试次数和自定义资源继续保留；
- Docker adapter 测试覆盖完整镜像默认值采集，takeover 测试覆盖重复项目名的 dotenv 覆盖顺序。

### 5. 文档同步

- `API.md` Projects/Takeover 段落补一句：草稿会剔除 Compose 运行时标签与默认值字段。
- `PLANS.md` 在 "Unified Projects and Compose Project Takeover" 小节新增条目（验证通过后勾选）。

## Assumptions & Decisions

- 使用保守修剪 + 双路径应用（`manual` 模式原文保存，天然不受影响）。
- 仅省略可证明等价的隐式默认网络；所有自定义网络配置和多网络关系保留。
- runtime 无法证明来源时宁可保留；匿名卷必须同时满足镜像目标和 64 位十六进制卷名两个条件。
- 修剪是显示与落盘一致的行为变更：已有外部用户草稿不受影响（草稿不持久化，重新预览即生成新内容）。
- 不需要前端改动（接管向导照常消费 `compose`/`environment` 字段）。

## Verification

1. `cd server && go test ./... && go build -buildvcs=false ./...`
2. 真实 Docker 冒烟（按仓库约定）：`cd server && SUMA_RUN_DOCKER_SMOKE=1 go test -tags dockersmoke -count=1 ./internal/compose ./internal/docker`
3. `cd web && npm run lint && npm run typecheck && npm run build`
4. 人工检查极简 nginx 的 mapped/runtime 草稿均不含镜像或引擎默认值，自定义网络/卷场景仍完整。
