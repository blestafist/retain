# Home-server deployment

Retain runs as one Docker Compose service, pulling the prebuilt `ghcr.io/blestafist/retain:latest` image. GitHub Actions builds the static binary and image; the server does not compile anything. SQLite stays on the host in `DATA_DIR`. No Litestream, R2, cloud database, or Tunnel container is used. The existing host `cloudflared` systemd service reaches the app through a loopback-only port binding.

## Prerequisites

- Docker Engine with the Compose plugin.
- The existing Cloudflare Tunnel systemd service and a Cloudflare Zero Trust Public Hostname.

Copy `.env.example` to `.env`, set `PUBLIC_ORIGIN=https://retain.<your-domain>`, and keep `.env` private (`chmod 600 .env`). LLM and VAPID settings are optional.

The first versioned release must be published before the initial pull. The GHCR package must be public for anonymous pulls; if it is private, log in on the server once using a GitHub token with `read:packages` before running Compose.

Set the Tunnel Public Hostname service to `http://127.0.0.1:8080`. Compose binds Retain to `127.0.0.1` only, so the host Tunnel can reach it but other machines cannot connect directly. Do not add a second connector or enable Cloudflare Access for this hostname; Retain uses its own invite-based authentication and PWA push.

## First boot

The container runs as UID/GID `65532`. Create the persistent host data directory with matching ownership. If you customize `DATA_DIR` in `.env`, use the same path below:

```sh
DATA_DIR=./data
sudo install -d -o 65532 -g 65532 -m 0750 "$DATA_DIR"
```

Pull the release image and start Retain:

```sh
docker compose config --quiet
docker compose pull retain
docker compose up -d --no-build
docker compose ps
docker compose logs -f retain
```

On first startup the app creates the SQLite database and applies its embedded migrations. Create the first administrator:

```sh
docker compose exec retain /app/retain admin create --email admin@example.com
```

Verify `https://retain.<your-domain>/healthz`. The Tunnel hostname must match `PUBLIC_ORIGIN` exactly.

## Cloudflare settings

- Route the existing Tunnel to `http://127.0.0.1:8080`.
- Bypass cache for `/api/*`.
- Serve `sw.js`, `push-sw.js`, and `manifest.webmanifest` with `Cache-Control: no-cache`; hashed assets may use `immutable` caching.
- Disable Rocket Loader and JavaScript optimization features that rewrite scripts.
- Set the upload limit to at least 100 MB and allow up to 100 seconds for responses/SSE.
- TLS terminates at Cloudflare. Do not change the Compose binding to `0.0.0.0:8080`; the app port should remain loopback-only.

## Updates and local data

Prepare releases manually after merging changes:

1. Wait for the latest push CI run on `main` to succeed.
2. Open **Actions → Draft release → Run workflow**, select `main`, and enter a version such as `1.2.3` (without `v`).
3. The workflow snapshots the current `main` commit, checks its CI result and that the version is unused, then creates the `v1.2.3` tag and a draft GitHub Release with generated notes. Its run summary links to the draft. If `main` advanced and its CI is still running, wait and run again.
4. Review the notes and **publish the draft manually** on GitHub. This triggers the release workflow, which builds the image for `linux/amd64` and `linux/arm64` and pushes both `v1.2.3` and `latest` to GHCR. Creating a draft alone does not publish an image. No additional token is required.

Existing versions are rejected, not overwritten. If tag creation succeeded but draft creation failed, the tag remains: inspect it before manually removing it or choosing another version. Running this workflow on a branch other than `main` skips release preparation.

No release image is built on the home server. After publishing a release, roll it out with:

```sh
docker compose pull retain
docker compose up -d --no-build
docker compose logs --tail=100 retain
```

The SQLite database and its WAL/SHM files persist under `DATA_DIR` on the server. Compose does not replicate or back up this data remotely; there is no automatic cloud recovery configured.

## SQLite / resource notes

The app uses WAL, a 5-second busy timeout, foreign keys, and one write connection. Keep `DATA_DIR` on local host storage, not NFS/SMB. Do not run multiple Retain containers against the same database. The Go runtime receives the IANA time-zone database explicitly for user-local study/reminder scheduling. The 50 MiB idle RSS target is a goal, not a hard container memory limit.
