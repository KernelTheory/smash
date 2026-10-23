#!/bin/sh
# Prints smash download counts from ~/smash-stats/downloads.csv on the download host, after
# bringing it up to yesterday with the collector that scripts/deploy.sh installs there.
#   SMASH_DEPLOY=deploy@your-server:/var/www/smash scripts/stats.sh
set -eu
target="${SMASH_DEPLOY:?set SMASH_DEPLOY=user@host:/web/root}"
ssh "${target%%:*}" '[ -f ~/smash-stats/collect.sh ] || { echo "smash stats: collector missing; run scripts/deploy.sh first" >&2; exit 1; }
sh ~/smash-stats/collect.sh && cat ~/smash-stats/downloads.csv' | awk -F, '
NR == 1 { for (i = 6; i <= NF; i++) plat[i] = $i; next }
{
	row[++r] = $0
	for (i = 2; i <= NF; i++) t[i] += $i
}
END {
	if (!r) { print "No smash downloads recorded yet (today is counted tomorrow)."; exit }
	split(row[1], a, ","); split(row[r], b, ",")
	printf "smash downloads, %s to %s (finished days; today is counted tomorrow)\n\n", a[1], b[1]
	printf "all time   installs %d   updates %d   other %d   total %d\n", t[2], t[3], t[4], t[2] + t[3] + t[4]
	printf "platforms "
	for (i = 6; i in plat; i++) printf "  %s %d", plat[i], t[i]
	printf "\n\n%-10s %9s %8s %6s %11s\n", "day", "installs", "updates", "other", "unique IPs"
	for (j = (r > 14 ? r - 13 : 1); j <= r; j++) {
		split(row[j], c, ",")
		printf "%-10s %9d %8d %6d %11d\n", c[1], c[2], c[3], c[4], c[5]
	}
}'
