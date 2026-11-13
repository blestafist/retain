#!/bin/sh
set -eu

database_path=/data/retain.db
config_path=/etc/litestream.yml

if [ ! -f "$database_path" ]; then
  echo "No local Retain database; attempting a verified Litestream restore" >&2
  litestream restore \
    -config "$config_path" \
    -if-replica-exists \
    -integrity-check full \
    "$database_path"
  if [ ! -f "$database_path" ]; then
    echo "No R2 replica exists; refusing to initialize an empty production database" >&2
    exit 1
  fi
fi

exec litestream replicate -config "$config_path"
