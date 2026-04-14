#!/bin/sh
set -eu

mkdir -p deploy/runtime/postgres
mkdir -p deploy/runtime/backups
mkdir -p deploy/runtime/artifacts
mkdir -p deploy/runtime/caddy/data
mkdir -p deploy/runtime/caddy/config

if command -v sudo >/dev/null 2>&1; then
  sudo chown -R 999:999 deploy/runtime/postgres
  sudo chown -R 0:0 deploy/runtime/backups deploy/runtime/artifacts deploy/runtime/caddy
else
  chown -R 999:999 deploy/runtime/postgres
fi

echo "Runtime directories are ready."
