# Troubleshooting

| Symptom | Check first |
| --- | --- |
| Console rejects token | Exact admin token, Bearer header, HTTPS proxy |
| Agent cannot enroll | token expiry/use, clock, certificate chain |
| Agent loses identity | persistence and permissions of `/data/agent.json` |
| Local down | target listener, address, port, NAS firewall |
| Tunnel down | key readability, known_hosts, SSH policy, port conflict |
| Gateway 404 | Host, protocol, enabled state, normalized domain |
| Gateway 502 | missing VPS loopback listener, disconnected SSH forward |
| Revision pending | heartbeat, desired/observed revisions, last error |

Useful commands:

```bash
docker compose ps
docker compose logs --tail=200 server
docker compose logs --tail=200 agent
curl -sS http://127.0.0.1:8080/healthz
curl -i -H 'Host: app.example.com' http://127.0.0.1:8081/
ssh -vvv -o ExitOnForwardFailure=yes tunnel@tunnel.example.com
```

Do not delete SQLite or Agent state as a first troubleshooting step. Back up first and isolate Local → Tunnel → Gateway → reverse proxy.
