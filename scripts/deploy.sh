#!/bin/sh
# Uploads a built release, the installer and the landing page, then points releases/latest at it.
#   SMASH_DEPLOY=deploy@your-server:/var/www/smash scripts/deploy.sh v0.1.3
set -eu
v="${1:?usage: SMASH_DEPLOY=user@host:/web/root scripts/deploy.sh v0.1.3}"
target="${SMASH_DEPLOY:?set SMASH_DEPLOY=user@host:/web/root}"
host="${target%%:*}"
dir="${target#*:}"
[ -f "dist/releases/$v/checksums.txt" ] || { echo "run scripts/release.sh $v first" >&2; exit 1; }
sed "s/__VERSION__/$v/g" site/index.html > dist/index.html
ssh "$host" "mkdir -p '$dir/releases/$v'"
scp dist/releases/"$v"/* "$host:$dir/releases/$v/"
scp dist/install.sh dist/index.html "$host:$dir/"
ssh "$host" "ln -sfn '$v' '$dir/releases/latest' && ls -l '$dir/releases'"
