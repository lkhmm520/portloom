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

## NAS-local cutover preflight

Run `portloomctl plan` from the NAS to compare route inventory on two PortLoom Servers before a VPS cutover. Use the matching v0.5.0 Linux binary from the GitHub Release after it is published (verify it against `SHA256SUMS`), or build it from the candidate source:

```bash
go build -o ./portloomctl ./cmd/portloomctl
```

Save `profiles.json` in a private directory on the NAS. Replace the example origins, management hostnames, and business hostnames with your own:

```json
{
  "profiles": {
    "old": {
      "url": "https://old-api.example.net",
      "management_host": "old-console.example.net",
      "token_file": "old.token"
    },
    "new": {
      "url": "https://new-api.example.net",
      "management_host": "new-console.example.net",
      "token_file": "new.token",
      "ca_file": "new-private-ca.pem"
    }
  },
  "groups": {
    "media": { "hosts": ["photos.example.net", "video.example.net"] }
  }
}
```

Put each Server's admin bearer token in its own regular file, with no group or other access (for example, `chmod 600 old.token new.token`). Keep tokens out of JSON, URLs, command-line arguments, and version control. File paths are absolute or relative to `profiles.json`. API URLs must be distinct HTTPS origins without credentials, a path prefix, a query, or a fragment. If a Server needs a private CA, supply its trusted PEM file via `ca_file`; otherwise omit that field. The CLI verifies API TLS, connects directly without ambient proxies, and does not follow redirects.

`groups` is optional. A mapping group is just a named, nonempty list of exact business DNS hostnames (no wildcards, IPs, URLs, ports, or paths). Names are 1–64 ASCII characters, starting with a letter or digit and continuing with letters, digits, `.`, `_`, or `-`. Select a name with its exact spelling; do not define names that differ only by case. All group definitions are validated before API calls. Hostnames are normalized, deduplicated, and sorted. The same group can be reused with different Server pairs: profile names and enrolled Client **IDs** are always explicit. Replace `source-client-id` and `target-client-id` with the IDs from the corresponding Server's client inventory.

```bash
./portloomctl plan --config ./profiles.json --source old --target new \
  --source-client source-client-id --target-client target-client-id --group media
```

For a one-off preflight, replace `--group media` with repeated `--host` flags; do not combine the two selectors:

```bash
./portloomctl plan --config ./profiles.json --source old --target new \
  --source-client source-client-id --target-client target-client-id \
  --host photos.example.net --host video.example.net
```

The plan sends only admin inventory `GET` requests to `/api/v1/clients` and `/api/v1/routes`. It checks whole hostnames: every matching source row, including disabled rows and separate paths on the same host, appears in `routes`. Scope is HTTPS on public port 443. Check `blockers` for missing clients or hosts, management/API hostname overlap, other source owners, unsupported endpoints, invalid local targets, or any existing target web route on a selected host (even if disabled or on a different path).

| Exit | Meaning |
| --- | --- |
| `0` | JSON `status: "preview"`: no inventory blockers found. |
| `1` | JSON `status: "blocked"`: inspect `blockers`; if inventory could not be read, the error is on stderr instead. |
| `2` | Invalid invocation or local configuration; no inventory report. |

Check the JSON `group` (present only with `--group`), expanded `hosts`, and `routes` against your intended scope. Keep reports private if they contain internal targets. **A preview is not readiness or authorization to cut over.** Inventory reads are not an atomic snapshot, and the group name and report do not pin inventory with a hash or compare-and-swap. `checks` marks SSH, service TLS, public reachability, and DNS as `not_performed`. Verify those separately before any change. `plan` always reports `execute: "unavailable"`; there is no `apply` command. The preflight does not change routes, Agents, services, or DNS. The separate Linux `switch` workflow uses its own operation configuration; it cannot apply a plan. See [v0.5.0 release notes](./release-v0.5.0.md) for its prerequisites and limits.

## v0.4.6 → v0.5.0 Server upgrade

Use a maintenance window for an existing installer-managed v0.4.6 stack. Quiesce traffic and administrative writes before the backup, and keep them quiesced until you verify the upgrade. Do not run two Servers against the same database. Until v0.5.0 artifacts are published, these are release-candidate instructions. After the first successful publication-gate transition, do not use v0.4.6 for recovery; see the interlock section below.

Download the installer to a working directory outside the install home:

```bash
curl -fsSLo install-server.sh https://docs.look4i.com/install-server.sh && chmod 0700 install-server.sh
```

In the same shell, set and export `SERVER_HOME` (the original absolute install path), `SERVER_DOMAIN`, and the five port variables below from the original `.env`. For a default install, the home is `$HOME/.portloom/server` and the Web, SSH, Gateway, HTTP, and HTTPS ports are `8080`, `2222`, `8081`, `80`, and `443` respectively. Use the original values for a custom install. Do not source `.env`. Run the following from the download directory with Docker access and `sudo` access to archive files owned by the containers:

```bash
(
  set -eu
  umask 077
  : "${SERVER_HOME:?Set the original absolute install path}" \
    "${SERVER_DOMAIN:?Set the original domain}" "${SERVER_WEB_PORT:?Set the original Web port}" \
    "${SERVER_SSH_PORT:?Set the original SSH port}" "${SERVER_GATEWAY_PORT:?Set the original Gateway port}" \
    "${SERVER_HTTP_PORT:?Set the original HTTP port}" "${SERVER_HTTPS_PORT:?Set the original HTTPS port}"
  case "$SERVER_HOME" in /*) ;; *) echo 'SERVER_HOME must be absolute' >&2; exit 1;; esac
  installer="$PWD/install-server.sh"
  test -x "$installer"
  cd "$SERVER_HOME"
  test -f .env && test -f compose.yml || exit 1
  compose() { docker compose --env-file .env -f compose.yml "$@"; }
  sudo -v
  backup_dir=$(mktemp -d "$HOME/portloom-server-v0.4.6.XXXXXXXX")
  for service in server sshd; do
    container=$(compose ps -q "$service")
    test -n "$container"
    docker inspect --format '{{.Name}} {{.Image}}' "$container"
  done > "$backup_dir/images.txt"
  compose stop server sshd
  sudo tar -cpf "$backup_dir/server-install.tar" -- .env compose.yml server-data ssh-hostkeys ssh-auth
  sudo tar -df "$backup_dir/server-install.tar" >/dev/null
  printf 'Backup: %s\n' "$backup_dir"
  compose start server sshd
  PORTLOOM_GATEWAY_PORT="$SERVER_GATEWAY_PORT" "$installer" \
    --domain "$SERVER_DOMAIN" --home "$SERVER_HOME" --web-port "$SERVER_WEB_PORT" \
    --ssh-port "$SERVER_SSH_PORT" --http-port "$SERVER_HTTP_PORT" \
    --https-port "$SERVER_HTTPS_PORT" --version 0.5.0
)
```

`stop` leaves the old containers in place for the offline backup. `start` resumes those same containers without recreating them or pulling images. This is required: the installer refuses stopped containers even if their immutable IDs match. Let the installer replace the running old containers; do not run `down` or start a separate Server. If the backup or its verification fails, the command stops before `start` and before upgrading; resolve the backup failure before continuing. Protect the archive (it contains credentials and SSH keys), and retain the old images identified in `images.txt` for recovery.

The installer resolves and persists immutable image IDs, writes candidate configuration, creates `native-upgrade-backup-0.5.0/`, runs Compose `up -d`, and requests the real HTTPS `/healthz`. On activation failure, the installer attempts to restore the previous configuration and restart with the previous image IDs; it does not restore Server data. An existing backup directory with the same name blocks another attempt.

On the first migration from a v0.3 install with no stream-edge value, `--disable-tcp-edge` may write `off`. A non-empty value in an existing `.env` is preserved, so later rerun flags are not a general toggle; back up and review the installed `.env`/Compose before changing it. When stream edge is enabled, allow only actual route ports—not a broad range.

After upgrade:

```bash
(
  set -eu
  cd "${SERVER_HOME:?Set the original absolute install path}"
  docker compose --env-file .env -f compose.yml ps
  docker compose --env-file .env -f compose.yml logs --tail=200 server
  curl -fsS "https://${SERVER_DOMAIN:?Set the original domain}:${SERVER_HTTPS_PORT:?Set the original HTTPS port}/healthz"
)
```

Confirm old web rows appear as HTTPS after migration, `/api/v1/system` reports 0.5.0, Dashboard metrics render, and test true plaintext HTTP, HTTPS, TCP, UDP, path, and extra-port routes.

## Migrating a v0.2.x Caddy install

```bash
./install-server.sh \
  --domain example.com \
  --version 0.5.0 \
  --migrate-native-edge
```

Repeat the original hostname and all original ports. Back up Caddy volumes, complete Server data, and old Agent state, and record exact image IDs for the old Server/sshd/Caddy containers. The installer saves old `.env`, `compose.yml`, and `Caddyfile`, stops Caddy, starts native edges, and verifies HTTPS. That automatic configuration backup contains neither the database nor old images. Public port 80 must reach the configured HTTP edge.

## Rollback

Preserve a failed-state copy before any rollback, then stop current Compose. Never erase `server-data`, `ssh-hostkeys`, `ssh-auth`, or Agent data.

### Server databases that have used publication gates

Do not downgrade a database that has used publication gates. The first successful gate transition installs a persistent SQLite interlock; releasing every gate does not remove it. A v0.4.6 Server pointed at that database exits before opening its listeners and logs `PortLoom: publication-gated database; restart with a gate-aware Server`. The interlock does not stop a legacy process that was already running: stop the old Server before an upgrade and never share the database between Server processes.

If a downgrade is refused, stop the attempted old stack, including its managed SSH service. Preserve the complete Server data, including any SQLite sidecar files, and leave cutover DNS, peer publication gates, and Agent assignments alone. Restore the recorded immutable image and matching configuration of the last working Server **with this interlock**, using the same unmodified data directory. Check the expected gate phase and generation on both Servers and verify that blocked hosts are still unreachable through the recovered node before reopening ingress. If you cannot identify the image or verify publication ownership, keep the affected ingress and managed SSH isolated. Never drop the trigger or gate table, clear gate rows, or point v0.4.6 at a copy of gated data. A pre-gate backup can contain routes that have since moved to the peer; restoring it does not make those routes safe to advertise.

- **v0.4 → v0.3.x:** restore `.env` and `compose.yml` from `native-upgrade-backup-0.4.6/` and a consistent pre-upgrade `server-data/`. Pass the recorded immutable IDs through both the v0.3 `PORTLOOM_*_IMAGE` contract and the v0.4 `PORTLOOM_*_IMAGE_ID` contract so a locally moved tag cannot be selected:

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
