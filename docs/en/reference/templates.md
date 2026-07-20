# Template downloads

Only this explicit public allowlist is copied during a docs build. Real `.env` files, keys, and runtime data never enter the documentation site.

<DownloadCard title="Beginner Server compose.yml" description="Recommended: copy the env template, edit two values, and start" file="compose.yml" />
<DownloadCard title="Beginner Server .env template" description="Rename to .env after downloading" file="compose.env.example" />
<DownloadCard title="Server Compose" description="Run the published Server image" file="docker-compose.server.yml" />
<DownloadCard title="Server environment" description="Copy to server.env and edit" file="server.env.example" />
<DownloadCard title="Advanced: single Agent Compose" description="Manual recovery/expert use; new installs should use the WebUI command" file="docker-compose.agent.yml" />
<DownloadCard title="Advanced: Agent environment" description="Copy to agent.env for manual recovery" file="agent.env.example" />
<DownloadCard title="Advanced: dual Agent Compose" description="Independent Web and media SSH masters" file="docker-compose.dual-agent.yml" />
<DownloadCard title="Web Agent environment" description="Copy to agent-web.env" file="agent-web.env.example" />
<DownloadCard title="Media Agent environment" description="Copy to agent-media.env" file="agent-media.env.example" />
<DownloadCard title="Restricted sshd block" description="Deny shells and permit loopback reverse forwards only" file="sshd_config.portloom.conf" />

::: warning
Beginners should use `compose.yml` and `compose.env.example` from [Compose template installation](/en/guide/compose-install). The commands below apply to the advanced split templates.

For a new Agent, always copy its dedicated command from **Add Agent** in the WebUI. Do not transcribe the one-time token or establish trust with an ad-hoc `ssh-keyscan`. The generated command pins this Server's SSH host key and removes the one-time token after enrollment. Agent templates are for expert deployments or manual recovery with a complete identity backup.

Replace every `change-me` value. A service-level Compose `env_file` does not participate in YAML interpolation, so pass the matching environment file explicitly:

```bash
cp server.env.example server.env
docker compose --env-file server.env -f docker-compose.server.yml config

cp agent.env.example agent.env
docker compose --env-file agent.env -f docker-compose.agent.yml config

cp agent-web.env.example agent-web.env
cp agent-media.env.example agent-media.env
docker compose --env-file agent-web.env --env-file agent-media.env \
  -f docker-compose.dual-agent.yml config
```

Inspect rendered output, but never commit output containing real tokens.

To let the non-root Server bind 80/443, the Server template applies `cap_drop: ALL`, adds back only `NET_BIND_SERVICE`, and intentionally omits `no-new-privileges` for Server; otherwise Linux suppresses the binary's `cap_net_bind_service` file capability. Do not re-add NNP without accounting for that boundary. In production, verify PID 1 remains non-root, `CapEff` contains only `0x400`, and real requests reach 80/443.

Place the templates in a dedicated install directory and set that directory to `0711` (traversable but not listable by other users); do not apply this to an entire home directory. The one-shot `state-init` creates `data/server/certs` in empty bind mounts, transfers Server data and authorization files to UID/GID 65532, and enforces `0700`/`0600`. Removing that initializer or making a parent directory non-traversable prevents the non-root Server from creating SQLite, especially on NAS FUSE paths.
:::
