#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE=${COMPOSE_FILE:-"$SCRIPT_DIR/docker-compose.prod.yml"}
ENV_FILE=${ENV_FILE:-"$SCRIPT_DIR/.env.prod"}

if [ "$#" -ne 1 ]; then
  echo "usage: $0 BACKUP.sql.gz" >&2
  exit 2
fi

backup_file=$1
checksum_file="$backup_file.sha256"

if [ ! -f "$ENV_FILE" ]; then
  echo "environment file not found: $ENV_FILE" >&2
  exit 1
fi
if [ ! -f "$backup_file" ]; then
  echo "backup file not found: $backup_file" >&2
  exit 1
fi
if [ ! -f "$checksum_file" ]; then
  echo "backup checksum not found: $checksum_file" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required for restore checks" >&2
  exit 1
fi

backup_dir=$(CDPATH= cd -- "$(dirname -- "$backup_file")" && pwd)
backup_name=$(basename "$backup_file")
checksum_name=$(basename "$checksum_file")

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$backup_dir" && sha256sum -c "$checksum_name")
else
  (cd "$backup_dir" && shasum -a 256 -c "$checksum_name")
fi
gzip -t "$backup_file"

restore_database="dio_restore_$(date -u +%Y%m%d%H%M%S)_$$"
created=0

compose() {
  docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

cleanup() {
  if [ "$created" -eq 1 ]; then
    compose exec -T -e RESTORE_DATABASE="$restore_database" mysql sh -c \
      'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -e "DROP DATABASE IF EXISTS \`$RESTORE_DATABASE\`"' >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT HUP INT TERM

compose exec -T -e RESTORE_DATABASE="$restore_database" mysql sh -c \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -e "CREATE DATABASE \`$RESTORE_DATABASE\` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"'
created=1

gzip -dc "$backup_file" | compose exec -T -e RESTORE_DATABASE="$restore_database" mysql sh -c \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot "$RESTORE_DATABASE"'

table_count=$(compose exec -T -e RESTORE_DATABASE="$restore_database" mysql sh -c \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --batch --skip-column-names "$RESTORE_DATABASE" -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name IN ('\''schema_migrations'\'', '\''users'\'', '\''point_ledgers'\'', '\''goods'\'', '\''exchange_orders'\'')"')
if [ "$table_count" -ne 5 ]; then
  echo "restore check failed: one or more core tables are missing" >&2
  exit 1
fi

schema_version=$(compose exec -T -e RESTORE_DATABASE="$restore_database" mysql sh -c \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --batch --skip-column-names "$RESTORE_DATABASE" -e "SELECT COALESCE(MAX(version), 0) FROM schema_migrations"')
if [ "$schema_version" -lt 1 ]; then
  echo "restore check failed: migration history is empty" >&2
  exit 1
fi

compose exec -T -e RESTORE_DATABASE="$restore_database" mysql sh -c \
  'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqlcheck -uroot --check --silent "$RESTORE_DATABASE"'

echo "restore check passed: $backup_name (schema version $schema_version)"
