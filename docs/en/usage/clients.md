# Clients and enrollment

## Add Agent and enrollment tokens

Open **Add Agent** and click **Generate install command**:

1. Agent name is 1–64 characters, starts with a letter or digit, and may contain only letters, digits, `.`, `_`, and `-`;
2. use an HTTPS public Server URL and fill Public Server host plus SSH tunnel port;
3. the WebUI offers 1, 6, or 24 hours (the API maximum is 30 days);
4. copy the full command immediately. The plaintext token appears once and cannot be recovered after closing the modal, signing out, or signing in again;
5. the command pins the current Server version and prefers `curl`, falling back to `wget`.

A token lasts at most 30 days and can complete enrollment once. Server stores only a verifier. In the **Add Agent** token table, locate the ID, click that row's **Delete**, and verify the confirmation dialog:

- deleting an unused token immediately invalidates a command that has not run;
- deleting a used/expired row only removes that token record;
- the long-lived identity, SSH key, and operation of an already enrolled Agent are unaffected;
- there is no separate revoked row; after `DELETE /api/v1/enrollment-tokens/{id}` succeeds, the record disappears.

## First start and safe retry

Agent generates a request ID and long-lived Agent token, atomically writes `/data/agent.json.pending`, and submits the claim with the one-time token. Server stores only the long-lived token verifier. The installer removes the pending claim and the one-time token from `.env` only after `/data/agent.json` is durable.

The same claim is retry-safe. If the enrollment response is lost, Agent can recover the same Client with the same request ID without consuming another token. Fix an installer error and rerun the same command; do not delete state or invent a new Agent name as a recovery method.

The v0.4 Agent installer checks Docker daemon access and Compose v2, supports both `docker compose` and standalone `docker-compose` v2, handles common NAS PATHs and SHA-256 tools, and falls back to a directory lock without `flock`. It performs a token-free restart and waits for heartbeat before reporting success.

## Backup and isolation

Back up the complete Agent install directory `~/.portloom/agent/`, not only `data/`. Restoring the same install requires at least:

- `.env` and `compose.yml`: install configuration and pinned immutable image ID;
- `data/agent.json`: Client ID and long-lived credential;
- `data/ssh/id_ed25519`: Agent SSH private key;
- `data/ssh/known_hosts`: pinned Server host key.

For example, create a permission-restricted complete archive:

```bash
umask 077
tar -C "$HOME/.portloom" -czf "$HOME/portloom-agent-backup.tgz" agent
```

With only `data/` and no `.env`/`compose.yml`, the installer fails closed rather than treating the state as a directly restorable complete install.

Never share one state directory between Agents. Use two Clients, install directories, and enrollment tokens when web and media traffic need separate SSH master connections.
