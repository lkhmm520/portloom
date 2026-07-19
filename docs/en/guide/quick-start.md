# Five-minute quick start

You need two Linux hosts with Docker Compose: a public VPS and a NAS/internal server, plus a management hostname.

## 0. DNS and firewall

Point the management hostname to the VPS; a wildcard record is optional:

```text
portloom.example.com  A  203.0.113.10
*.example.com         A  203.0.113.10
```

Allow public TCP `80`, `443`, and `2222`. The NAS needs no inbound port. ACME HTTP-01 requires public port 80 to reach Server's HTTP edge; with defaults, local 80/443 must be free. Later, allow every custom web/TCP port you publish, and allow UDP for UDP routes.

## 1. Install Server v0.4

```bash
curl -fsSLo install-server.sh https://docs.961121.xyz/install-server.sh
less install-server.sh
chmod 0700 install-server.sh
./install-server.sh --domain portloom.example.com --version 0.4.0
```

The installer starts `portloom-server` and `portloom-sshd`, verifies the real HTTPS `/healthz`, and prints the WebUI URL and a random administrator token. The stream edge binds `0.0.0.0` by default; append `--disable-tcp-edge` on first install if you do not need TCP/UDP publication.

## 2. Add an Agent in the WebUI

Open `https://portloom.example.com`, enter the administrator token, then open **Add Agent**. Enter the Agent name, HTTPS Server URL, public SSH hostname, and port `2222`; select **Generate command**.

An unused install command can be deleted/revoked from the token list. Already enrolled Agents are unaffected.

## 3. Run the generated command on the NAS

The Agent installer checks Docker daemon access and Compose v2, accepts both `docker compose` and standalone `docker-compose` v2, and handles common Synology/QNAP PATH, hash-tool, and no-`flock` environments. It creates an Ed25519 key, pins the Server host key, enrolls, and removes the one-time token from configuration after success.

If installation fails, fix the reported prerequisite and rerun the **same command** to resume safely. Do not delete `~/.portloom/agent/data`.

## 4. Create the first HTTPS route

Open **Routes → Add route**:

| Field | Example |
| --- | --- |
| Name | Jellyfin |
| Client | home-nas |
| Protocol | HTTPS |
| Public domain | jellyfin.example.com |
| Path prefix | empty |
| Public port | empty (primary HTTPS edge) |
| Local host | `127.0.0.1` or another NAS-reachable address |
| Local port | `8096` |

Wait for Local, Tunnel, and Public to converge, then open `https://jellyfin.example.com`. Add a separate A/AAAA record first if you did not configure wildcard DNS.

## 5. Try v0.4 route capabilities

- Plaintext: select **HTTP**; no certificate or forced redirect is used.
- Sub-path: enter `/jellyfin` and enable **Strip path prefix** if the upstream expects `/`.
- Custom web port: enter a value such as `8443` and allow that TCP port.
- TCP/UDP: select the protocol and enter the required Public port; ensure the stream edge is enabled.

See [Route management](/en/usage/routes) for exact conflicts and [Backup, upgrade, rollback](/en/operations/backup-upgrade) for upgrades.
