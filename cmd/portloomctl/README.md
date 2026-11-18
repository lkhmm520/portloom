# Linux cutover CLI

Build from the repository root: `go build -o ./portloomctl ./cmd/portloomctl`.

`plan` is the existing read-only inventory comparison; it uses its own profiles JSON and neither prepares nor approves a switch. The operational commands use a separate, per-operation JSON file. No command creates routes, enrolls Agents, provisions DNS or TLS, creates initial holds, or releases holds.

## Before starting

- Enable `TM_ENABLE_CUTOVER=true` on **both** PortLoom servers and configure their distinct, trusted management hosts (`TM_PUBLIC_HOST`). Use independent HTTPS admin origins, reachable even while the business hosts are blocked. Prepare both route sets, including matching disabled paths. The selected business hosts must be exact main-port HTTPS hosts, with all their mappings owned by the approved client on each node.
- Use the authenticated gate API to provision the entire group on each side under the same operation ID: source `frozen` (serving) and target `blocked` (not serving). Read back and record each starting generation and confirm both nodes' ready Agents. Do not include management or preview hosts in the business group.
- Provision one controlled, directly authoritative RFC 2136 primary, not a resolver or DNS proxy. Seed every selected A/AAAA set with the same approved source addresses and provision the dedicated TXT marker (`portloom-v1:<sha256-of-sorted-lowercase-FQDNs-joined-with-newlines>:<generation>`; include each FQDN's trailing dot and no terminal newline). Restrict HMAC-SHA256 TSIG updates to these exact A/AAAA owners and the marker TXT; support signed nonrecursive queries and prerequisite-only updates. Do not overlap groups or let another system write the records. BIND authoritatively committing an update is distinct from recursive cache and secondary visibility.
- Run from a Linux verifier that uses the intended ordinary DNS path to HTTPS port 443. Configure one exact, nonempty application canary per enabled mapping on **each** node, including separate unshadowed paths for nested mappings. Arrange a separately ACL-protected preview Host/SNI and numeric address/port that uses the same upstream/path rewrite without opening the business gate. Set `TM_SERVING_NODE_ID` on each Server to its configured node ID. The gateway replaces upstream `PortLoom-Serving-Node` and `PortLoom-Serving-Route` claims with its own node and matched route IDs. Preserve those headers through any trusted TLS edge. Public checks require the business mapping ID (`route.ID`); isolated checks require the separately configured preview mapping ID (`preview_route_id`). Both verify trusted TLS, the actual connected peer, expected 2xx status, and SHA-256 of a nonempty body of at most 64 KiB. A CDN, split DNS, proxy-only delivery, cached or mutating canary, and paths needing inline secrets are not supported.
- Use a trusted config directory and ancestors, and a dedicated, **existing** EUID-owned mode-0700 journal directory on durable local storage. The JSON, admin tokens and base64 TSIG secret files must be EUID-owned mode-0600 regular files (not symlinks or hard links). Use one canonical journal directory on one Linux host, keep its lock/config/progress files intact, and serialize other operators/writers. File paths in JSON are relative to the config unless absolute. API `ca_file` adds trust to the system roots; `public_ca_file` or `preview_ca_file` replaces system roots in that mode if explicitly set. Omit optional CA fields for system trust.

[Disabled example: `switch.example.json`](switch.example.json). Copy it to an owned mode-0600 file and replace the sample values. The `route` object is the exact projected route identity: take `ID`, `Host` (domain), `PathPrefix`, `LocalHost`, `TunnelGroup`, `LocalPort`, `RemotePort`, `DesiredRevision`, `Enabled`, and `StripPath` from the authenticated route snapshot for that node. Do not copy route IDs, allocated ports, or revisions from the other node. Set `enabled` to the JSON boolean `true` only after reviewing the complete configuration.

## Run and recover

```sh
./portloomctl check --config /secure/switch.json --operation media-move-1
```

Review the JSON from `check`, then set `PORTLOOM_CONFIRM` to the exact `confirmation` value in your shell. When ready to start or resume forward, run:

```sh
./portloomctl switch --config /secure/switch.json --operation media-move-1 \
  --confirm "${PORTLOOM_CONFIRM:?Set PORTLOOM_CONFIRM from check}"
```

Inspect the journal before deciding how to recover:

```sh
./portloomctl status --journal-dir /secure/journal --operation media-move-1
```

If there is an ambiguous pending intent, settle only that intent with the same confirmation:

```sh
./portloomctl settle --config /secure/switch.json --operation media-move-1 \
  --confirm "${PORTLOOM_CONFIRM:?Set PORTLOOM_CONFIRM from check}"
```

After checking status again, choose either to resume with the `switch` command above or to roll back with the same confirmation:

```sh
./portloomctl rollback --config /secure/switch.json --operation media-move-1 \
  --confirm "${PORTLOOM_CONFIRM:?Set PORTLOOM_CONFIRM from check}"
```

`check` only validates local inputs and prints a confirmation; it makes no network requests, does not prove readiness, and does not start an operation. `status` reads the journal under the operation lock, without reading credentials or making network requests. The digest binds the config bytes, its absolute path and the supplied CA files' content. Preserve those exactly after any intent is recorded. The contents of token/TSIG files are not in the digest; coordinate any required credential rotation without changing the operation's authority or scope. Do not use a different journal or operation ID to bypass recovery.

`switch` starts, or resumes forward with the same config. An operation blocks the old side before promoting the new one; DNS cache lag can cause downtime, and in-flight requests are not drained. An error or interruption may leave a durable pending CAS. `settle` confirms or retries only that CAS; it never starts the following step. `rollback` can accept an already-confirmable forward result but will not send an unresolved forward CAS, and cannot be reversed by a subsequent `switch`. Both directions retain holds. After a successful command, independently check that the inactive edge refuses all business paths and test HTTPS from a representative external resolver/network. `complete` and `rolled_back` are recorded barriers, not continuously verified live state.

Exit codes: `0` command succeeded; `1` blocked, failed or unsettled; `2` invalid invocation or local configuration. No nonzero exit proves the operation had no side effects. Network commands are bounded to two minutes per invocation; timeout can leave pending progress.

## Local test

`PORTLOOM_TEST_BIND=1 go test ./cmd/portloomctl ./internal/cutover -count=1` uses disposable Docker BIND with a random loopback port, TLS listeners, temporary SQLite databases and a built CLI. It exercises a write that commits before the CLI loses authoritative readback, refusal of config drift, and settlement without replay or promotion.
