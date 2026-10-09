# Proxysql — SQL over HTTP (PostgreSQL)

`POST /v1/query/sync` 收 SQL（含参数化），由 Go + pgx 连接池转发到 PostgreSQL，SYNC 回傳。

- `base_path` 可配（nginx 子路徑 `/proxysql`）
- 预设 `allowed_statements=[SELECT,INSERT,UPDATE,CREATE]`，`allow_raw_sql=false`
- 超时预设 60s，可配 `default_timeout/max_timeout`

## 快速启动（Docker：建置与运行分离）

```bash
cp .env.example .env   # 按机器改 PROXYSQL_IMAGE（ARM 用 arm64，x64 用 amd64）
make build             # 本机建置（或 make build-amd64 / make build-arm64 指定架构）
make up                # 运行（docker-compose.yml 只运行不建置）
# 经由 nginx:
curl -X POST http://localhost:8081/proxysql/v1/query/sync \
  -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT $1 AS x","params":[1]}'
# 直连:
curl http://localhost:8080/proxysql/healthz
curl http://localhost:8080/proxysql/readyz
make down              # 停止并清理
```

## 镜像版本（多架构）

| Tag | 架构 | 适用机器 |
|-----|------|----------|
| `proxysql:amd64` | linux/amd64 | x64 服务器 |
| `proxysql:arm64` | linux/arm64 | ARM 本机（Mac M 系列） |

```bash
# 各别建置（ARM 本机交叉编译，Dockerfile 用 $BUILDPLATFORM + GOARCH=$TARGETARCH）
make build-amd64
make build-arm64
# 或经 compose 建置档（本机原生架构）
docker compose -f docker-compose.build.yml build

# x64 机器运行
docker run -d -p 8080:8080 \
  -e PROXYSQL_DB__DSN="postgres://user:pass@host:5432/db?sslmode=require" \
  proxysql:amd64

# 合并成单个 multi-arch tag（一条 tag 通吃，机器自动拉对架构，需 registry）
make push REGISTRY=xxx TAG=latest
```

默认 `docker compose build` 按本机架构（ARM 即 arm64）。

## API

`POST {base}/v1/query/sync`

```json
{"sql":"SELECT * FROM users WHERE id=$1","params":[123],"timeout_ms":5000,"max_rows":1000}
{"sql":"INSERT INTO users(name,age) VALUES($1,$2) RETURNING id","params":["alice",30]}
{"sql":"UPDATE users SET age=$1 WHERE id=$2","params":[31,123]}
{"sql":"CREATE TABLE IF NOT EXISTS t(id serial primary key)"}
```

回傳 `{"request_id","columns":[{"name","type"}],"rows","row_count","truncated","duration_ms"}`
错误 `{"error","request_id"}`，状态：400 参数/语法、403 语句不允许、408 超时、429/503 池/DB、499 客户端取消。

裸 SQL 需在 `config.yaml` 设 `query.allow_raw_sql: true`（唯一开关）才放行，
改完 `make down && make up`，见 `examples/raw_sql.sh`。预设严格模式下，
DML（SELECT/INSERT/UPDATE/DELETE）含单引号/`$tag$` 字符串字面量会 `400`
（请改用 `$n` 参数）；数字字面量暂不拦截（`LIMIT 10`、`SELECT 1` 需要它），属已知限制。

参数一律以文本形式送 PG，由 PG 按上下文转型（int/float/bool/uuid/timestamptz/jsonb
均可从文本解析；JSON 对象/数组会压成紧凑 JSON 文本，适合 jsonb 栏位；
原生 PG 数组请传 `'{1,2}'` 文本并视需要 `::$1::int[]` 转型）。

## 配置（一设一处）

`config.yaml` 管全部行为；`PROXYSQL_DB__DSN` 是唯一的 env 項（DSN 帶密碼，
不進 yaml、不進 compose，只住 `.env`——已 gitignore，缺失啟動失敗）。
`PROXYSQL_IMAGE` 只給 compose 選鏡像，不算應用配置。

DSN 只能走 env（其餘 key 改 yaml，无 env 版）：
`PROXYSQL_DB__DSN=postgres://user:pass@host:5432/db?sslmode=require`
