# Release acceptance checklist

This page is for maintainers, not an installation prerequisite. Beginner flows should reference only public artifacts that have passed this checklist.

## First-public-release gate

Before the first public release is complete:

- `https://docs.961121.xyz/install-server.sh` and `install-agent.sh` must not be treated as stable installation endpoints;
- the default two-container Server installation cannot work until `ghcr.io/lkhmm520/portloom-sshd:<version>` is published;
- merged documentation and commands alone do not prove that the two-host installation is available.

The first release must publish Server, Agent, sshd, and docs images with the same safe version tag, deploy the docs-hosted scripts, and then pass the post-release checks below.

## Release sequence

1. Push the version tag and let `publish-images` complete its quality gate, publish all four exact-version images, verify commit revisions and both architectures, and compare the installers embedded in the docs image. Existing exact tags are never overwritten; a rerun only fills missing images. This workflow does not wait for external docs deployment and does not move `latest`, major, or minor channels early.
2. Deploy the exact `ghcr.io/lkhmm520/portloom-docs:<version>` image on the authoritative docs host; do not rely only on floating `latest`.
3. Manually run `finalize-release` with the same `v<version>` tag. It re-verifies both architectures' image revisions, production docs, and both installer SHA-256 digests. Only an accepted stable release moves `latest`, major, and minor channels, and replaying an older release cannot downgrade them; prereleases never move them. The GitHub Release is created or validated with an exact commit marker only after those steps finish.
4. If docs deployment or public verification fails, repair the deployment and rerun only `finalize-release`; do not rebuild and republish already verified images.

## Artifact checks after every release

1. Download both scripts from the public docs domain; require successful HTTP responses, non-empty files, and clean `sh -n` results.
2. Confirm that Server, Agent, sshd, and docs GHCR images all have the exact release tag.
3. Run `install-server.sh --version <tag>` and confirm that generated Server and sshd image references use that same tag.
4. Sign in to the WebUI, confirm `/api/v1/system` reports the release version, and confirm the generated Agent command contains the same `--version` value.
5. Run that command on a clean NAS/internal Docker host; confirm Agent enrollment and removal of the one-time token from configuration.
6. Create an HTTP route in the WebUI and verify HTTPS, Host preservation, Local/Tunnel/Public state, and recovery after restarts.
7. Confirm the WebUI cannot create TCP routes and existing TCP metadata is never shown as published/healthy.
8. Perform one pinned upgrade and rollback drill using [Backup, upgrade, rollback](/en/operations/backup-upgrade).

If a critical step fails, stop promoting the release or moving `latest`; repair the artifacts and repeat the full checklist.
