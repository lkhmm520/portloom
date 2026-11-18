# Isolated two-node PortLoom cutover

From the repository root, run:

```sh
bash tests/cutover_e2e_test.sh
```

Requires Bash, Go (per `go.mod`), OpenSSL 3, `ssh-keygen`, `timeout`, and a Linux Docker daemon with an available private bridge subnet. The first run may need access to the Docker registry and Debian package mirrors. If Docker's default pools are exhausted, set `PORTLOOM_CUTOVER_SUBNET` to an **unused private subnet** (`/28` or larger). To reuse a locally built image from this checkout's `Dockerfile.sshd`, set `PORTLOOM_CUTOVER_SSHD_IMAGE` to its tag. Otherwise the harness builds the SSHD image from the checkout. The test always builds the Server, Agent, and verifier from the current tree.

The harness creates two independent PortLoom Server/SQLite + managed OpenSSH pairs, two enrolled Agents with separate origins, a writable BIND primary, and an independent verifier. Every container uses one private, internal Docker bridge. No ports are published, no host state or Docker socket is mounted, and only test-scoped certificates, SSH keys, tokens, and `.test` names are used. The verifier resolves business names with its ordinary resolver pointed at BIND. Management names are fixed separately so cutover never moves either control plane.

The test covers two business hosts and three HTTPS mappings, including a nested route that must strip its prefix to reach the correct origin. It checks:

- **Baseline (DNS generation 7):** Source serves all three canaries. The fenced target returns 404 on every business path but serves equivalent preview routes through the real gateway and SSH transport. The fixture edge restricts the preview hosts to the verifier's address.
- **Forward (generation 8):** Both nodes refuse new requests at the break-before-make barrier. Then BIND serves the target address, all three trusted-TLS canaries reach the target by ordinary DNS, and the source refuses all three paths even when dialed directly.
- **Rollback (generation 9):** A new verifier process reopens the same durable journal. It observes both nodes refusing at the rollback barrier; ordinary DNS and all three canaries then return to the source, and the target refuses all three paths.

The production coordinator, gate client, BIND/RFC 2136 adapter, file journal, HTTPS verifier, Server, Agents, and managed SSHD are exercised. The fixture supplies a private CA, a TLS reverse proxy, and deterministic origins. The proxy passes the node header through unchanged: the Server gateway must replace both false origin claims with exactly one `PortLoom-Serving-Node` matching its configured `TM_SERVING_NODE_ID`. Blocked requests must return an unmarked gateway 404. The header, exact response digests, and selected TCP peer identify the serving path. The test does not invoke a production write CLI, obtain ACME certificates, test public DNS propagation, or measure interruption of existing connections. It performs no production cutover.

A failure exits nonzero and prints bounded fixture service logs. The cleanup trap removes only its own containers, network, temporary files, and (if built for this run) image tag. A successful run ends with `cutover_e2e=ok`.
