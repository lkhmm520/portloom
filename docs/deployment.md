# Deployment

This guide deploys the server on the DMIT host and the agent on the NAS without changing existing DNS or Nginx Proxy Manager (NPM) automatically. Review every path, port, hostname, and SSH policy before applying it.

## Prerequisites

- Docker Engine with Compose v2 on both hosts
- An existing HTTPS reverse proxy (NPM) on the server
- An OpenSSH server reachable from the NAS
- A dedicated SSH account permitted to create loopback remote forwards
- DNS records already pointing at NPM for the intended HTTP route domains
- The repository checked out on each host

The examples assume admin UI `127.0.0.1:8080`, HTTP gateway `127.0.0.1:8081`, and persistent data under `/opt/portloom`.

## 1. Prepare the server configuration

```sh
cd /path/to/portloom
cp .env.example .env
openssl rand -hex 32
```

Put the generated value in `TM_ADMIN_TOKEN`. Keep both listeners on loopback unless a reviewed firewall and TLS layer provide equivalent isolation:

```dotenv
TM_ADMIN_TOKEN=<64-hex-character-secret>
TM_LISTEN_ADDR=127.0.0.1:8080
TM_GATEWAY_ADDR=127.0.0.1:8081
TM_SERVER_DATA_DIR=/opt/portloom/server
```

Create persistent storage and restrict it to the image's UID/GID:

```sh
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/server
```

Render and inspect the deployment before starting it:

```sh
docker compose -f deploy/server/docker-compose.yml config
```

Build and start only after the rendered configuration is correct:

```sh
docker compose -f deploy/server/docker-compose.yml build
docker compose -f deploy/server/docker-compose.yml up -d
curl --fail http://127.0.0.1:8080/healthz
```

## 2. Configure NPM

Make changes manually and preserve the existing TLS configuration.

### Admin host

Create an access-controlled proxy host such as `tunnel-admin.example.com`:

- Scheme: `http`
- Forward host: the Docker host as reachable by NPM
- Forward port: `8080`
- WebSocket support: optional
- TLS: enabled, force HTTPS
- Additional protection: an NPM access list or VPN is strongly recommended

If NPM itself runs in a container, `127.0.0.1` means the NPM container, not the host. Use a reviewed host-gateway address or bind PortLoom to a private interface instead of opening the port publicly.

### Published HTTP domains

For each published domain, configure NPM to forward HTTP to the gateway on port `8081` and preserve the original `Host` header. NPM continues to own certificates and HTTP-to-HTTPS redirects. Multiple domains can share the gateway because it routes by `Host`.

Do not point the public Internet directly at `8080`, `8081`, or an allocated SSH loopback port.

## 3. Restrict the SSH account

Create a dedicated account on the server and install only the NAS agent's public key. A minimal `sshd_config` match block is:

```text
Match User tunnel
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowTcpForwarding remote
    GatewayPorts no
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
```

`GatewayPorts no` is critical: it keeps remote forwards on loopback. Validate SSH configuration with `sshd -t` before reloading it. Tighten the account further with an `authorized_keys` policy appropriate to your OpenSSH version and allocated port range. Do not reuse an administrator's SSH key.

Generate a key on a trusted system or the NAS:

```sh
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/secrets
sudo -u '#65532' ssh-keygen -t ed25519 -a 64 -f /opt/portloom/secrets/id_ed25519 -C portloom-agent
sudo sh -c 'ssh-keyscan -p 22 tunnel.example.com > /opt/portloom/secrets/known_hosts'
```

If the private key was generated elsewhere, place it at the same path before continuing. Verify the scanned fingerprint against a trusted server-side source before accepting it. The agent container runs as UID/GID `65532`; after generating or placing the files, make that identity the owner and set restrictive permissions:

```sh
sudo chown 65532:65532 /opt/portloom/secrets \
  /opt/portloom/secrets/id_ed25519 \
  /opt/portloom/secrets/known_hosts
sudo chmod 0700 /opt/portloom/secrets
sudo chmod 0600 /opt/portloom/secrets/id_ed25519
sudo chmod 0644 /opt/portloom/secrets/known_hosts
```

Mode `0600` is secure only when ownership matches the container's UID/GID; a root-owned `0600` key is unreadable by the agent. Compose bind-mounts the private key and `known_hosts` read-only.

## 4. Create an enrollment token

Open the HTTPS admin host, sign in with `TM_ADMIN_TOKEN`, choose **Enrollment tokens**, and create a short-lived token (the API enforces a maximum lifetime of 30 days). Copy the secret immediately; it is shown once and consumed by one enrollment.

You may configure routes after the client appears in the UI.

## 5. Prepare the NAS agent

On the NAS checkout, copy `.env.example` to `.env` and set:

```dotenv
TM_SERVER_URL=https://tunnel-admin.example.com
TM_CLIENT_NAME=nas-home
TM_ENROLLMENT_TOKEN=<one-time-secret>
TM_AGENT_DATA_DIR=/opt/portloom/agent
TM_SSH_HOST=tunnel.example.com
TM_SSH_PORT=22
TM_SSH_USER=tunnel
TM_SSH_PRIVATE_KEY_PATH=/opt/portloom/secrets/id_ed25519
TM_SSH_KNOWN_HOSTS_PATH=/opt/portloom/secrets/known_hosts
```

Create state storage:

```sh
sudo install -d -o 65532 -g 65532 -m 0700 /opt/portloom/agent
```

Render, build, and start:

```sh
docker compose -f deploy/agent/docker-compose.yml config
docker compose -f deploy/agent/docker-compose.yml build
docker compose -f deploy/agent/docker-compose.yml up -d
docker compose -f deploy/agent/docker-compose.yml logs --tail=100 agent
```

After the client is enrolled and a client credential is persisted in `/opt/portloom/agent`, remove `TM_ENROLLMENT_TOKEN` from `.env` and recreate the agent container. Never keep a consumed enrollment token in backups or shell history.

## 6. Create and verify a route

In **Routes**, select the enrolled client and configure:

- **HTTP:** name, domain, NAS local host/port, tunnel group, enabled state
- **TCP:** name, NAS local host/port, public metadata port, tunnel group, enabled state

Verify the layers in order:

1. **Local service** becomes healthy after the agent can connect to the NAS target.
2. **SSH tunnel** becomes connected after the remote forward is established.
3. **Public exposure** becomes published after observed and desired revisions match.

For HTTP, test the gateway locally while preserving `Host`:

```sh
curl --fail -H 'Host: media.example.com' http://127.0.0.1:8081/
```

Then test the public HTTPS URL. A successful local target check with a failed gateway request usually indicates SSH or revision state; a successful gateway request with failed HTTPS usually indicates NPM, TLS, or DNS.

## Upgrades

Back up state, pull the reviewed revision, rebuild, and recreate one component at a time:

```sh
docker compose -f deploy/server/docker-compose.yml build --pull
docker compose -f deploy/server/docker-compose.yml up -d
```

Repeat with the agent Compose file. The agent's last desired configuration remains persisted while the server restarts, but monitor tunnel and revision status after every upgrade.

## Backup and restore

Back up:

- server SQLite database and associated state under `TM_SERVER_DATA_DIR`
- agent identity/state under `TM_AGENT_DATA_DIR`
- SSH private key and verified known-host record
- `.env` files through a secret-safe mechanism

For a raw SQLite file copy, stop the server first so the database, WAL, and shared-memory files are consistent. Prefer an online SQLite backup command when zero downtime is required. Test restoration on an isolated host.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| UI rejects token | Exact `TM_ADMIN_TOKEN`, HTTPS proxy headers, server logs |
| Agent cannot enroll | Token expiry/use, `TM_SERVER_URL`, certificate trust, system clock |
| SSH layer offline | Key permissions, pinned host key, account policy, server SSH logs |
| Local layer unhealthy | NAS address from host network, service listener, firewall |
| Public layer pending | desired/observed revisions, agent heartbeat, route enabled state |
| Gateway returns not found | normalized `Host`, enabled HTTP route, NPM preserving Host |
| Gateway returns bad gateway | allocated loopback listener and SSH forward process |
| Container cannot write state | data directory owner UID/GID `65532`, read-only mounts |

Use `docker compose logs` and the layered UI status before restarting services. Avoid deleting SQLite or agent state as a first troubleshooting step.

## Uninstall

Stopping a Compose project does not remove host state:

```sh
docker compose -f deploy/agent/docker-compose.yml down
docker compose -f deploy/server/docker-compose.yml down
```

Remove NPM entries, the dedicated SSH key/account, data directories, and DNS records separately only after confirming they are no longer needed.
