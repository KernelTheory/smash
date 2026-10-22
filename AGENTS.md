# smash: guide for contributors and coding agents

smash is a CLI that removes secrets from shell history and AI coding-agent logs once a day. A local filter finds suspicious lines, a model (OpenAI or Anthropic) returns redacted versions, and smash validates them before rewriting files in place. Read this before changing code; keep it current when a decision changes.

## Architecture
- Go, single package `main`. Dependencies: stdlib, `modernc.org/sqlite` (pure Go, so every target cross-compiles from Docker without cgo) and `github.com/anthropics/anthropic-sdk-go`. OpenAI is called with plain `net/http`.
- Files:
  - `main.go`: CLI dispatch (`smash`, `smash run`, `smash log`, `smash version`, `smash uninstall`), interactive setup and preview.
  - `config.go`: `~/.smash/config` parsing and API-key encryption.
  - `run.go`: sources, incremental state, the scan → model → apply pipeline, SQLite schema.
  - `scan.go`: local suspicious-line filter, snippet windows, response validation (`masked`), JSONL replacement.
  - `history.go`: zsh/bash history parser and encoder, atomic writes.
  - `openai.go`, `anthropic.go`: the two `redactor` implementations sharing one prompt and JSON schema.
  - `schedule.go`: daily job (launchd on macOS, systemd user timer on Linux).
- `smash` (interactive): first run picks a provider and asks for the key (hidden) → preview run (counts only; nothing sent or written) → confirm → real run → offer the daily job. `smash run` is the non-interactive path the daily job uses.
- Sources: shell history (`$HISTFILE`, `~/.zsh_history`, `~/.zhistory`, `~/.bash_history`), `~/.claude/projects/**/*.jsonl`, `~/.codex/sessions/**/*.jsonl`, `~/.claude/history.jsonl`, `~/.codex/history.jsonl`.
- Daily job: macOS → `~/Library/LaunchAgents/com.smash.daily.plist` (10:00, `StartCalendarInterval` catches up after sleep, logs to `~/.smash/launchd.log`). Linux with a systemd user manager (`systemctl --user show-environment` succeeds) → `~/.config/systemd/user/smash.{service,timer}` (`OnCalendar=*-*-* 10:00:00`, `Persistent=true`, logs in `journalctl --user -u smash`; prints the `loginctl enable-linger` hint, never enables it). Otherwise print a crontab line. `smash uninstall` stops both kinds, deletes `jobFiles()`, `~/.smash` and `~/.local/bin/smash` (a symlink there is removed, never its target).

## Database (`~/.smash/smash.db`, mode 600)
- `files(path PK, done, tail)`: bytes processed and sha256 of the 4 KB before `done`. Tail mismatch or `done > size` → rescan from 0; a shell offset not on an entry boundary → rescan.
- `seen(hash PK)`: sha256[:16] of snippets the model found clean and of smash's own redacted output; never resent.
- `settings(name PK, value)`: `config_key`, the random 32-byte AES-256 key for `~/.smash/config`.
- `scans(at, source, path, status, bytes, candidates, redacted, rejected)`: one row per file per real run (`scanned`, `skipped-active`, `changed-during-run`, `error`). Never stores text.

## Logic patterns
- The local filter (`suspicious`) gates what reaches the model: known key prefixes, keyword=value (value not all digits, not starting like code or `$var`), secret flags, bearer/basic auth, URL credentials, `-pSECRET`, and high-entropy tokens (20–200 chars, upper+lower+digit, entropy ≥ 3.5, fewer than 2 slashes, not an object ID such as `toolu_`/`msg_`/`call_`, no date). Tuned on a real ~1 GB backlog: 61k → 5k lines. Keep it broad: a false positive costs tokens, a false negative leaves a secret.
- Snippet = a suspicious line (≤ 2000 bytes) or ±200-byte windows around hits in longer lines; deduped per flush.
- Providers: `PROVIDER=openai` (default; `gpt-6-luna`, Responses API, `reasoning.effort: low`, `store: false`) or `PROVIDER=anthropic` (`claude-haiku-5-5`, Messages API via the official SDK, `output_config.effort: low` + JSON-schema output). Config keys `OPENAI_API_KEY`/`OPENAI_MODEL`/`OPENAI_BASE_URL`, `ANTHROPIC_API_KEY`/`ANTHROPIC_MODEL`/`ANTHROPIC_BASE_URL`. A refusal marks the batch rejected instead of failing the run.
- Requests carry `[{id,text}]`, ≤ 100 items / 100 KB; responses list only redacted items. 3 attempts on 429/5xx/network errors.
- An answer is applied only if `masked(orig, red)`: `red` equals `orig` with ≥ 1 non-empty spans replaced by `[REDACTED]`. Wide spans are accepted (over-masking beats leaving a secret); anything else is `rejected`.
- Apply: shell → replace in the entry's command, re-encode (zsh metafy, backslash-newline continuations). JSONL → replace the JSON-escaped snippet in the raw line (no HTML escaping); the line must stay valid JSON. Untouched bytes are preserved exactly.
- Before writing, re-read the file; if the processed prefix changed, skip (`changed-during-run`), otherwise keep bytes appended meanwhile. Atomic temp file + rename, mode kept, symlinks followed. No backups by design.
- Flush when ≥ 100 snippets or ≥ 64 MB are held. An API error aborts the run before that flush is written; offsets don't advance, so the next run retries.
- JSONL files modified < 1 h ago are skipped (a live session may hold the file open). Shell history is always processed.
- Today is never touched: each file is processed only up to its first record dated on or after local midnight; the next run continues from there. Dates: zsh `: <epoch>:`, bash `#<epoch>`, JSONL top-level `timestamp` (RFC 3339 or epoch ms) or `ts` (epoch s). Undated records count as old. `runner.now` overrides the clock in tests.

## Config encryption
- `~/.smash/config` stores `*_API_KEY=enc:v1:<base64(nonce|AES-256-GCM ciphertext)>` with the variable name as associated data; the key is `settings.config_key`. `PROVIDER`, `*_MODEL` and `*_BASE_URL` stay plain so users can edit them. Plaintext keys (older installs, hand edits) are encrypted in place on the next load; if the key can't be decrypted, `smash` re-runs setup.
- Limit: the key and the ciphertext share `~/.smash` (mode 600). This prevents plain-text exposure (synced or shared configs, grep, secret scanners), not malware running as the same user. A stronger option is the macOS Keychain with a signed binary.

## Distribution
- `install.sh` installs to `~/.local/bin` from `$SMASH_BASE` (default `https://smash.kerneltheory.com`): `releases/<version|latest>/smash_<os>_<arch>.tar.gz`, verified against `checksums.txt`. On a fresh install with a terminal it runs `smash </dev/tty` for first-time setup (stdin is the script pipe); without a terminal or with `SMASH_NO_SETUP=1` it prints a hint; a reinstall keeps the existing setup.
- Releases are annotated git tags. `scripts/release.sh vX.Y.Z` refuses a dirty tree or an untagged `HEAD`, then builds darwin/linux × arm64/amd64 in Docker with `-X main.version` into `dist/` (laid out as served). `scripts/deploy.sh vX.Y.Z` with `SMASH_DEPLOY=user@host:/var/www/smash` uploads releases, `install.sh` and the landing page (`site/index.html`, `__VERSION__` replaced), then repoints `releases/latest`.
- `deploy/server-setup.sh` is the one-time nginx + Let's Encrypt setup for the download host (run with sudo; `SMASH_DOMAIN`, `SMASH_OWNER`).

## Constraints
- Build and test only in Docker (`compose.yaml`, `golang:1.27-trixie`): `docker compose run --rm go go test ./...`.
- Never print real history or transcript lines while debugging; use counts or collapsed character-class shapes.
- Mounting a missing host file with `docker -v` creates an empty directory on the host; avoid or clean it up.
- No server addresses, usernames or personal emails in the repository; deployment targets come from environment variables.
- Known ceilings: no file lock (a writer appending in the milliseconds between re-read and rename loses that line); a log writer that escapes characters differently from Go (e.g. U+2028) makes that snippet count as rejected; offsets advance past rejected snippets.
