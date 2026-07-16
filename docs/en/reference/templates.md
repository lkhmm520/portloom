# Template downloads

Only this explicit public allowlist is copied during a docs build. Real `.env` files, keys, and runtime data never enter the documentation site.

<DownloadCard title="Server Compose" description="Run the published Server image" file="docker-compose.server.yml" />
<DownloadCard title="Server environment" description="Copy to server.env and edit" file="server.env.example" />
<DownloadCard title="Single Agent Compose" description="Standard NAS deployment" file="docker-compose.agent.yml" />
<DownloadCard title="Agent environment" description="Copy to agent.env and edit" file="agent.env.example" />
<DownloadCard title="Dual Agent Compose" description="Independent Web and media SSH masters" file="docker-compose.dual-agent.yml" />
<DownloadCard title="Web Agent environment" description="Copy to agent-web.env" file="agent-web.env.example" />
<DownloadCard title="Media Agent environment" description="Copy to agent-media.env" file="agent-media.env.example" />
<DownloadCard title="Restricted sshd block" description="Deny shells and permit loopback reverse forwards only" file="sshd_config.portloom.conf" />

::: warning
Replace every `change-me` value. Inspect `docker compose config`, but never commit rendered output containing real tokens.
:::
