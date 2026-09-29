#!/bin/sh

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
COMPOSE_FILE=${COMPOSE_FILE:-"$SCRIPT_DIR/docker-compose.prod.yml"}
ENV_FILE=${ENV_FILE:-"$SCRIPT_DIR/.env.prod"}
BACKUP_DIR=${BACKUP_DIR:-"$SCRIPT_DIR/backups"}

if [ ! -f "$ENV_FILE" ]; then
  echo "environment file not found: $ENV_FILE" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"
umask 077

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_file="$BACKUP_DIR/dio_wapp-$timestamp.sql.gz"
backup_name=$(basename "$backup_file")
checksum_name="$backup_name.sha256"
raw_file=$(mktemp "$BACKUP_DIR/.dio_wapp-$timestamp.XXXXXX.sql")
compressed_file="$backup_file.tmp"

cleanup() {
  rm -f "$raw_file" "$compressed_file"
}
trap cleanup EXIT HUP INT TERM

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T mysql sh -c \
  'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump -udio_app --single-transaction --quick --routines --triggers --set-gtid-purged=OFF dio_wapp' \
  > "$raw_file"

if [ ! -s "$raw_file" ]; then
  echo "database backup is empty" >&2
  exit 1
fi

gzip -c "$raw_file" > "$compressed_file"
gzip -t "$compressed_file"
mv "$compressed_file" "$backup_file"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$BACKUP_DIR" && sha256sum "$backup_name" > "$checksum_name")
else
  (cd "$BACKUP_DIR" && shasum -a 256 "$backup_name" > "$checksum_name")
fi

echo "$backup_file"
