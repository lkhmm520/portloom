#!/usr/bin/env bash
set -euo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
for name in portloom-server portloom-sshd; do
  if docker inspect "$name" >/dev/null 2>&1; then
    echo "refusing conflict-guard test: container name $name is already in use" >&2
    exit 1
  fi
done

tmp=$(mktemp -d)
project=portloom-lifecycle-sentinels-$$
cleanup() {
  docker compose -p "$project" -f "$tmp/compose.yml" down >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

cat > "$tmp/compose.yml" <<'EOF'
services:
  server-sentinel:
    image: debian:bookworm-slim
    container_name: portloom-server
    network_mode: none
    command: [sleep, "300"]
  sshd-sentinel:
    image: debian:bookworm-slim
    container_name: portloom-sshd
    network_mode: none
    command: [sleep, "300"]
EOF

docker compose -p "$project" -f "$tmp/compose.yml" up -d >/dev/null
server_before=$(docker inspect -f '{{.Id}}' portloom-server)
sshd_before=$(docker inspect -f '{{.Id}}' portloom-sshd)

set +e
PORTLOOM_TEST_SERVER_IMAGE=${PORTLOOM_TEST_SERVER_IMAGE:-portloom-server:test} \
PORTLOOM_TEST_SSHD_IMAGE=${PORTLOOM_TEST_SSHD_IMAGE:-portloom-sshd:test} \
  "$repo/tests/beginner_compose_lifecycle_test.sh" >"$tmp/probe.log" 2>&1
rc=$?
set -e

[ "$rc" -ne 0 ]
grep -q 'refusing to run: container name portloom-server is already in use' "$tmp/probe.log"
[ "$(docker inspect -f '{{.Id}}' portloom-server)" = "$server_before" ]
[ "$(docker inspect -f '{{.Id}}' portloom-sshd)" = "$sshd_before" ]
[ "$(docker inspect -f '{{.State.Running}}' portloom-server)" = true ]
[ "$(docker inspect -f '{{.State.Running}}' portloom-sshd)" = true ]

echo 'beginner_compose_conflict_guard=ok'
