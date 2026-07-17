# Five-minute quick start

You need two Linux hosts with Docker Compose: a public VPS and a NAS or internal server. You also need a domain name.

## 0. Prepare DNS and the firewall

Point the management hostname to the VPS:

```text
portloom.example.com  A  203.0.113.10
```

If all services use the same parent domain, add one wildcard record:

```text
*.example.com  A  203.0.113.10
```

Allow TCP `80`, `443`, and `2222` on the VPS. The NAS needs no inbound port. ACME HTTP-01 validation requires public access to port 80, and no other process may own ports 80/443.

## 1. Install Server on the public host

Download and inspect the script before running it:

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com
```

The installer starts two containers:

- `portloom-server`: WebUI, API, route gateway, and the native public HTTPS edge on ports 80/443;
- `portloom-sshd`: a restricted SSH endpoint used only by PortLoom.

Server uses autocert with ACME HTTP-01 to obtain and renew certificates, persisting its cache at `/data/certs` in the Server data directory. The installer grants Server the `NET_BIND_SERVICE` capability required to bind ports 80/443. It does not install Caddy, Nginx, or NPM.

It prints the WebUI URL and a random administrator token when finished.

## 2. Open the WebUI

Open `https://portloom.example.com` and enter the administrator token. Go to **Add Agent**, enter the Agent name, HTTPS Server URL, public Server hostname, and SSH port `2222`, then click **Generate command**.

## 3. Install Agent on the NAS

Paste the complete generated command on the NAS or internal Docker host. The installer creates an Ed25519 key, pins the Server host key, enrolls once, uploads the Agent public key, and removes the one-time enrollment token after success.

The new host appears under Clients within a few seconds.

## 4. Add the first route

Open **Routes → Add HTTP route** and enter:

| Field | Example |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTP |
| Public domain | jellyfin.example.com |
| Local host | 127.0.0.1 or a LAN service address |
| Local port | 8096 |

Wait until local and tunnel status are green, then open `https://jellyfin.example.com`.

If you did not create wildcard DNS, point the route hostname to the VPS separately. The native edge authorizes certificates only for `TM_PUBLIC_HOST` and enabled HTTP route hostnames; unknown names are denied.

See [Install with Docker](/en/install/docker) for files and upgrades. Use [Production deployment](/en/install/production) when integrating an existing ingress or auditing every Compose setting.
