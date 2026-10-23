#!/bin/sh
# Runs on the download host, daily from cron (installed by scripts/deploy.sh) and on demand from
# scripts/stats.sh. Adds each finished day's smash downloads from the nginx access logs to
# ~/smash-stats/downloads.csv. Recorded days are never recounted, so the history outlives log
# rotation. install.sh downloads with curl and `smash update` with Go's HTTP client, so the user
# agent tells installs from updates. Stores counts only, never IPs.
set -eu
csv="${SMASH_STATS:-$HOME/smash-stats/downloads.csv}"
for f in /var/log/nginx/access.log*; do
	[ -r "$f" ] || { echo "smash stats: cannot read $f (add $(id -un) to the adm group)" >&2; exit 1; }
done
[ -s "$csv" ] || echo "day,installs,updates,other,unique_ips,darwin_arm64,darwin_amd64,linux_arm64,linux_amd64" > "$csv"

zcat -f /var/log/nginx/access.log* | awk -F'"' -v today="$(date +%Y-%m-%d)" '
FILENAME == ARGV[1] { recorded[substr($0, 1, 10)]; next }
$2 ~ /^GET \/releases\/[^ ]+\/smash_[a-z]+_[a-z0-9]+\.tar\.gz / && $3 ~ /^ 200 / {
	split($1, f, " ")
	d = substr(f[4], 2, 11) # 23/Oct/2026
	day = substr(d, 8, 4) "-" sprintf("%02d", (index("JanFebMarAprMayJunJulAugSepOctNovDec", substr(d, 4, 3)) + 2) / 3) "-" substr(d, 1, 2)
	days[day]
	n[day, $6 ~ /^curl\// ? "installs" : $6 ~ /^Go-http-client\// ? "updates" : "other"]++
	if (!((day, f[1]) in seen)) { seen[day, f[1]]; ips[day]++ }
	match($2, /smash_[a-z]+_[a-z0-9]+/)
	n[day, substr($2, RSTART + 6, RLENGTH - 6)]++
}
END {
	for (day in days)
		if (day < today && !(day in recorded))
			printf "%s,%d,%d,%d,%d,%d,%d,%d,%d\n", day, n[day, "installs"], n[day, "updates"], n[day, "other"], ips[day],
				n[day, "darwin_arm64"], n[day, "darwin_amd64"], n[day, "linux_arm64"], n[day, "linux_amd64"]
}' "$csv" - > "$csv.new"
{ head -n 1 "$csv"; { tail -n +2 "$csv"; cat "$csv.new"; } | sort; } > "$csv.tmp"
mv "$csv.tmp" "$csv"
rm -f "$csv.new"
