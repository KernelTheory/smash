#!/bin/sh
# Builds release archives in Docker into dist/, laid out exactly as served:
#   dist/install.sh
#   dist/releases/<version>/smash_<os>_<arch>.tar.gz + checksums.txt
set -eu
v="${1:?usage: scripts/release.sh v0.1.0}"
# Every published binary must map to a tagged commit.
[ -z "$(git status --porcelain)" ] || { echo "commit your changes before releasing" >&2; exit 1; }
[ "$(git describe --tags --exact-match 2>/dev/null)" = "$v" ] || { echo "tag HEAD first: git tag -a $v -m $v" >&2; exit 1; }
out="dist/releases/$v"
mkdir -p "$out"
docker compose run --rm -T go sh -c "
set -e
for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
	os=\${t%/*}; arch=\${t#*/}
	mkdir -p /tmp/b/\$os-\$arch
	GOOS=\$os GOARCH=\$arch CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=$v' -o /tmp/b/\$os-\$arch/smash .
	tar -czf $out/smash_\${os}_\${arch}.tar.gz -C /tmp/b/\$os-\$arch smash
done
cd $out && sha256sum smash_*.tar.gz > checksums.txt
"
cp install.sh dist/install.sh
echo "Built $out"
