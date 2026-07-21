# Backup, upgrade, and rollback

## Backup scope

The easy Server install defaults to `~/.portloom/server`. Before upgrade, preserve at least:

- `server-data/`: SQLite, WAL/SHM, and `certs/`;
- `ssh-hostkeys/`: the SSH identity pinned by Agents;
- `ssh-auth/`, `.env`, and `compose.yml`;
- the **complete install directory** of every Agent, normally `~/.portloom/agent/`, including `data/`, `.env`, `compose.yml`, and the pinned `PORTLOOM_AGENT_IMAGE_ID`;
- for a v0.2 Caddy migration: `Caddyfile`, `caddy-data/`, `caddy-config/`, and the exact image ID of every old running Compose container.

Agent `data/` alone is not installer-restorable. The installer fails closed when credentials exist without `.env` and `compose.yml`. Preserve original 0700/0600 permissions and verify all files in an isolated backup location. Stop Server before a raw SQLite copy, or use SQLite online backup while keeping the database/WAL/SHM consistent. Never copy only `portloom.db`.

Before migration, record the image IDs actually used by current containers:

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps -q \
  | xargs -r docker inspect --format '{{.Name}} {{.Image}}'
```

::: danger v0.4 database and Agent compatibility
The first v0.4 start adds route fields/indexes and converts early legacy `http` rows to `https` to preserve old automatic-TLS behavior. A v0.3.2 Agent accepts only `http|tcp` and rejects migrated `https` routes; a v0.3.2 Gateway cannot publish those rows either.

Installer `native-upgrade-backup-*` directories contain only `.env` and `compose.yml`; they **do not back up the database**. A v0.3.x rollback must restore a consistent pre-v0.4 `server-data/` snapshot as well as old images.
:::

## v0.3.x → v0.4.x requires a maintenance window

Current `install-agent.sh` supports same-version continuation/recovery only and fails closed when an existing home receives another `--version`. The WebUI also locks Client ownership while editing a route. Therefore v0.4.x has **no installer-managed, one-command, zero-downtime Agent cross-version upgrade**. Do not upgrade Server first and leave old Agents running for long; do not delete `agent.json` or keys or guess immutable image IDs.

The conservative procedure below causes a short interruption. Commands assume the default Agent home; replace it with the real absolute path for custom homes:

1. Quiesce writes; back up Server and every complete Agent install directory; record all route fields, old ports, and image IDs.
2. From the old Agent home, run Compose `down` to remove the fixed-name old container while preserving bind-mounted data:

   ```bash
   cd ~/.portloom/agent
   docker compose --env-file .env -f compose.yml down
   ```

3. Upgrade Server with the original hostname, Server home, and ports.
4. After v0.4 Server is ready, create a new name under **Add Agent** and install a fresh v0.4 Agent into a different `--home`. The old container must already be down because generated Compose fixes `container_name: portloom-agent`.
5. The WebUI cannot change an existing route's Client. Record and delete each old endpoint, recreate the same configuration on the new Agent, wait for Local/Tunnel/Public convergence, and run real end-to-end tests.
6. Keep the pre-upgrade database, complete old Agent home, and image IDs through the rollback window.

If uninterrupted cutover is required, wait for a tested Agent cross-version transaction instead of improvising an image-ID switch.

## Server upgrade command

Download the current installer, repeat original options, and pin the new release:

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain example.com --version 0.4.1
```

A non-default install must repeat every original value. Gateway has no CLI flag and uses an environment variable:

```bash
PORTLOOM_GATEWAY_PORT=<original-gateway-port> \
./install-server.sh \
  --domain example.com \
  --home <original-install-directory> \
  --web-port <original-web-port> \
  --ssh-port <original-ssh-port> \
  --http-port <original-http-edge-port> \
  --https-port <original-https-edge-port> \
  --version 0.4.1
```

The installer resolves and persists immutable image IDs, writes candidate configuration, creates `native-upgrade-backup-0.4.1/`, runs Compose `up -d`, and requests the real HTTPS `/healthz`. Failure restores previous configuration and image IDs. An existing backup directory with the same name blocks another attempt.

On the first migration from a v0.3 install with no stream-edge value, `--disable-tcp-edge` may write `off`. A non-empty value in an existing `.env` is preserved, so later rerun flags are not a general toggle; back up and review the installed `.env`/Compose before changing it. When stream edge is enabled, allow only actual route ports—not a broad range.

After upgrade:

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=200 server
curl -I https://example.com/healthz
```

Confirm old web rows appear as HTTPS after migration, `/api/v1/system` reports 0.4.1, Dashboard metrics render, and test true plaintext HTTP, HTTPS, TCP, UDP, path, and extra-port routes.

## Migrating a v0.2.x Caddy install

```bash
./install-server.sh \
  --domain example.com \
  --version 0.4.1 \
  --migrate-native-edge
```

Repeat the original hostname and all original ports. Back up Caddy volumes, complete Server data, and old Agent state, and record exact image IDs for the old Server/sshd/Caddy containers. The installer saves old `.env`, `compose.yml`, and `Caddyfile`, stops Caddy, starts native edges, and verifies HTTPS. That automatic configuration backup contains neither the database nor old images. Public port 80 must reach the configured HTTP edge.

## Rollback

Preserve a failed-state copy before any rollback, then stop current Compose. Never erase `server-data`, `ssh-hostkeys`, `ssh-auth`, or Agent data.

- **v0.4 → v0.3.x:** restore `.env` and `compose.yml` from `native-upgrade-backup-0.4.1/` and a consistent pre-upgrade `server-data/`. Pass the recorded immutable IDs through both the v0.3 `PORTLOOM_*_IMAGE` contract and the v0.4 `PORTLOOM_*_IMAGE_ID` contract so a locally moved tag cannot be selected:

  ```bash
  OLD_SERVER_IMAGE_ID=sha256:replace-with-recorded-server-id
  OLD_SSHD_IMAGE_ID=sha256:replace-with-recorded-sshd-id
  for image_id in "$OLD_SERVER_IMAGE_ID" "$OLD_SSHD_IMAGE_ID"; do
    printf '%s\n' "$image_id" | grep -Eq '^sha256:[0-9a-f]{64}$' || {
      echo 'invalid old image ID' >&2
      exit 1
    }
  done
  docker image inspect "$OLD_SERVER_IMAGE_ID" "$OLD_SSHD_IMAGE_ID" >/dev/null
  PORTLOOM_SERVER_IMAGE="$OLD_SERVER_IMAGE_ID" \
  PORTLOOM_SSHD_IMAGE="$OLD_SSHD_IMAGE_ID" \
  PORTLOOM_SERVER_IMAGE_ID="$OLD_SERVER_IMAGE_ID" \
  PORTLOOM_SSHD_IMAGE_ID="$OLD_SSHD_IMAGE_ID" \
    docker compose --env-file .env -f compose.yml up -d --pull never
  ```

  Stop and `down` the new Agent, then start the old Agent from its complete old install directory with `--pull never`:

  ```bash
  OLD_AGENT_HOME=/absolute/path/to/complete-old-agent-install
  case "$OLD_AGENT_HOME" in /*) ;; *) echo 'OLD_AGENT_HOME must be absolute' >&2; exit 1;; esac
  test -f "$OLD_AGENT_HOME/.env" && test -f "$OLD_AGENT_HOME/compose.yml" || {
    echo 'OLD_AGENT_HOME is not a complete Agent install directory' >&2
    exit 1
  }
  cd "$OLD_AGENT_HOME"
  docker compose --env-file .env -f compose.yml up -d --pull never
  ```

- **v0.2 Caddy migration rollback:** restore `.env`, `compose.yml`, and `Caddyfile` from `migration-backup-v0.3.0/`, retained Caddy volumes, **a consistent pre-migration `server-data/`, and matching old Agent state**. Pin every image reference in old Compose to the recorded pre-migration `sha256:` ID, verify each locally with `docker image inspect <sha256-ID>`, then run from the original project directory:

  ```bash
  docker compose --env-file .env -f compose.yml config
  docker compose --env-file .env -f compose.yml up -d --pull never
  ```

  Abort if any old image or pre-migration database backup is unavailable and restore it from a trusted archive. Never pull a moved `latest` as an old stack. Finally verify 80/443, WebUI, existing routes, and old Agent heartbeats. Installer automatic edge-configuration restore is not a database rollback.
