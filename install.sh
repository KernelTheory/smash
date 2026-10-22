#!/bin/sh
# Installs smash into ~/.local/bin.
#   curl -fsSL https://keysmash.website/install.sh | sh
#   curl -fsSL https://keysmash.website/install.sh | sh -s -- v0.1.0
set -eu

main() {
	base="${SMASH_BASE:-https://keysmash.website}"
	version="${1:-latest}"
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	case "$os" in darwin | linux) ;; *) fail "unsupported OS: $os" ;; esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) fail "unsupported CPU: $(uname -m)" ;;
	esac

	file="smash_${os}_${arch}.tar.gz"
	url="$base/releases/$version"
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	echo "Downloading smash ($version, $os/$arch)..."
	curl -fsSL "$url/$file" -o "$tmp/$file" || fail "download failed: $url/$file"
	curl -fsSL "$url/checksums.txt" -o "$tmp/checksums.txt" || fail "download failed: $url/checksums.txt"

	want=$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/checksums.txt")
	if command -v sha256sum >/dev/null 2>&1; then
		got=$(sha256sum "$tmp/$file" | awk '{ print $1 }')
	else
		got=$(shasum -a 256 "$tmp/$file" | awk '{ print $1 }')
	fi
	[ -n "$want" ] && [ "$want" = "$got" ] || fail "checksum mismatch for $file; not installing"

	tar -xzf "$tmp/$file" -C "$tmp" smash
	dir="$HOME/.local/bin"
	mkdir -p "$dir"
	mv -f "$tmp/smash" "$dir/smash"
	chmod 755 "$dir/smash"
	echo "Installed $("$dir/smash" version) to $dir/smash"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "Add it to your PATH:  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc && exec zsh" ;;
	esac

	# First install: run setup now. stdin is this script's pipe, so talk to the terminal directly.
	if [ -f "$HOME/.smash/config" ]; then
		echo "Kept your existing setup (~/.smash/config)."
	elif [ -z "${SMASH_NO_SETUP:-}" ] && (: </dev/tty) 2>/dev/null; then
		echo
		"$dir/smash" </dev/tty || echo "Setup didn't finish; run 'smash' to try again."
	else
		echo "Next: run 'smash' to pick a model provider, add your API key and preview what it would clean."
	fi
}

fail() {
	echo "smash install: $*" >&2
	exit 1
}

main "$@" # everything runs from here, so a partially downloaded script does nothing
