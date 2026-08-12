# DockPort API

All REST responses use `{ "code": 0, "message": "success", "data": ... }`. Errors use a nonzero code and an appropriate HTTP status. Authentication uses the `dockport_session` HttpOnly cookie.

## REST `/api/v1`

- `GET /health`, `GET /docker/info`
- `GET /auth/status`, `POST /auth/initialize`, `POST /auth/login`, `POST /auth/logout`, `GET /auth/session`
- `GET /containers`, `GET /containers/:id`, `POST /containers/:id/{start|stop|restart|pause|unpause|kill}`, `PATCH /containers/:id`, `DELETE /containers/:id`
- `GET /images`, `GET /images/:id`, `POST /images/pull`, `POST /images/:id/tag`, `DELETE /images/:id`
- `GET|POST /networks`, `GET|DELETE /networks/:id`
- `GET|POST /volumes`, `GET|DELETE /volumes/:id`
- `GET|POST /compose`, `GET|PUT|DELETE /compose/:name`, `GET /compose/:name/services`, `GET /compose/:name/logs`, `POST /compose/:name/validate`, `POST /compose/:name/{up|down|start|stop|restart|pull|build|update}`
- `GET /overview` for live host CPU, memory, disk, network, uptime, platform, and Docker summary data
- `GET /tasks`, `GET /tasks/:id/logs`, `POST /tasks/:id/cancel`
- `GET /audit-logs`, `GET|PUT /settings`
- `POST /system/prune` starts a confirmed task for unused containers, networks, dangling images, and anonymous volumes

## WebSocket

- `/ws/containers/:id/logs` streams timestamped UTF-8 log chunks.
- `/ws/containers/:id/stats` streams Docker Stats JSON samples.
- `/ws/containers/:id/terminal` streams binary terminal output and accepts binary input or JSON `{type:"input",data}` / `{type:"resize",cols,rows}`.
- `/ws/tasks/:id` replays retained task logs and streams progress/status events.

All WebSockets require the same session cookie as REST. Disconnecting cancels the underlying context and closes Docker streams or exec sessions.
