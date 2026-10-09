REGISTRY ?= xxx
TAG ?= latest

.PHONY: build build-amd64 build-arm64 up down push logs

# 本机原生架构建置（tag 与 docker-compose.yml 的 PROXYSQL_IMAGE 对应）
build:
	docker compose -f docker-compose.build.yml build

build-amd64:
	docker buildx build --platform linux/amd64 -t proxysql:amd64 --load .

build-arm64:
	docker buildx build --platform linux/arm64 -t proxysql:arm64 --load .

# 运行（须先 build，或镜像已存在；见 .env.example 设 PROXYSQL_IMAGE）
up:
	docker compose up -d

down:
	docker compose down -v

logs:
	docker compose logs -f proxysql

# 发布多架构单一 tag（机器自动拉对架构）
push:
	docker buildx build --platform linux/amd64,linux/arm64 -t $(REGISTRY)/proxysql:$(TAG) --push .
