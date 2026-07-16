# Install with Docker

## Requirements

- Docker Engine 24+ and Compose v2;
- OpenSSH on the Server host;
- outbound HTTPS and SSH from the NAS;
- a reverse proxy able to reach ports `8080` and `8081`;
- data directories writable by UID/GID `65532`.

## Pull images

```bash
docker pull ghcr.io/lkhmm520/portloom-server:latest
docker pull ghcr.io/lkhmm520/portloom-agent:latest
docker pull ghcr.io/lkhmm520/portloom-docs:latest
```

Pin release tags in production instead of tracking `latest` indefinitely.

## Compose templates

Use the files under [`examples/`](https://github.com/lkhmm520/portloom/tree/main/examples) or download them from [Templates](/en/reference/templates). Render before applying:

```bash
docker compose --env-file server.env -f docker-compose.server.yml config
docker compose --env-file server.env -f docker-compose.server.yml up -d
```

The supplied deployments run as non-root, use read-only root filesystems, drop all Linux capabilities, and do not mount the Docker socket.

## Build from source

```bash
git clone https://github.com/lkhmm520/portloom.git
cd portloom
make docker-build VERSION=local
docker build -f Dockerfile.docs -t portloom-docs:local .
```
