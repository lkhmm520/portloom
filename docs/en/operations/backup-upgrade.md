# Backup, upgrade, and rollback

## Back up

- Server SQLite database together with WAL/SHM;
- Agent `/data/agent.json`;
- SSH private key and verified `known_hosts`;
- secret-safe Compose/environment configuration;
- current NPM upstream mappings.

Use SQLite online backup for zero downtime. Stop the Server before a raw file copy unless database, WAL, and SHM are captured consistently.

## Upgrade

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs --tail=100
```

Upgrade one component at a time: Server, ordinary Web Agent, then high-throughput media Agent. Verify heartbeat, revisions, and public traffic after each step.

## Roll back

Pin the prior image tag and recreate through Compose. A complete ingress rollback also restores previous NPM upstreams; starting an old tunnel container alone is not enough.
