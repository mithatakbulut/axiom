#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
out=${1:-dist}
mkdir -p "$out"
out=$(cd "$out" && pwd -P)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for arch in arm64 amd64; do
  name="vigil-0.1.0-darwin-$arch"
  mkdir "$stage/$name"
  case "$arch" in arm64) target=arm64 ;; amd64) target=x86_64 ;; esac
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 CGO_CFLAGS="-arch $target" CGO_LDFLAGS="-arch $target" \
    go build -trimpath -buildvcs=false -o "$stage/$name/vigil" ./cmd/vigil
  cp README.md "$stage/$name/"
  mkdir "$stage/$name/configs"
  cp configs/example.yaml "$stage/$name/configs/"
  cp plan.md "$stage/$name/"
  cp -R docs "$stage/$name/"
  tar -C "$stage" -czf "$stage/$name.tar.gz" "$name"
done
(cd "$stage" && shasum -a 256 vigil-*.tar.gz > SHA256SUMS)
mv "$stage"/vigil-*.tar.gz "$stage"/SHA256SUMS "$out/"
