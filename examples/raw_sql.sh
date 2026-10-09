#!/usr/bin/env bash
# Proxysql raw_sql（非参数化）示例
#
# 前提：config.yaml 须设 query.allow_raw_sql: true（唯一开关，无 env 版），
# 改完重起生效：
#   make down && make up
#   BASE=http://localhost:8080/proxysql ./examples/raw_sql.sh
#
# 对照：数字字面量（如 WHERE age=31）即使严格模式也放行，属已知限制，见 README。
set -u

BASE=${BASE:-http://localhost:8080/proxysql}

q() {
  echo "### $1"
  curl -s -w '\nHTTP:%{http_code}\n' -X POST "$BASE/v1/query/sync" \
    -H 'Content-Type: application/json' -d "$2"
  echo
}

q "CREATE" \
  '{"sql":"CREATE TABLE IF NOT EXISTS raw_demo(id serial primary key, name text, age int)"}'

q "INSERT inline values (no params)" \
  '{"sql":"INSERT INTO raw_demo(name,age) VALUES('"'"'dave'"'"',40) RETURNING id"}'

q "SELECT with string literal (needs allow_raw_sql=true)" \
  '{"sql":"SELECT * FROM raw_demo WHERE name='"'"'dave'"'"'"}'

q "SELECT with numeric literal (passes even in strict mode)" \
  '{"sql":"SELECT * FROM raw_demo WHERE age=40"}'

q "UPDATE inline literal (needs allow_raw_sql=true)" \
  '{"sql":"UPDATE raw_demo SET name='"'"'dave2'"'"' WHERE age=40"}'

q "SELECT verify" \
  '{"sql":"SELECT id, name, age FROM raw_demo WHERE age=40"}'

# 100 行一批：bash 迴圈拼出 VALUES ('b1',1),...,('b100',100)，单句 INSERT 一次送达。
# （行数可调 N；1MB body 上限内 100 行无压力，不需调 max_body_bytes。）
echo "### INSERT bulk 100 rows raw (needs allow_raw_sql=true)"
vals=""
for i in $(seq 1 100); do
  [ -n "$vals" ] && vals="$vals,"
  vals="$vals('b$i',$i)"
done
curl -s -w '\nHTTP:%{http_code}\n' -X POST "$BASE/v1/query/sync" \
  -H 'Content-Type: application/json' \
  -d '{"sql":"INSERT INTO raw_demo(name,age) VALUES '"$vals"'"}'
echo
echo "### SELECT count verify (expect n=101: dave2 + 100x bN)"
curl -s -w '\nHTTP:%{http_code}\n' -X POST "$BASE/v1/query/sync" \
  -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT COUNT(*) AS n FROM raw_demo"}'
echo
