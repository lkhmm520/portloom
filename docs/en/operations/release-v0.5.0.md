# PortLoom v0.5.0 release notes

The candidate targets Git tag `v0.5.0` and exact image tag `0.5.0` for Server, Agent, managed sshd, and docs. These pins do not indicate that the artifacts have been published.

## What changed

- `portloomctl plan` compares two Servers' route inventories without changing either Server. The separate Linux `check`, `status`, `switch`, `settle`, and `rollback` commands use an operation configuration and a durable local journal, not a plan report. `check` validates local inputs, not live readiness. After an interruption, inspect `status`; `settle` resolves only the pending step. A nonzero exit does not prove no write occurred.
- Publication gates let an operator freeze or block a whole business hostname and protect its route set during cutover. Enable the gate API explicitly on both Servers and give each Gateway a distinct `TM_SERVING_NODE_ID`. See the [configuration reference](../reference/configuration.md).
- The GitHub Release is expected to include `portloomctl-linux-amd64`, `portloomctl-linux-arm64`, and `SHA256SUMS`. Verify the matching binary against the manifest after downloading it.

## Supported cutover scope

The switch is CLI-only and limited to pre-provisioned, exact business hostnames in one directly authoritative RFC 2136 DNS zone, serving HTTPS on public port 443. Both Server/Agent pairs, matching route inventories, trusted TLS, protected preview endpoints, and per-mapping application canaries must already exist. The CLI coordinates publication and approved A/AAAA records; it does not provision those prerequisites. Once published, see the [CLI operator guide](https://github.com/lkhmm520/portloom/blob/v0.5.0/cmd/portloomctl/README.md) at the release tag.

`switch` does not migrate production Cloudflare-managed DNS or proxied records, raw SSH, TCP/UDP routes, or Agents. Handle production Cloudflare migrations separately and manually. DNS cache lag can cause downtime, and in-flight requests are not drained. The isolated acceptance test does not establish production DNS propagation or live readiness.

## Upgrade and recovery

Stop the old Server and take a consistent backup before upgrading. The first successful gate transition persists a database interlock, even if all gates are later released. Do not run v0.4.6 against gated data or clear the interlock to force a downgrade. Recovery requires a known-good gate-aware Server image, intact Server data, and verification of publication ownership on both nodes before reopening ingress. See [Backup, upgrade, and rollback](./backup-upgrade.md).