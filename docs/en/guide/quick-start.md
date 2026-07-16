# Five-minute quick start

This page uses published GHCR images. Read [Production deployment](/en/install/production) before exposing a real environment.

## 1. Start the Server

```bash
mkdir -p portloom/server && cd portloom/server
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.server.yml
curl -Lo server.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/server.env.example
mkdir -p data/server
sudo chown -R 65532:65532 data/server
chmod 700 data/server
openssl rand -hex 32
```

Put the generated value in `TM_ADMIN_TOKEN`, then render and start:

```bash
docker compose --env-file server.env -f docker-compose.server.yml config
docker compose --env-file server.env -f docker-compose.server.yml up -d
curl --fail http://127.0.0.1:8080/healthz
```

## 2. Add HTTPS and restricted SSH

Proxy an administration hostname to port `8080`. Point application hostnames to the shared `8081` gateway and preserve `Host`. The VPS `tunnel` account must use `/usr/sbin/nologin`, deny commands and shell sessions, and permit only loopback-bound remote forwarding. See [Production deployment](/en/install/production).

## 3. Prepare and enroll an Agent

Download the templates on the NAS and create the **real key and `known_hosts` file before changing permissions**:

```bash
mkdir -p portloom/agent/{data/agent,secrets} && cd portloom/agent
curl -LO https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/docker-compose.agent.yml
curl -Lo agent.env https://raw.githubusercontent.com/lkhmm520/portloom/main/examples/agent.env.example
ssh-keygen -t ed25519 -a 64 -N '' -f secrets/id_ed25519 -C portloom-agent
ssh-keyscan -p 22 tunnel.example.com > secrets/known_hosts
sudo chown -R 65532:65532 data secrets
chmod 700 data/agent secrets
chmod 600 secrets/id_ed25519
chmod 644 secrets/known_hosts
```

Verify the host fingerprint through a trusted channel. Install `secrets/id_ed25519.pub` in the VPS `tunnel` account's `authorized_keys` using the [restricted template](/en/reference/templates). Create a one-time enrollment token, fill `agent.env`, and start:

```bash
docker compose --env-file agent.env -f docker-compose.agent.yml config
docker compose --env-file agent.env -f docker-compose.agent.yml up -d
docker compose -f docker-compose.agent.yml logs --tail=100 agent
```

After enrollment, remove `TM_ENROLLMENT_TOKEN` and recreate the container.

## 4. Create a route

Select the client, enter a domain and NAS target, and enable the route. Verify Local and Tunnel are up and desired/observed revisions match before testing public HTTPS.
