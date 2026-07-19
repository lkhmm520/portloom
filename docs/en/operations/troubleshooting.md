# Troubleshooting

| Symptom | Check first |
| --- | --- |
| Console rejects token | exact `TM_ADMIN_TOKEN`, Bearer header, correct HTTPS management hostname |
| Agent installer cannot find Docker | Synology Container Manager / QNAP Container Station, PATH, current-user permission |
| Compose v2 error | `docker compose version` or standalone `docker-compose version --short` must be v2 |
| Agent enrollment fails | token deleted/expired/used, clock, certificate chain, Server URL has no path |
| Agent rerun identity mismatch | reuse original name/Server/SSH host/port/`--home`; do not overwrite state |
| Local down | target listener, address/port, container network, NAS firewall |
| UDP Local up but no packets | Local means the UDP relay was created; inspect real UDP service, reply path, end-to-end datagrams |
| Tunnel down | key, known_hosts, managed SSH, loopback-port conflict, outbound 2222 |
| Public `waiting_agent` | revision, Tunnel, recent heartbeat (90-second window) |
| Public `conflict` | HTTP/HTTPS share an extra web port, or duplicate TCP/UDP public port |
| Public `bind_error` | occupied port, missing bind IP, low-port capability/permission |
| HTTP/HTTPS 404 | scheme, port, Host, path prefix, enabled/ready state |
| 502 | route matched but SSH loopback upstream is unavailable |
| Custom HTTPS connects but certificate fails | public 80 still reaches primary HTTP edge; DNS points to Server |
| Dashboard has no metrics | `/api/v1/metrics`, generate traffic; counters reset on restart |

## Useful commands

```bash
cd ~/.portloom/server
docker compose --env-file .env -f compose.yml ps
docker compose --env-file .env -f compose.yml logs --tail=200 server
curl -sS http://127.0.0.1:8080/healthz
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
```

When checking occupied ports, include primary edges, 2222, route public ports, and the 20000–29999 loopback pool. Do not troubleshoot by deleting SQLite, Agent identity, private keys, or the install directory. Back up first, then narrow Local → Tunnel/revision → PortLoom edge → DNS/ACME/firewall.

## After installer failure

The Agent installer prints the failed step and bilingual guidance. Fix Docker daemon, Compose, or connectivity and rerun the same command. Without `flock`, it uses `.install.lock.d`; remove that directory only when no installer is running and the previous one was forcibly interrupted.

The Server installer requires `flock`; QNAP/Entware can install it with `/opt/bin/opkg install flock`. After upgrade failure, first verify whether automatic restore completed. Do not manually follow a mutable `latest`.
