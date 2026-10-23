#!/bin/sh
# Counts smash downloads in the download host's nginx access logs (current and rotated).
# install.sh fetches the tarball with curl and `smash update` with Go's HTTP client, so the
# user agent tells installs from updates. Counting runs on the server; IPs are never printed.
#   SMASH_DEPLOY=deploy@your-server:/var/www/smash scripts/stats.sh
set -eu
target="${SMASH_DEPLOY:?set SMASH_DEPLOY=user@host:/web/root}"
ssh "${target%%:*}" sh -s <<'EOF'
set -eu
zcat -f /var/log/nginx/access.log* | awk -F'"' '
$2 ~ /^GET \/releases\/[^ ]+\/smash_[a-z]+_[a-z0-9]+\.tar\.gz / && $3 ~ /^ 200 / {
	split($1, f, " ")
	ip = f[1]
	d = substr(f[4], 2, 11) # 23/Oct/2026
	day = substr(d, 8, 4) "-" sprintf("%02d", (index("JanFebMarAprMayJunJulAugSepOctNovDec", substr(d, 4, 3)) + 2) / 3) "-" substr(d, 1, 2)
	if (first == "" || day < first) first = day
	if (day > last) last = day
	k = $6 ~ /^curl\// ? "installs" : $6 ~ /^Go-http-client\// ? "updates" : "other"
	n[k]++
	if (!((k, ip) in seen)) { seen[k, ip]; u[k]++ }
	if (!(ip in all)) { all[ip]; people++ }
	match($2, /smash_[a-z]+_[a-z0-9]+/)
	p = substr($2, RSTART + 6, RLENGTH - 6)
	if (!(p in plat)) order[++np] = p
	plat[p]++
	total++
}
END {
	if (!total) { print "No smash downloads in the logs."; exit }
	printf "smash downloads, %s to %s (as far back as the logs go)\n\n", first, last
	printf "%-10s %9s %11s\n", "", "downloads", "unique IPs"
	printf "%-10s %9d %11d   install.sh (curl)\n", "installs", n["installs"], u["installs"]
	printf "%-10s %9d %11d   smash update\n", "updates", n["updates"], u["updates"]
	printf "%-10s %9d %11d   browsers, scripts\n", "other", n["other"], u["other"]
	printf "%-10s %9d %11d\n\n", "total", total, people
	printf "platforms:"
	for (i = 1; i <= np; i++) printf " %s %d", order[i], plat[order[i]]
	print ""
}'
EOF
