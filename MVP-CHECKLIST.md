# DockPort MVP Acceptance Checklist

## Product and build

- [x] Product is named DockPort and has a modern, dense, non-template UI.
- [x] React frontend lint, typecheck, and build pass.
- [x] Go server tests and build pass.
- [x] `docker compose up -d` starts DockPort with persistent data.
- [x] Docker socket is mounted locally and is never exposed through TCP 2375.

## Access and overview

- [x] First run creates an administrator.
- [x] Login, session, and logout work securely.
- [x] Overview reads live host resource and Docker information.

## Containers and realtime

- [x] List and inspect containers; start, stop, restart, and remove them.
- [x] Live logs work and release streams on disconnect.
- [x] Terminal supports input/output, bash/sh fallback, resize, and cleanup.
- [x] Stats show CPU, memory, network, block IO, and PIDs.

## Images, networks, and volumes

- [x] List, pull, inspect, tag, and delete images.
- [x] List, create, inspect, and delete networks.
- [x] List, create, inspect, usage-check, and delete volumes.

## Compose

- [x] Create and list projects; edit Compose YAML and `.env`.
- [x] Up, down, restart, pull, build, and update work through `ComposeRunner`.
- [x] Long operations appear in Task Center with realtime logs.

## Operations and UX

- [x] Important actions create audit records.
- [x] Command palette supports search, navigation, and contextual actions.
- [x] Dark, light, and system themes work.
- [x] Every dangerous action has an explicit confirmation mechanism.

## Final verification

- [x] Real-Docker smoke test covers login; container list/start/stop/restart/logs/terminal/stats; image pull/list; network create/delete; volume create/delete; Compose create/edit/up/restart/pull/down; task progress/logs; and audit recording.
- [x] Same-path bind-mount behavior is verified for Compose projects managed through the host Docker socket.
- [x] Smoke-test resources are removed.
- [x] README contains complete development and deployment instructions.
- [x] Final completion report covers all twelve required sections and lists known limitations.
