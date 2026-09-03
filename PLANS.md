# SUMA Plan

This file records the completed MVP implementation. Every checked phase was verified by automated tests, production builds, or the real-Docker smoke tests recorded in the pre-launch verification log below.

## Completed phases

- [x] Foundation: React 19 TypeScript frontend, layered Go/Gin backend, GORM/SQLite, Docker adapter, health APIs, compact application shell, and themes.
- [x] Authentication: first-run administrator initialization, bcrypt login, hashed opaque sessions, HttpOnly cookie, protected APIs, and logout invalidation.
- [ ] Single-user account center: optional nickname, required email with username/email login, verified profile and password changes, other-session revocation, SQLite-backed cropped WebP avatar lifecycle, account menu/page, bilingual UI, audit coverage, and compatibility for legacy users without email. Automated account/migration/HTTP tests, backend/web gates, and an isolated authenticated real-Docker access smoke passed on 2026-08-29; browser interaction checks remain before completion.
- [x] Containers: dense searchable list, detail and inspect, lifecycle actions, confirmations, and audit records.
- [x] Realtime containers: cancellable WebSocket logs, xterm.js exec terminal with resize and shell fallback, and ECharts statistics.
- [x] Docker resources: image, network, and volume lifecycle operations with usage checks and confirmations.
- [x] Compose: project lifecycle, Monaco Compose/`.env` editor, validation, changed-file summary, service/log views, and one centralized `ComposeRunner`.
- [x] Operations: persistent tasks and logs, realtime task progress, audit log, confirmed system prune, and settings.
- [x] UX: global command palette, Chinese/English localization, accessible dialogs, loading/error/empty states, responsive layout, design-system primitives, and dark/light/system themes.
- [x] Opt-in Web demo build: `npm run build:demo` compiles the real React interface with browser-local Mock authentication, Docker resources, Compose, CD, task/audit APIs, and realtime log/stat/terminal streams; `npm run build` tree-shakes the complete demo module and fixed credentials. Verification: frontend lint/typecheck, production build, demo build, demo-chunk credential scan, and production artifact absence scan.
- [x] Deployment: multi-stage Dockerfile, Compose deployment, persistent database/project storage, same-path bind mounts, and complete README.
- [x] Verification: backend tests/build, frontend lint/typecheck/build, production image, persistence restart, and complete live-engine smoke suite.

## Frontend redesign

- [x] Semi Design foundation: replace Base UI with the React 19-specific `@douyinfe/semi-ui-19`, install global locale/theme providers, and migrate shared Select, dialog, checkbox, switch, and dropdown primitives. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Semi Design forms and actions: migrate native credential, node, settings, Compose, CD, and resource controls to Semi Input/TextArea/Select/Checkbox/Button/Form without changing validation, secret-masking, or destructive-confirmation behavior. Verification: frontend gates plus isolated Chrome interaction smoke tests for authentication, settings, node SideSheet, Compose, and resource actions.
- [x] Semi Design navigation and feedback: migrate shell navigation actions, command palette, tabs, disclosure, loading/error/empty feedback, and task status surfaces; remove obsolete compatibility CSS and unused primitive helpers. Verification: frontend gates plus isolated Chrome navigation, overlay, keyboard-dismissal, tab, desktop, and mobile smoke tests.
- [x] Semi Design visual acceptance: verify dark/light/system themes, Chinese/English locale, responsive dense lists, keyboard navigation, overlays, Monaco/xterm/ECharts integration, and production bundle splitting. Verification: frontend gates and Chrome 152.0.7977.64 browser smoke tests passed with no page errors; system theme follows live OS color-scheme changes.

### Universe Design realignment

The completed items above cover the Semi component-layer migration. Their visual-acceptance rules were superseded by the later shadcn/ui foundation section: the UI layer no longer keeps an independent Semi-based visual language.

- [x] Universe theme gate: integrate official `@douyinfe/semi-vite-plugin` and `@semi-bot/semi-theme-universedesign`; prove the published theme package against React 19 Semi `2.102.x`, light/dark mode, lazy chunks, and production build. Verification: Universe `1.0.13` tokens resolved in Chrome and the production build passed with Semi `2.102.x`.
- [x] Universe application frame: rebuild shell, authentication, command palette, navigation, theme controls, typography, spacing, and overlays from Semi compositions; remove project styling for those surfaces. Verification: desktop/mobile Nav, authentication, command palette, overlays, and theme controls passed isolated browser checks.
- [x] Universe resource language: migrate resource lists, metadata, statuses, forms, validation, details, disclosures, and operation controls to Semi Table/List/Descriptions/Tag/Badge/Form/Card/Tabs/Collapse patterns while retaining dense Docker workflows. Verification: all resource routes and node/CD forms passed component and interaction checks.
- [x] Universe page migration: remove the custom cockpit, signal/orbit, glass, grid, scanline, gradient, shadow, radius, palette, and component-state styling from every page; Tailwind is permitted only for responsive structure and placement. Verification: global CSS reduced to 30 lines and the source audit found no project visual utilities or component overrides.
- [x] Universe integration styling: source ECharts colors from `--semi-color-data-*`, use built-in Monaco light/dark themes and Semi tokens for xterm, and retain only structural sizing/overflow CSS for third-party surfaces. Verification: Monaco, ECharts, and xterm rendered in the isolated browser suite.
- [x] Universe enforcement and acceptance: prohibit `.semi-*` appearance overrides and visual Tailwind utilities, reduce global CSS to the approved boundary, then pass frontend gates and isolated light/dark/system, locale, responsive, keyboard, overlay, destructive-flow, Monaco/xterm/ECharts visual smoke tests. Verification: `npm run audit:universe`, lint, typecheck, production build, diff check, and Chrome 152.0.7977.64 completed with zero page errors.
- [x] Immersive operations cockpit: unified Lucide iconography, emoji-free interface, redesigned navigation, authentication, command center, live host canvas, semantic materials, responsive behavior, and reduced-motion support. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Dense Compose project list: compact desktop columns and responsive mobile rows expose path, runtime status, service/container counts, and precise update time without inline action-button clutter. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Expandable Compose operations: independent multi-row expansion, project start/stop/restart/update/down actions, live container status, and per-container start/stop/resume/restart/log/terminal shortcuts. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Container-style Compose list and batch operations: rounded dense resource rows, right-aligned expand controls, multi-select, and batch start/stop/restart/update/down tasks with per-project results. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Unified dense resource lists: Continuous Delivery, images, networks, and volumes now share the rounded high-density list language, responsive rows, semantic icons, compact metadata, empty/error states, and right-aligned actions used by containers and Compose. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Reliable Compose runtime attribution: correlate containers by Compose working-directory labels rather than assuming the Docker Compose runtime name equals the SUMA project name. Verification: `go test ./...` and `go build ./...`.
- [x] Docker-label Compose discovery: remove SQLite as the Compose inventory source, group current-node projects from `com.docker.compose.*` labels, retain inactive SUMA-managed projects through Compose-root discovery, and expose external Compose Projects as Project-level runtime views. The former local single-file copy behavior was removed by the later Project Takeover phase. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Compose operation clarity and editing: replace the ambiguous Update action with Pull & recreate (`pull` then `up -d`), show its asynchronous task status and result inline with a Task Center link, expose managed Project editing and external Project Takeover paths, and standardize dense project/container action groups on icon-only controls with shadcn tooltips while keeping toolbar actions icon-plus-text. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Compose Project detail action feedback: make stop/restart/pull/build/down/start respond immediately with an action-specific loading state and Task progress dialog, stream Compose output and terminal success/failure into the dialog, support explicit cancellation and background continuation, refresh Project runtime views on completion, and parse newline/carriage-return CLI progress with monotonic percentage reporting. Verification: report-writer unit coverage, `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, production/demo builds, and an isolated real-Docker `docker compose pull` Task smoke test that starts no containers.
- [x] Container log tail behavior: default every container-log surface to the newest 200 lines, provide a persisted 100–5000-line selector, enforce the selected limit at the Docker/Compose source and in the live browser buffer, continue following appended output only while the reader remains near the bottom, preserve position while they inspect history, and resume following after they return to the tail. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, `npm run build`, and a real-Docker shadow-preview smoke test.
- [x] Unified list pagination and scroll containment: paginate resource, activity, task-output, Project-service, environment, release, deployment, credential, and nested runtime lists with a persisted 10/20/50/100 page-size preference (default 20); make page-level selection operate on the visible page; and give table/list viewports sticky headers, bounded height, and overscroll containment so wheel input does not move the surrounding page. Verification: `npm run lint`, `npm run typecheck`, `npm run build`, and `npm run build:demo`.
- [x] Global shadcn Tooltip enforcement: provide Tooltip once at the application root and replace native browser `title` hints across shell, Docker resources, Compose, fleet overview, nodes, and delivery controls with the shared shadcn Tooltip wrapper, including disabled controls and truncated values. Verification: native-title source audit, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Image list selection and pull workflow: add searchable multi-select rows, container-style selection actions to the left of the search field, safe non-force batch removal with partial-failure feedback, and used/unused image counts beside the page title. Keep one Pull button in the toolbar and use a dedicated dialog for optional credentials, stable selector positioning, live Task progress, explicit pull cancellation, and background continuation when the dialog closes. Persist every Docker Layer as a Task Step with stage-aware progress, byte counts, status, and terminal success/failure/cancellation handling, then expose and render all Layer steps in the pull dialog. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Network list selection and runtime disclosure: add multi-select with confirmed batch removal and partial-failure feedback, plus on-demand row expansion that inspects the selected node runtime and lists each attached container with its IPv4 and IPv6 endpoint addresses. Verification: adapter/domain tests, `go test ./...`, `go build ./...`, frontend gates, and a read-only real-Docker Network Inspect smoke check.
- [x] Volume list batch removal: add selection for unused volumes, select-all over only safe candidates, exact-name confirmation covering every selected volume, non-force removal, and partial-failure feedback when runtime usage changes. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.

### Unified Projects and Compose Project Takeover

SUMA uses `Project` as its first-level application and orchestration domain object. A current `Project backend=compose` maps to an official Docker Compose Project. A future `Project backend=swarm` may map to an official Docker Swarm Stack, but no Swarm runtime, API, runner, task, scheduler, or menu is implemented in this phase. The UI has one `Projects` entry and keeps Docker-specific official names at adapter and backend boundaries.

- [x] Project domain foundation: add backend/scope-aware `ProjectRef`, capability-based actions, a list-only `ProjectSummary` contract that excludes Compose content, environment values and source paths, separate observed/draft/managed models, and non-secret `.suma/project.json` identity metadata while retaining legacy managed Compose directories. Takeover drafts expose `shadow_preview` only after strict eligibility succeeds. Verification: Project identity/serialization/capability tests, HTTP response tests, `go test ./...`, and `go build ./...`.
- [x] Compose Project observation: aggregate all containers by `com.docker.compose.project`, group instances by Service, preserve `--scale` as multiple Container Instances under one Service, retain source-declared Services with no running instances, and detect canonical variants, field-level drift without exposing field values, evidence-based drift reasons, one-off containers, orphans, networks, and volumes without importing Docker SDK types into the Compose domain. Configuration variants never split a Service or Project. Verification: observation, source-correlation and Docker adapter tests plus backend gates and a real-Docker drift/one-off/unassigned-container smoke test.
- [x] Safe source normalization and whole-Project reconstruction: Local Unix nodes use all safely accessible `working_dir`/`config_files` in label order through the single `ComposeRunner`; path, symlink, file type, count, size, and associated-file checks are all-or-nothing. Any failure falls back for the entire Project. TCP nodes never read label paths and use Inspect metadata only. Verification: mapped multi-file, escape, dependency, runtime reconstruction, environment-source, and mTLS TCP smoke tests.
- [x] Project Takeover API: preview, variable rendering, unsaved-draft validation, exact Project Name confirmation, fingerprint conflict detection, shared per-Project concurrency locks, atomic Compose/`.env`/metadata writes, and Audit integration. Takeover does not call pull/up/down and does not alter existing containers. The old single-container and single-file copy APIs are removed. Verification: takeover service tests, authenticated HTTP concurrency test with exactly one `201` and one `409`, Project Summary/Audit secret-leak assertions, backend gates, and unchanged-container real-Docker assertions.
- [x] Projects web experience: one `/projects` entry, backend badges, capability-driven actions, legacy `/compose` redirects, external Project takeover warning, four-step Project analysis/environment/Monaco edit/confirmation flow, a dedicated drift/stopped/orphan report, sensitive values held in page memory only, and first-SUMA-deployment risk confirmation. Dense row actions remain icon-only with shadcn Tooltip; toolbars use icon plus text. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Blocked external Project cleanup: when takeover analysis fails or reports blockers, offer a Project-level destructive cleanup Task that requires the complete Project Name, force-removes only exact-label Project containers, removes only Project-owned networks, and preserves bind paths and all volumes by default. Project-owned named-volume deletion is an independent high-risk opt-in, uses non-force Docker removal, and has a distinct Audit action. Verification: capability/service/adapter/authenticated-HTTP tests, isolated real-Docker preserve/delete-volume smoke, `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Optional isolated preview: strict default-deny eligibility rejects published ports, production mounts, external networks, shared namespaces, privileged devices, build contexts, file dependencies, configs, and secrets. Eligible drafts use a temporary `suma-preview-*` Compose Project with isolated network names, Task progress, health/runtime status and logs, explicit accept/reject/decide-later cleanup, 30-minute TTL, page-leave cleanup, and proactive startup recovery across every enabled node. It never switches production traffic. Verification: eligibility/unit tests, previous-process recovery test, Task lifecycle tests, frontend gates, and real-Docker preview create/status/cleanup smoke.
- [x] Project Takeover real-Docker verification (2026-08-28): Local Unix multi-file mapped source, forced whole-Project runtime fallback, two-instance scale aggregation, isolated preview lifecycle, takeover without container replacement, evidence-based runtime drift, one-off/unassigned-container classification, and independently hosted mTLS TCP runtime-only takeover all passed. The dedicated Docker-in-Docker mTLS engine and test certificates were removed after verification; all temporary Local smoke resources were cleaned by the tests.

#### Project Takeover completion verification (2026-08-28)

- Final server gate: `go test ./...` and `go build ./...` PASS, including authenticated Projects HTTP response and concurrent takeover coverage.
- Final web gate: `npm run lint`, `npm run typecheck`, and `npm run build` PASS. Lint retains four pre-existing non-blocking shadcn primitive warnings; the production build retains Vite/CSS/chunk-size advisory warnings.
- Final Local Docker gate: `SUMA_RUN_DOCKER_SMOKE=1 go test -tags dockersmoke ./internal/compose ./internal/docker -count=1` PASS. This reran mapped/runtime takeover, scale aggregation, preview lifecycle and cleanup, plus real Engine drift/one-off/unassigned-container inspection.
- The mTLS TCP runtime-only takeover smoke had already passed independently in this phase; TCP label paths remained unread and the temporary TLSVerify engine/certificates were removed afterward.

### shadcn/ui foundation

This section supersedes the Semi Design and Universe Design phases above (the former Universe styling boundary is retired): the base UI layer is now shadcn/ui in the official `base-nova` style on `@base-ui/react` primitives with Tailwind v4 design tokens, dark-first defaults, light/dark/system themes, and the Lucide icon library.

- [x] Foundation: `components.json`, official `@theme inline` token layer in `src/styles.css` (oklch light/dark), `@/lib/utils` `cn()`, `@` path aliases, `shadcn/tailwind.css` utilities, and dependency swap from Semi to `@base-ui/react`. Verification: `npm run typecheck`.
- [x] Registry components: all shared primitives (`button badge card input input-group textarea label select checkbox switch radio-group dialog alert-dialog alert dropdown-menu popover tooltip sheet command progress skeleton spinner separator collapsible tabs table scroll-area`) installed from the official base-nova registry source. Verification: `npm run typecheck`, `npm run lint`.
- [x] Shell and primitives rewrite: app shell sidebar/header/mobile Sheet navigation, Base UI Select node switcher, theme-toggle dropdown, AppDialog (confirm/prompt/choice flows preserved via the dialog store), Command palette rebuilt on `command`, ErrorState on `alert`, LoadingState on `skeleton`+`spinner`. Verification: `npm run typecheck`, `npm run lint`, `npm run build`.
- [x] Page and feature migration: all pages and feature modules rewritten from Semi components to shadcn patterns with identical business logic, i18n, destructive confirmations, volume-name prompts, and secret masking; xterm/ECharts palettes now read shadcn tokens (`--background`, `--foreground`, `--chart-*`). Verification: frontend gates (`npm run lint`, `npm run typecheck`, `npm run build`) pass with zero `@douyinfe` references.
- [x] Cleanup: Semi dependencies, the Semi Vite theming plugin, and the Universe audit gate removed; `oxlint` retained as lint gate.
- [ ] Visual acceptance in Chrome against official shadcn demos (light/dark/system, locale, overlays, Monaco/xterm/ECharts) plus the real-Docker smoke suite.

## Authentication center

- [x] Reusable Git and registry credential management, encrypted project-owned Git credentials, explicit CD credential sources, bilingual Authentication Center UI, audit coverage, and lifecycle tests. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, `npm run build`, and the real-Docker CD smoke test.

## V2 multi-node Docker management

- [x] Agentless node registry: mounted `unix://` sockets and `tcp://` Docker APIs, default-node migration, runtime client replacement, duplicate Engine detection, background status probes, and safe reference-aware deletion. Verification: node service/security tests pass and the real-Docker Unix engine (CD delivery/rollback smoke) plus a real TLSVerify-enabled Docker engine (`TestRealMTLSDockerNode`, Ping/Info/List) both passed on 2026-08-27.
- [x] Node-scoped operations: resource, overview, Compose, system, task, audit, and WebSocket APIs resolve an explicit node; Task/Audit distinguish node and control-plane scopes, canonical node task routes reject cross-node IDs before logs/steps/cancel/WebSocket upgrade, and validated legacy aliases remain available. Verification: HTTP/WebSocket isolation tests, `go test ./...`, and `go build ./...` passed on 2026-08-31.
- [x] Secure TCP and Compose: mTLS by default, typed endpoint confirmation for loopback/private-network/Tailscale plaintext, encrypted TLS credentials, explicit absolute remote binds, and private temporary TLS/registry configuration with cleanup. Verification: security tests and compose multinode runner cleanup tests pass (`go test ./...` green on 2026-08-30).
- [ ] Multi-target CD: immutable target snapshots, bounded parallel per-node drift probes with tri-state aggregation/cache, immutable deployment Attempts, transactional restart recovery, per-node tasks/status/health/failure history, partial-failure aggregation, and server-selected failed-node retry/rollback are implemented. Unit/migration/API gates and the Unix real-Docker delivery/rollback smoke passed on 2026-08-31; the added dual-node Unix+mTLS smoke remains pending because this host has no `SUMA_SMOKE_TCP_HOST` / `SUMA_SMOKE_TLS_DIR` environment.
- [x] Credential authorization: Git, Registry, and Docker TLS credentials deny all nodes by default; projects require full-target authorization; in-use credentials and grants cannot be deleted. Verification: credential lifecycle tests pass (`go test ./...` green on 2026-08-27).
- [x] Fleet overview and control-plane CD summary: `GET /api/v1/fleet/overview` aggregates every node in parallel (engine info, container/image counts, container CPU/memory totals, latency, live status with graceful degradation) and `GET /api/v1/cd/overview` aggregates delivery projects (release status, drift, runtime health, target nodes) for the global CD plane. The overview page is redesigned in two layers: a control-plane layer (fleet metric cards, node table with per-node resource columns and row-click node switching, CD project list with release/drift states, running-task counter) and a per-node detail layer (host resource cards, container table with CPU/memory columns, engine info with uptime and resource counts). Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [ ] Multi-node web experience: persistent Header node selector, node-aware Query keys, system node management, global CD/Authentication Center, multi-target settings, per-node drift/Attempt/progress views, remediation confirmations, and Task/Audit current-node/control-plane/all switches are implemented. Frontend lint/typecheck/production/demo builds passed on 2026-08-31; interactive Chrome smoke remains pending.
- [ ] Documentation and final verification: V2 scope, deployment/security guidance, API documentation, all Go/web gates, and one Unix plus one mTLS TCP real-Docker smoke environment.

## 上线前验证记录（2026-08-27）

- 后端门禁：`go build ./...`、`go test ./...` 全部通过；此前无测试的 8 个包已补齐离线单元测试：image（拉取流解析/registry 分支）、network、volume、system、settings、config、docker adapter（httptest 假引擎）、monitor。
- 前端门禁：`npm run lint`、`npm run typecheck`、`npm run build` 通过。
- 真实 Docker 冒烟 1（Unix 本地引擎）：`SUMA_RUN_DOCKER_SMOKE=1 go test -tags dockersmoke ./internal/cd` — CD 双版本交付 + 回滚 + 运行时健康校验 PASS（14.1s）。
- 真实 Docker 冒烟 2（TLSVerify 引擎）：本地 dind 容器（docker:27-dind，强制客户端证书认证），`SUMA_SMOKE_TCP_HOST=tcp://127.0.0.1:23760 SUMA_SMOKE_TLS_DIR=<certs> go test -tags dockersmoke ./internal/node` — `TestRealMTLSDockerNode` Ping/Info/List PASS（0.29s）；curl 无证书访问被 TLS 拒绝，确认 mTLS 生效。
- 发现并修复上线阻断项：`docker-compose.yml` 仍使用 `DOCKPORT_*` 变量与 `dockport` 服务名（服务端实际读取 `SUMA_*`）、Makefile 遗留 `DOCKPORT_DEV_API` —— 已全部改为 `SUMA_*` / `suma`。
- 文档同步完成：README/ARCHITECTURE/API/MVP-CHECKLIST/CD-DESIGN/SEMI-MIGRATION/AGENTS/CLAUDE/COMPLETION_REPORT 全部更名至 SUMA 并修正环境变量与路径事实错误。

## Future

- Remote agents and SSH execution.
- Docker Swarm: Docker's official runtime object is a Swarm Stack; in SUMA it will map to `Project backend=swarm`, scoped by Swarm ID. Keep one Projects UI entry and add backend capabilities instead of a parallel Stack menu. No Swarm implementation exists in the current scope.
- Kubernetes.
- LDAP, OIDC, and SSO.
- Git-driven continuous delivery through a Compose deployment adapter. Scope is CD only: SUMA consumes
  deployable Compose configuration and images, and never acts as a CI runner or
  executes repository build/test pipelines.
  - [x] CD foundation: preserve Managed Compose projects; add immutable releases,
    stable Compose project execution specs, project locks, final task/audit results,
    and restart recovery. Verification: service tests and HTTP tests.
  - [x] Generic Git repository model: safe HTTPS/SSH Clone URL validation,
    token/basic authentication, SSH deploy keys with known-host verification,
    custom CA trust, manual sync, deployment, release history, and rollback.
    Verification: adapter, path-escape, credential-redaction, and lifecycle tests.
  - [x] Repository-neutral webhooks: one project URL with automatically detected
    GitHub-compatible, GitLab-compatible, or generic signed payload formats,
    push/tag filtering, and delivery idempotency. Verification: signature, replay,
    repository, and ref tests.
  - [x] Continuous delivery reconciliation: polling fallback, observe/manual/auto
    modes, approvals, drift detection, deployment health gates, optional rollback,
    coalesced updates, and startup reconciliation. Verification: concurrency,
    failure recovery, drift, and real-Docker smoke tests.
  - [x] CD web experience and documentation: a standalone Continuous Delivery
    navigation surface for repository configuration, release history, approvals,
    deployment, drift, rollback, and task progress. Includes Chinese/English copy
    and API/deployment documentation. Verification: lint, typecheck, and production
    build. The later domain-separation phase replaces its original shared Compose
    project catalog.
  - [x] Compose/CD domain separation: introduce an independent Delivery Project
    aggregate, persistence tables, `/delivery-projects` API, creation flow, runtime
    namespace, and credential/release ownership.
    Creating a Compose project never creates or lists a CD project. Verification:
    `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`,
    `npm run build`, and the real-Docker CD delivery/rollback smoke test.
- Prometheus/Grafana and longer-term metrics retention.
- GPU clusters, marketplace, automatic backups, and mobile application.
