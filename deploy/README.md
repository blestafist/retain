# Home-server deployment

The Compose stack contains three containers:

- `retain`: the static Go binary and embedded PWA; listens on port 8080 inside Docker only.
- `cloudflared`: the outbound Cloudflare Tunnel connector.
- `litestream`: continuous SQLite WAL replication to Cloudflare R2.

The Retain container has no published host ports. It shares a private backend network with `cloudflared` and `litestream`, plus outbound connectivity for optional LLM and Web Push requests. The SQLite directory must be on local host storage, not NFS/SMB.

Litestream takes a full snapshot every 24 hours and retains snapshots and their WAL history for 30 days. The R2 token needs read/write access (including deletion for Litestream retention enforcement).

## Prerequisites

- Docker Engine with the Compose plugin.
- A Cloudflare zone/domain and a remotely-managed Tunnel.
- An R2 bucket and an R2 API token scoped to that bucket with Object Read & Write permissions.

Copy `.env.example` to `.env`, then set at least:

- `PUBLIC_ORIGIN=https://retain.<your-domain>`
- `TUNNEL_TOKEN`
- `R2_ACCOUNT_ID`, `R2_BUCKET`, `R2_ACCESS_KEY_ID`, and `R2_SECRET_ACCESS_KEY`

Keep `.env` private (`chmod 600 .env`) and never commit it. LLM and VAPID values are optional. The R2 access token is passed only to the Litestream container; the tunnel token is passed only to `cloudflared`.

In Cloudflare Zero Trust, create a Tunnel connector using the token and add a Public Hostname route for `retain.<your-domain>` to `http://retain:8080`. Do not publish port 8080 on the host or enable Cloudflare Access for this hostname; Retain uses its own invite-based authentication and PWA push.

## First boot

The runtime user is UID/GID `65532`. Prepare the bind-mounted directory:

```sh
DATA_DIR="${DATA_DIR:-./data}" # export a custom path here if .env uses a non-default DATA_DIR
install -d -o 65532 -g 65532 -m 0750 "$DATA_DIR"
if [ ! -e "$DATA_DIR/retain.db" ]; then
  # First-ever install only; never create an empty file during disaster recovery.
  install -o 65532 -g 65532 -m 0640 /dev/null "$DATA_DIR/retain.db"
fi
```

For a brand-new installation with no R2 backup yet, the guarded step creates the empty database file once so Litestream can begin replication. **Do not create an empty file during recovery.** If the local database is missing, Litestream must restore it from R2 before Retain starts.

Validate the Compose configuration and start the services:

```sh
docker compose config --quiet
docker compose up -d --build
docker compose ps
docker compose logs -f retain litestream cloudflared
```

Litestream first restores a missing database from R2 with a full SQLite integrity check. If no replica exists, it exits unhealthy instead of silently initializing an empty production database. Retain is held until the database file is present. On first boot Retain applies its embedded migrations. Then create the first administrator:

```sh
docker compose exec retain /app/retain admin create --email admin@example.com
```

Verify `https://retain.<your-domain>/healthz`. The tunnel route and HTTPS hostname must match `PUBLIC_ORIGIN` exactly.

## Cloudflare settings

- Bypass cache for `/api/*`.
- Serve `sw.js`, `push-sw.js`, and `manifest.webmanifest` with `Cache-Control: no-cache`; hashed assets may use `immutable` caching.
- Disable Rocket Loader and JavaScript optimization features that rewrite scripts.
- Set the upload limit to at least 100 MB and allow up to 100 seconds for responses/SSE.
- TLS terminates at Cloudflare. The tunnel is the only route to the origin; do not add a direct host port or another public proxy in front of Retain.

## Updates and operations

Build and roll out the application with:

```sh
docker compose up -d --build
docker compose logs --tail=100 retain litestream cloudflared
```

`/healthz` is the application health endpoint. Container logs go to stdout/stderr and use Docker's bounded `local` log driver. The SQLite database, WAL and SHM files remain under `DATA_DIR`; include this directory in any local host-level backup as an additional recovery option, but R2 is the off-site copy.

After the first successful replication, run a non-destructive restore drill:

```sh
make deploy-restore-check
```

The check restores the latest R2 replica to a temporary file on the shared data volume, runs Litestream's full SQLite integrity check, and removes the temporary files. It does not overwrite the live database. Run this manually after provisioning and periodically thereafter.

### Full recovery from R2

Stop all users of the database first. Preserve the damaged local files for investigation, then move the database and sidecars out of Litestream's configured path so restore cannot be blocked or combined with stale WAL data:

```sh
docker compose stop retain cloudflared litestream
DATA_DIR="${DATA_DIR:-./data}" # use the same custom path as in .env, if applicable
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
for suffix in retain.db retain.db-wal retain.db-shm; do
  if [ -e "$DATA_DIR/$suffix" ]; then
    mv "$DATA_DIR/$suffix" "$DATA_DIR/$suffix.failed-$stamp"
  fi
done
docker compose run --rm --no-deps --entrypoint /usr/local/bin/litestream litestream \
  restore -config /etc/litestream.yml -integrity-check full /data/retain.db
docker compose up -d
```

The restore command reads the same R2 replica configuration as the sidecar. Keep Retain stopped until restore and the integrity check succeed; on startup, the application runs any pending migrations. If R2 has no replica, investigate rather than creating an empty database over the recovery path.

## SQLite / resource notes

The application uses WAL, a 5-second busy timeout, foreign keys, and one write connection. Litestream must share the same local Docker bind mount/kernel as Retain. Do not run multiple Retain or Litestream writers against the same database. The Go runtime receives the IANA time-zone database explicitly for user-local study/reminder scheduling. The 50 MiB idle RSS target is a goal, not a hard container memory limit.
