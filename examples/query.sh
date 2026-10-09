#!/usr/bin/env bash
# Proxysql SYNC 查询示例：CREATE / INSERT / SELECT / UPDATE / DELETE
# 用法：
#   BASE=http://localhost:8080/proxysql ./examples/query.sh  # 直连
#   BASE=http://localhost:8081/proxysql ./examples/query.sh  # 经 nginx
set -u

BASE=${BASE:-http://localhost:8080/proxysql}

q() {
  echo "### $1"
  curl -s -w '\nHTTP:%{http_code}\n' -X POST "$BASE/v1/query/sync" \
    -H 'Content-Type: application/json' -d "$2"
  echo
}

q "CREATE" \
  '{"sql":"CREATE TABLE IF NOT EXISTS demo(id serial primary key, name text, age int)"}'

q "INSERT" \
  '{"sql":"INSERT INTO demo(name,age) VALUES($1,$2) RETURNING id","params":["alice",30]}'

q "SELECT" \
  '{"sql":"SELECT * FROM demo WHERE name=$1","params":["alice"]}'

q "UPDATE" \
  '{"sql":"UPDATE demo SET age=$1 WHERE name=$2","params":[31,"alice"]}'

q "SELECT(verify update)" \
  '{"sql":"SELECT id, name, age FROM demo WHERE name=$1","params":["alice"]}'

# 注意：预设 allowed_statements=[SELECT,INSERT,UPDATE,CREATE]，DELETE 会回 403。
# 要放行 DELETE，请在 config.yaml 的 allowed_statements 加上 "DELETE" 后重起。
echo "### DELETE (preset returns 403)"
curl -s -w '\nHTTP:%{http_code}\n' -X POST "$BASE/v1/query/sync" \
  -H 'Content-Type: application/json' \
  -d '{"sql":"DELETE FROM demo WHERE name=$1","params":["alice"]}'
echo
