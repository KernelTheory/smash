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
ssh "$host" "mkdir -p '$dir/releases/$v' smash-stats"
scp dist/releases/"$v"/* "$host:$dir/releases/$v/"
scp dist/install.sh dist/index.html site/fist_logo_header_256.png "$host:$dir/"
# Download stats live in ~/smash-stats, outside the web root; cron records each finished day.
scp deploy/stats-collect.sh "$host:smash-stats/collect.sh"
ssh "$host" "(crontab -l 2>/dev/null | grep -v smash-stats/collect.sh; echo '30 0 * * * sh \$HOME/smash-stats/collect.sh 2>>\$HOME/smash-stats/collect.log') | crontab -"
ssh "$host" "ln -sfn '$v' '$dir/releases/latest' && ls -l '$dir/releases'"
