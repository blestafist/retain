#!/usr/bin/env bash
set -euo pipefail

docker compose run --rm --no-deps --entrypoint /bin/sh litestream -ec '
  restore_path="/data/restore-check-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
  trap '\''rm -f "$restore_path" "$restore_path-wal" "$restore_path-shm"'\'' EXIT
  litestream restore \
    -config /etc/litestream.yml \
    -integrity-check full \
    -o "$restore_path" \
    /data/retain.db
  echo "Litestream restore integrity check passed: $restore_path"
'
