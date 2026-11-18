#!/usr/bin/env bash
set -euo pipefail

go=${1:-go}
out=bin/release/portloomctl
mkdir -p "$out"
# Never leave a previous manifest claiming a partially rebuilt pair is ready.
rm -f "$out/SHA256SUMS"
for arch in amd64 arm64; do
  binary=$out/portloomctl-linux-$arch
  GOOS=linux GOARCH=$arch GOAMD64=v1 GOARM64=v8.0 CGO_ENABLED=0 \
    "$go" build -buildvcs=true -trimpath -ldflags '-s -w' -o "$binary" ./cmd/portloomctl
  info=$("$go" version -m "$binary")
  grep -Fx $'\tpath\tgithub.com/lkhmm520/portloom/cmd/portloomctl' <<<"$info"
  for setting in GOOS=linux "GOARCH=$arch" CGO_ENABLED=0; do
    grep -Fx $'\tbuild\t'"$setting" <<<"$info"
  done
  if [[ -n ${EXPECTED_REVISION:-} ]]; then
    grep -Fx $'\tbuild\tvcs.revision='"$EXPECTED_REVISION" <<<"$info"
    grep -Fx $'\tbuild\tvcs.modified=false' <<<"$info"
  fi
done

# Run the actual packaged binary on native Linux; cross builds are metadata-checked above.
if [[ $("$go" env GOHOSTOS) == linux ]]; then
  case $("$go" env GOHOSTARCH) in
    amd64|arm64)
      help=$("$out/portloomctl-linux-$("$go" env GOHOSTARCH)" --help)
      for verb in plan check status switch settle rollback; do
        grep -Fq "portloomctl $verb --" <<<"$help"
      done
      ;;
  esac
fi
(cd "$out" && sha256sum portloomctl-linux-amd64 portloomctl-linux-arm64 > SHA256SUMS)
