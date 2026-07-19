# Release acceptance checklist

This page is for maintainers. A v0.4 release cannot be accepted by testing only the old HTTPS hostname route.

## Release order

1. Push the exact version tag and wait for `publish-images` to pass quality gates and publish exact dual-architecture Server, Agent, sshd, and docs images.
2. Deploy the same exact docs image on the authoritative host.
3. Run `finalize-release` (or the project's `finalize-vX.Y.Z` marker) to revalidate image revisions, production docs, and installer SHA-256.
4. Move `latest`/major/minor channels and publish the GitHub Release only after all acceptance passes. Fix blockers and rerun finalize; never overwrite an existing exact-version artifact.

## Artifacts and clean install

Set `VERSION` to the exact v0.4 release candidate (for example `0.4.1`) first. Every image, install command, and runtime version below must use that same value.

- [ ] Both public installer URLs return 200, non-empty scripts, and pass `bash -n`.
- [ ] All four `$VERSION` images exist, and dual-architecture revisions match the tag commit.
- [ ] `install-server.sh --version "$VERSION"` uses matching Server/sshd versions and passes real HTTPS `/healthz`.
- [ ] `/api/v1/system` reports `$VERSION` and default `tcp_edge=true`, `udp_edge=true`, `web_port_edge=true`.
- [ ] The generated Agent command uses the same version, and a clean NAS install removes the one-time token from `.env`.
- [ ] A Synology/QNAP-like host covers PATH, Docker daemon, Compose-v2 fallback, and Agent installation without `flock`.

## v0.4 feature acceptance

- [ ] Default HTTPS hostname route: certificate, HTTP 308, Host, WebSocket/Range.
- [ ] Real HTTP route: no certificate issuance and no forced redirect.
- [ ] Same-host paths: longest prefix and Strip path.
- [ ] Custom HTTP/HTTPS public ports: listener, scheme conflict, bind_error.
- [ ] Safe management-host path: allow ordinary HTTPS prefix; reject root, HTTP, custom port, and `/api|/assets|/healthz`.
- [ ] TCP: public byte forwarding, port conflict, disabled stream edge.
- [ ] UDP: bidirectional datagrams, separate source sessions, 60-second expiry, TCP-encapsulation boundary.
- [ ] Dashboard: 60-minute series, totals, Server/Agent CPU/RSS, reset after Server restart; metrics API: per-route counters.
- [ ] Add Agent token deletion: unused command fails while enrolled Agent remains unaffected.

## Upgrade and rollback

- [ ] Upgrade a v0.3.x database to v0.4.0 and verify legacy `http` becomes `https` with unchanged old public behavior.
- [ ] Save consistent `server-data` before upgrade because config backup excludes the database.
- [ ] A failed Server version restores old config/image IDs automatically.
- [ ] Complete a real v0.3.x rollback with the pre-upgrade database.
- [ ] Docs state that Agent installer has no cross-version in-place upgrade, and the sequential maintenance-window flow is tested: `down` the old Agent, install the new Client, delete/recreate routes, complete end-to-end acceptance, and retain the old directory/image IDs for rollback.

Any critical failure blocks finalize and channel movement. Do not hide an artifact defect with manual production edits.
