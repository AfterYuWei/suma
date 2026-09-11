# 接管自动补全 Compose 草稿精简方案（保守修剪，双路径）

## Summary

接管向导自动生成的 compose.yml 过于冗余。本次在不改变语义的前提下对草稿模型做**保守修剪**：删除 Docker Compose 运行时注入标签、默认值字段（tcp 协议、rprivate bind 传播、10s stop_grace_period、SIGTERM stop_signal、服务名自身 alias）和空 map。修剪同时应用于 mapped（安全源渲染）与 runtime（运行态重建）两条草稿路径，manual 模式不受影响（原文保存）。

## Current State Analysis

草稿生成入口为 `BuildTakeoverDraft`（[reconstruction.go L61-L136](file:///Projects/yuweinfo/dockport/server/internal/compose/reconstruction.go#L61-L136)）：

- **mapped 路径**：`runner.Render()`（`docker compose config --format json`）产出规范化模型；
- **runtime 路径**：`runtimeServiceModel`（L236-L339）从容器 Config 逐字段还原，`addRuntimeResources`（L345-L404）还原网络/卷。

冗余来源（均为已确认的代码事实）：

1. **`com.docker.compose.*` 运行时标签**：`runtimeServiceModel` L264 将 `config.Labels` 原样写入 `labels`，其中包含 Compose 在容器创建时注入的十余个内部标签（project、service、container-number、config-hash、oneoff、working_dir、config_files、environment_file、network、volume 等）。这些标签下次 `up` 时 Compose 会重新注入，且 `working_dir`/`config_files` 还固化了旧源路径（甚至指向接管前已不可靠的位置）。
2. **默认值字段**：
   - 端口条目无条件写 `protocol`（L290），tcp 为 Compose 默认；
   - bind 挂载写 `bind: {propagation: rprivate}`（L300-L302），rprivate 为 Docker 默认传播（adapter.go L289 总是携带 `mount.Propagation`）；
   - `stop_grace_period: 10s`（L281-L283），10s 为 Docker 默认 StopTimeout；
   - `stop_signal: SIGTERM`（L280），为默认信号；
   - 服务 `networks.<name>.aliases`（L310-L312）包含 Compose 隐式注入的服务名自身 alias，等价于默认行为；
   - 值为空 map 的键（如修剪后清空的 `labels`、空 `bind`）残留在 YAML 中。
3. mapped 路径经 compose config 规范化同样会携带上述默认值形态。

影响：草稿 YAML 冗长难读；`labels` 还会覆盖/固化旧运行时元数据。

## Proposed Changes

### 1. `server/internal/compose/reconstruction.go` — 新增修剪函数并接入

新增 `pruneTakeoverModel(model map[string]any)`（原地修改），规则仅限"语义可被 Compose 默认值重建"的内容：

| 规则 | 说明 |
| --- | --- |
| 删除服务 `labels` 中所有 `com.docker.compose.` 前缀键 | 非该前缀的用户标签保留；修剪后 map 为空则删除 `labels` 键 |
| 端口条目删除 `protocol == "tcp"`、`host_ip ∈ {"", "0.0.0.0"}`、`mode ∈ {"", "inbox"}` | 保留 `mode: host/ingress` 与其他协议（udp/sctp） |
| bind 型挂载删除 `bind.propagation == "rprivate"`，`bind` map 为空则删除 `bind` 键 | 保留其他传播模式；`read_only` 不动 |
| 删除 `stop_grace_period == "10s"`、`stop_signal == "SIGTERM"` | 仅精确匹配默认值 |
| 服务 `networks.<key>.aliases` 恰好等于 `[该服务名]` 时删除 `aliases` | 网络接线本身保留（保守：不省略网络声明） |
| 修剪后值为空 map 的键删除（如 `labels`、`bind`）；服务 `networks` 整体为空则删除该键 | 防止 `{}` 残留 |

接入点：`BuildTakeoverDraft` 中 `if model == nil { ... }` 回退块之后、`model["name"] = name` 之前调用一次。由于 mapped 分支 `applyExpectedProject` 存入的 `ExpectedConfig` 与模型共享同一 map 指针，原地修剪对显示侧同样生效；指纹在修剪后计算，render/takeover 生命周期内保持一致。

不改动：`renderTakeoverModel`、`RenderTakeoverDraft`（消费已修剪的 `draft.model`）、`validateManagedTakeoverContent`、shadow preview（`AssessShadowPreview` 解析修剪后内容仍然成立）。

### 2. `server/internal/compose/reconstruction_test.go` — 新增覆盖

- 新增 `TestBuildTakeoverDraftPrunesRuntimeNoise`：runtime 路径快照带 `com.docker.compose.*` 标签 + tcp 端口 + rprivate bind + StopTimeout 10 + `aliases: [web]`，断言 `draft.Compose` 不含这些内容，且用户自定义标签、非 tcp 协议、自定义传播、非默认 stop 值、网络接线（`shop_default`）保留。
- mapped 路径在现有 `TestBuildTakeoverDraftPrefersCompleteMappedProject` 的 rendered JSON 中加入带 `com.docker.compose.*` 标签的服务，断言同样被剔除。
- 现有测试已核对：无任何断言涉及 `protocol`/`stop_grace`/`propagation`/`aliases`/labels，预期零破坏。

### 3. 文档同步

- `API.md` Projects/Takeover 段落补一句：草稿会剔除 Compose 运行时标签与默认值字段。
- `PLANS.md` 在 "Unified Projects and Compose Project Takeover" 小节新增条目（验证通过后勾选）。

## Assumptions & Decisions

- 用户已选择：保守修剪 + 双路径应用（`manual` 模式原文保存，天然不受影响）。
- 不省略默认网络接线、不重写 `name:` 字段（留待激进方案，超出本次范围）。
- 修剪是显示与落盘一致的行为变更：已有外部用户草稿不受影响（草稿不持久化，重新预览即生成新内容）。
- 不需要前端改动（接管向导照常消费 `compose`/`environment` 字段）。

## Verification

1. `cd server && go test ./... && go build -buildvcs=false ./...`
2. 真实 Docker 冒烟（按仓库约定）：`cd server && SUMA_RUN_DOCKER_SMOKE=1 go test -tags dockersmoke -count=1 ./internal/compose`
3. 若本机有运行中的 Compose 项目，可人工验证：预览接管草稿，确认 YAML 无 `com.docker.compose.*` 标签、无 `protocol: tcp`、无 `rprivate`，服务网络/卷接线完整。
4. 前端未改动，跳过 web 门禁；PLANS.md 条目在第 1、2 项通过后勾选。
