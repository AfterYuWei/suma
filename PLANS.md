# DockPort MVP Plan

This file records the completed MVP implementation. Every checked phase was verified by automated tests, production builds, or the real-Docker smoke test documented in `COMPLETION_REPORT.md`.

## Completed phases

- [x] Foundation: React 19 TypeScript frontend, layered Go/Gin backend, GORM/SQLite, Docker adapter, health APIs, compact application shell, and themes.
- [x] Authentication: first-run administrator initialization, bcrypt login, hashed opaque sessions, HttpOnly cookie, protected APIs, and logout invalidation.
- [x] Containers: dense searchable list, detail and inspect, lifecycle actions, confirmations, and audit records.
- [x] Realtime containers: cancellable WebSocket logs, xterm.js exec terminal with resize and shell fallback, and ECharts statistics.
- [x] Docker resources: image, network, and volume lifecycle operations with usage checks and confirmations.
- [x] Compose: project lifecycle, Monaco Compose/`.env` editor, validation, changed-file summary, service/log views, and one centralized `ComposeRunner`.
- [x] Operations: persistent tasks and logs, realtime task progress, audit log, confirmed system prune, and settings.
- [x] UX: global command palette, Chinese/English localization, custom accessible dialogs, loading/error/empty states, responsive layout, Base UI primitives, and dark/light/system themes.
- [x] Deployment: multi-stage Dockerfile, Compose deployment, persistent database/project storage, same-path bind mounts, and complete README.
- [x] Verification: backend tests/build, frontend lint/typecheck/build, production image, persistence restart, and complete live-engine smoke suite.

## Frontend redesign

- [x] Immersive operations cockpit: unified Lucide iconography, emoji-free interface, redesigned navigation, authentication, command center, live host canvas, semantic materials, responsive behavior, and reduced-motion support. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Dense Compose project list: compact desktop columns and responsive mobile rows expose path, runtime status, service/container counts, and precise update time without inline action-button clutter. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Expandable Compose operations: independent multi-row expansion, project start/stop/restart/update/down actions, live container status, and per-container start/stop/resume/restart/log/terminal shortcuts. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Container-style Compose list and batch operations: rounded dense resource rows, right-aligned expand controls, multi-select, and batch start/stop/restart/update/down tasks with per-project results. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Unified dense resource lists: Continuous Delivery, images, networks, and volumes now share the rounded high-density list language, responsive rows, semantic icons, compact metadata, empty/error states, and right-aligned actions used by containers and Compose. Verification: `npm run lint`, `npm run typecheck`, and `npm run build`.
- [x] Reliable Compose runtime attribution: correlate containers by Compose working-directory labels rather than assuming the Docker Compose runtime name equals the DockPort project name. Verification: `go test ./...` and `go build ./...`.

## Authentication center

- [x] Reusable Git and registry credential management, encrypted project-owned Git credentials, explicit CD credential sources, bilingual Authentication Center UI, audit coverage, and lifecycle tests. Verification: `go test ./...`, `go build ./...`, `npm run lint`, `npm run typecheck`, `npm run build`, and the real-Docker CD smoke test.

## V2 multi-node Docker management

- [ ] Agentless node registry: mounted `unix://` sockets and `tcp://` Docker APIs, default-node migration, runtime client replacement, duplicate Engine detection, background status probes, and safe reference-aware deletion. Verification: node service/security tests and real-Docker Unix/mTLS smoke test.
- [ ] Node-scoped operations: resource, overview, Compose, system, task, audit, and WebSocket APIs resolve an explicit node; legacy routes remain default-node aliases. Verification: HTTP/WebSocket isolation tests and backend gates.
- [ ] Secure TCP and Compose: mTLS by default, loopback-only plaintext, encrypted TLS credentials, absolute allowlisted remote binds, and private temporary TLS/registry configuration with cleanup. Verification: security and runner cleanup tests.
- [ ] Multi-target CD: immutable target snapshots, parallel per-node deployments, per-node tasks/status/health/failure history, partial-failure status, and failed-node-only rollback. Verification: CD concurrency/recovery tests and multi-target real-Docker smoke test.
- [ ] Credential authorization: Git, Registry, and Docker TLS credentials deny all nodes by default; projects require full-target authorization; in-use credentials and grants cannot be deleted. Verification: credential lifecycle tests.
- [ ] Multi-node web experience: persistent Header node selector, node-aware Query keys, system node management, global CD/Authentication Center, multi-target settings, and per-node Release progress. Verification: frontend gates and browser smoke test.
- [ ] Documentation and final verification: V2 scope, deployment/security guidance, API documentation, all Go/web gates, and one Unix plus one mTLS TCP real-Docker smoke environment.

## Future

- Remote agents and SSH execution.
- Docker Swarm and Kubernetes.
- LDAP, OIDC, and SSO.
- Git-driven continuous delivery through a Compose deployment adapter. Scope is CD only: DockPort consumes
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
