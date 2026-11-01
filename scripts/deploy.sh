#!/bin/sh
set -eu
umask 077

root_dir=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
env_file="$root_dir/.env"
mail_key_file="$root_dir/.mail-encryption.env"
mode=${1:-postgres}

case "$mode" in
  postgres) compose_file="$root_dir/docker-compose.yml" ;;
  sqlite) compose_file="$root_dir/docker-compose.sqlite.yml" ;;
  *)
    printf 'Usage: sh scripts/deploy.sh [postgres|sqlite]\n' >&2
    exit 2
    ;;
esac

if [ ! -f "$env_file" ] || [ -L "$env_file" ]; then
  printf 'A regular .env file is required. Create it from .env.example and set the required deployment values first.\n' >&2
  exit 1
fi
if [ -L "$mail_key_file" ]; then
  printf 'The dedicated mail-key file must not be a symbolic link.\n' >&2
  exit 1
fi

for command_name in docker openssl curl awk; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf 'Required command not found: %s\n' "$command_name" >&2
    exit 1
  fi
done

if [ -f "$mail_key_file" ]; then
  if ! awk -F= '
    /^MAIL_ENCRYPTION_KEY=/ {
      count++
      value = substr($0, index($0, "=") + 1)
      if (length(value) == 64 && value ~ /^[[:xdigit:]]+$/) valid++
    }
    END { exit !(count == 1 && valid == 1) }
  ' "$mail_key_file"; then
    printf 'The dedicated mail-key file is invalid; it was not changed.\n' >&2
    exit 1
  fi
  chmod 600 "$mail_key_file"
  printf 'A valid dedicated MAIL_ENCRYPTION_KEY is present; preserving it.\n'
else
  temporary_file=$(mktemp "$root_dir/.mail-encryption.env.tmp.XXXXXX")
  trap 'rm -f "$temporary_file"' EXIT HUP INT TERM
  {
    printf 'MAIL_ENCRYPTION_KEY='
    openssl rand -hex 32
  } > "$temporary_file"
  if ! awk -F= '
    /^MAIL_ENCRYPTION_KEY=/ {
      count++
      value = substr($0, index($0, "=") + 1)
      if (length(value) == 64 && value ~ /^[[:xdigit:]]+$/) valid++
    }
    END { exit !(count == 1 && valid == 1) }
  ' "$temporary_file"; then
    printf 'OpenSSL did not generate the expected key format.\n' >&2
    exit 1
  fi
  chmod 600 "$temporary_file"
  mv "$temporary_file" "$mail_key_file"
  trap - EXIT HUP INT TERM
  printf 'Generated a new 32-byte MAIL_ENCRYPTION_KEY without displaying it or reading .env.\n'
fi

cd "$root_dir"
docker compose -f "$compose_file" up -d --build --wait --wait-timeout 180
curl --silent --show-error --fail http://127.0.0.1:8080/api/health >/dev/null
curl --silent --show-error --fail http://127.0.0.1:3000/healthz >/dev/null
docker compose -f "$compose_file" ps
printf 'Deployment and frontend/API health checks succeeded. The encryption key value was not displayed.\n'
