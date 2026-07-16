# Architecture

```text
Browser ──HTTPS──> NPM ──Host──> Gateway :8081
                    │                  │
                    │ admin           ▼
                    └────────> Server/SQLite
                                      ▲
                                      │ HTTPS heartbeat
NAS service <── Agent <── OpenSSH -R ─┘
```

The Server stores clients, token verifiers, routes, allocated ports, revisions, and observations in SQLite. The current design expects one Server writer, not active/active replicas.

NPM terminates TLS. The Gateway resolves an enabled HTTP route by Host and proxies to `127.0.0.1:<allocated-port>` on the VPS. Host OpenSSH carries that connection back to the NAS target.

If the control plane is briefly unavailable, a running Agent preserves established forwards and retries. After an Agent restart, the control plane must be reachable to restore desired routes. Failed ControlMasters are rebuilt; failed cancellation keeps the previous observed revision instead of reporting false convergence.

See the deeper repository [architecture document](https://github.com/lkhmm520/portloom/blob/main/docs/architecture.md).
