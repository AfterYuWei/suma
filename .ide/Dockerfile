# SUMA 云原生开发环境（CNB 单容器模式）。
# 单容器模式要求镜像内预装 code-server，否则 CNB 会回退到双容器模式；
# 参见 https://docs.cnb.cool/zh/workspaces/double-container.html
FROM golang:1.26-trixie

ENV LANG=C.UTF-8 \
    LANGUAGE=C.UTF-8 \
    GOPROXY=https://goproxy.cn,direct

# 基础工具；openssh-server 支持 VS Code / Cursor 客户端远程接入
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl wget git jq unzip procps openssh-server \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /run/sshd \
    && git config --system --add safe.directory '*'

# code-server（单容器模式必需）与常用扩展
RUN curl -fsSL https://code-server.dev/install.sh | sh \
    && code-server --install-extension cnbcool.cnb-welcome \
    && (code-server --install-extension golang.go \
        && code-server --install-extension bradlc.vscode-tailwindcss \
        || true)

# Node.js 26（Vite 8 / React 19 前端）
RUN curl -fsSL https://deb.nodesource.com/setup_26.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# PostgreSQL 18（本地开发与测试库，由 .cnb.yml 启动流水线初始化）
RUN install -d /usr/share/postgresql-common/pgdg \
    && curl -fsSL -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc \
        https://www.postgresql.org/media/keys/ACCC4CF8.asc \
    && echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt trixie-pgdg main" \
        > /etc/apt/sources.list.d/pgdg.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends postgresql-18 \
    && rm -rf /var/lib/apt/lists/*

# Docker CLI 与 Compose 插件（守护进程由 CNB docker 服务提供）
RUN install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL -o /etc/apt/keyrings/docker.asc https://download.docker.com/linux/debian/gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" \
        > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin \
    && rm -rf /var/lib/apt/lists/*
