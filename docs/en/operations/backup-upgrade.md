# Backup, upgrade, and rollback

## Back up

The easy installer uses `~/.portloom/server` by default. Back up at least:

- `server-data/`: the SQLite database, WAL/SHM, and the ACME cache under `certs/`;
- `ssh-hostkeys/`: the Server SSH identity pinned by Agents;
- `ssh-auth/`: the currently generated Agent authorization file;
- `.env` and `compose.yml` (they contain sensitive data; protect backups with mode 0600/0700);
- for a v0.2.x migration, `Caddyfile`, `caddy-data/`, and `caddy-config/`;
- each Agent's `/data/agent.json`, SSH private key, and verified `known_hosts`.

Use SQLite online backup for zero downtime. Before copying `server-data/` directly, stop Server or capture the database, WAL, and SHM consistently. Do not copy only `portloom.db`, and do not omit `server-data/certs/` or `ssh-hostkeys/`.

## Routine upgrade

For manually maintained Compose deployments, pin the new image tags and upgrade one component at a time:

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100
```

Upgrade Server first, then ordinary Web Agents, then high-throughput media Agents. Verify heartbeat, revisions, HTTPS, and public traffic after each step.

### Upgrade a v0.3 native-edge easy install

For an easy install already using the v0.3 native edge, rerun the installer with the original domain and ports plus a pinned target version whose image reference differs from the current one:

```bash
./install-server.sh --domain portloom.example.com --version 0.3.1
```

To keep rollback valid when mutable tags such as `latest` are involved, an idempotent rerun with unchanged image references does not pull them again; upgrades must use a new pinned version. When image tags change, the installer fully generates candidate files before creating `native-upgrade-backup-0.3.1/` (the suffix is the target version), containing the pre-upgrade `.env` and `compose.yml`. The new version succeeds only after a real HTTPS `/healthz` check. On failure, the installer restores old Compose and the canonical files and verifies old HTTPS. If automatic restoration cannot be verified, inspect the service manually as reported. The backup directory remains after success; archive or remove it after the release is stable.

## Migrate a v0.2.x easy install from Caddy to the v0.3.0 native edge

The old easy install includes Caddy. A plain `docker compose pull/up` neither replaces its generated files nor releases ports 80/443. Back up the complete installation directory, then request the migration explicitly:

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
chmod 0700 install-server.sh
./install-server.sh \
  --domain portloom.example.com \
  --version 0.3.0 \
  --migrate-native-edge
```

Use the original domain, Web/SSH/Gateway ports, and the old Caddy 80/443 ports, plus a pinned v0.3 `--version` whose Server image reference differs from the old one. The installer rejects replacing a legacy deployment through the same mutable `latest` reference so rollback still points to the old image. If old Caddy uses custom local ingress ports, prefix the command with matching `PORTLOOM_HTTP_PORT` and `PORTLOOM_HTTPS_PORT` values; public 80/443 must be NAT/forwarded to those local ports, and HTTP redirects include the custom HTTPS port. The installer:

1. refuses a legacy Caddy installation unless `--migrate-native-edge` is explicit;
2. fully generates candidate files, then atomically creates a mode-0700 `migration-backup-v0.3.0/` directory containing the original `.env`, `compose.yml`, and `Caddyfile`;
3. generates the new two-service `portloom-server` plus `portloom-sshd` configuration;
4. stops old Caddy before starting the native edge, then starts with `--remove-orphans`;
5. waits for Server health and makes a real loopback HTTPS request to the management hostname. It reports success only after certificate issuance, the 443 listener, and `/healthz` work. On failure it stops the new configuration, restores old Compose and the three canonical files, and verifies old HTTPS. If automatic restoration cannot be verified, the installer reports that explicitly and requires manual service inspection.

Verify the migration:

```bash
cd ~/.portloom/server
docker compose ps
docker compose logs --tail=100 server
curl -I http://portloom.example.com
curl -I https://portloom.example.com
```

Require `portloom-caddy` to be absent, HTTP to redirect to HTTPS, and the WebUI certificate to be valid. Create one HTTP route and verify hostname-specific issuance and forwarding.

## Roll back the v0.3.0 native-edge migration

If automatic restoration fails or a manual rollback is required, stop the new configuration and restore the three installer backups:

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml down --remove-orphans
cp migration-backup-v0.3.0/.env .env
cp migration-backup-v0.3.0/compose.yml compose.yml
cp migration-backup-v0.3.0/Caddyfile Caddyfile
docker compose --env-file .env -f compose.yml up -d
docker compose ps
```

Keep `server-data/`, `ssh-hostkeys/`, `ssh-auth/`, `caddy-data/`, and `caddy-config/`; never empty them during rollback. After old Caddy owns 80/443 again, verify the WebUI and existing routes.

## Ordinary version rollback

Pin the previous image tags and recreate from the backed-up Compose configuration. Back up before database changes. An ingress rollback must also restore the previous public ingress and upstream mapping; starting an old tunnel container alone is insufficient.
