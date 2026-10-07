SHELL := /bin/sh
GO_CACHE ?= /tmp/suma-go-cache
DEV_HOST ?= 0.0.0.0
DEV_WEB_PORT ?= 5173
DEV_API_PORT ?= 8081

.DEFAULT_GOAL := help

.PHONY: help install local-config dev web-dev server-dev check web-check server-check build web-build server-build docker-up docker-down docker-logs db-up db-down

help: ## 显示可用命令
	@awk 'BEGIN {FS = ":.*## "; printf "SUMA commands:\n\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## 安装前端依赖并下载 Go 模块
	cd web && npm ci
	cd server && go mod download

local-config: ## 创建本地私密配置模板（保留已有文件）
	@if [ ! -e .env.local ]; then (umask 077; set -C; cat .env.local.example > .env.local); fi
	@echo 'Edit .env.local with your PostgreSQL credentials; make dev and tests read it automatically.'

dev: ## 同时启动前后端开发服务（Ctrl+C 停止）
	$(MAKE) --no-print-directory -j2 web-dev server-dev

web-dev: ## 启动 Vite 前端开发服务
	cd web && SUMA_DEV_API=http://127.0.0.1:$(DEV_API_PORT) npm run dev -- --host $(DEV_HOST) --port $(DEV_WEB_PORT)

server-dev: ## 启动 Go 后端开发服务
	cd server && GOCACHE=$(GO_CACHE) SUMA_ADDRESS=:$(DEV_API_PORT) go run -buildvcs=false ./cmd/suma

check: web-check server-check ## 执行所有质量检查

web-check: ## 前端 lint、类型检查和构建
	cd web && npm run lint
	cd web && npm run typecheck
	cd web && npm run build

server-check: ## 后端测试和构建检查
	cd server && GOCACHE=$(GO_CACHE) go test ./...
	cd server && GOCACHE=$(GO_CACHE) go build -buildvcs=false ./...

build: web-build server-build ## 构建前端和后端

web-build: ## 构建前端生产资源
	cd web && npm run build

server-build: ## 构建后端二进制到 server/bin/suma
	mkdir -p server/bin
	cd server && GOCACHE=$(GO_CACHE) go build -buildvcs=false -o bin/suma ./cmd/suma

docker-up: ## 构建并后台启动生产容器
	docker compose up -d --build

docker-down: ## 停止生产容器（保留持久化数据）
	docker compose down

docker-logs: ## 持续查看 SUMA 容器日志
	docker compose logs -f suma

db-up: ## 启动本机开发 PostgreSQL（仅 loopback 端口）
	docker compose -f docker-compose.yml -f compose.dev.yml up -d postgres

db-down: ## 停止开发 PostgreSQL，保留数据卷
	docker compose -f docker-compose.yml -f compose.dev.yml stop postgres
