# Release acceptance checklist

This page is for maintainers, not an installation prerequisite. Beginner flows should reference only public artifacts that have passed this checklist.

## First-public-release gate

Before the first public release is complete:

- `https://docs.961121.xyz/install-server.sh` and `install-agent.sh` must not be treated as stable installation endpoints;
- the default three-container Server installation cannot work until `ghcr.io/lkhmm520/portloom-sshd:<version>` is published;
- merged documentation and commands alone do not prove that the two-host installation is available.

The first release must publish Server, Agent, sshd, and docs images with the same safe version tag, deploy the docs-hosted scripts, and then pass the post-release checks below.

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
