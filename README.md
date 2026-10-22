# smash

**Keep API keys out of your shell history and AI agent logs.**

Every `export OPENAI_API_KEY=…`, `curl -H "Authorization: Bearer …"` or `mysql -pPassword` you've ever typed is still sitting in plain text in `~/.zsh_history`. If you use Claude Code or Codex, every key you pasted into a chat, and every secret a command printed, is also in their session logs under your home folder. Anything that runs as your user can read all of it.

smash scrubs those secrets once a day, in place, without touching the rest of your history. You keep your past commands, minus the credentials.

```sh
curl -fsSL https://keysmash.website/install.sh | sh
```

> **Before you run it:** smash permanently rewrites your shell history and Claude Code / Codex session logs, with no backups, and sends suspicious lines to OpenAI or Anthropic. Read [Risks](#risks) first.

```text
before:  export OPENAI_API_KEY=sk-proj-9fK2mQx7Lp4Vz8Rt3Nw6Yb1H…
after:   export OPENAI_API_KEY=[REDACTED]
```

## How it works

1. **Find locally.** smash reads only what's new since its last run and picks out suspicious lines on your machine: known key formats (`sk-…`, `ghp_…`, `AKIA…`, `xoxb-…`, JWTs, private keys), `KEY=value` and `--password` style arguments, bearer tokens, credentials in URLs, and long random-looking strings. On a real 1 GB backlog this cut 61,000 candidate lines to about 5,000.
2. **Ask a model.** Only those suspicious lines are sent, in batches, to the provider you chose: OpenAI (`gpt-6-luna`) or Anthropic (`claude-haiku-5-5`). The model returns just the lines that contain a secret, with the secret replaced by `[REDACTED]`.
3. **Verify.** A returned line is accepted only if it equals the original with some spans replaced by `[REDACTED]` and nothing else changed. A model can't rewrite, drop or reorder your commands.
4. **Rewrite in place.** History and log files are rewritten atomically, byte-for-byte identical except for the redactions. Anything appended while smash was working is kept.

Then it does this again every day in the background.

## Risks

smash changes files you may care about. Read this before your first run.

- **Redaction is permanent.** smash keeps no backups, on purpose: a backup would be another copy of the secret. If the model masks something that wasn't a secret, that text is gone too.
- **Resuming old agent sessions may break.** smash edits Claude Code and Codex session logs in place, and those tools replay a log when you resume a session. Anthropic's API can reject, or drop the model's earlier reasoning from, a conversation whose earlier messages were edited, so resuming an older Claude session that smash redacted may fail or lose context. A resumed session also sees `[REDACTED]` where the value was. smash never touches today's lines or sessions active in the last hour, and never changes encrypted or signed data such as Codex's `encrypted_content`, Claude's thinking signatures or inline images.
- **Suspicious lines go to a third party.** The lines the local filter flags, which can include real secrets, are sent to OpenAI or Anthropic to be confirmed. OpenAI requests are sent with `store: false`; check your provider's data policy if that matters to you.
- **It can miss secrets.** The local filter and the model are both heuristics, so a secret in an unusual format may survive. **Rotate any key you find in your history:** if it was on disk, assume it was read.
- **It isn't protection against malware.** Anything running as your user can read your history before smash cleans it, and can read smash's encrypted config together with the database that holds its key.
- **Open shells can write old lines back.** A running shell keeps its history in memory and may save it again when it exits. Restart open terminals after a run.

## What it cleans

| Source | Location |
| --- | --- |
| zsh / bash history | `~/.zsh_history`, `~/.zhistory`, `~/.bash_history`, `$HISTFILE` |
| Claude Code sessions | `~/.claude/projects/**/*.jsonl` |
| Codex sessions | `~/.codex/sessions/**/*.jsonl` |
| Prompt histories | `~/.claude/history.jsonl`, `~/.codex/history.jsonl` |

## Features

- **One command to install.** A single static binary for macOS and Linux (Apple Silicon, Intel, x86-64, ARM64). The installer verifies the SHA-256 checksum, installs to `~/.local/bin` (no sudo) and starts first-time setup.
- **Preview before anything leaves your machine.** The first run shows how many suspicious lines it found per source and asks before sending.
- **Daily background job.** launchd on macOS, a systemd user timer on Linux. Both catch up on a run missed while your machine was asleep or off.
- **Incremental.** After the first run, only content added since the previous run is checked. Lines a model already judged clean are never sent again.
- **Today stays untouched.** Lines dated today, in your timezone, are left alone and cleaned on the next day's run, so the commands you're working with right now stay intact.
- **Live sessions are left alone.** Claude and Codex logs modified in the last hour are skipped and picked up later.
- **Two providers.** OpenAI or Anthropic, chosen at setup and switchable in the config.
- **API key encrypted at rest.** The provider key in `~/.smash/config` is stored as AES-256-GCM ciphertext; the encryption key lives in smash's local database.
- **A log you can check.** `smash log` shows which files were scanned, with how many lines were suspicious, redacted or rejected. It never stores the text itself.
- **Live progress.** In a terminal, scans show a progress line with a little secret-shredding animation. The background job logs plain text.
- **Self-update.** `smash update` installs the latest release after verifying its checksum.
- **Clean uninstall.** `smash uninstall` removes the background job, the config, the database and the binary.

## Usage

```sh
smash             # first-time setup, or preview + confirm a run now
smash run         # scan and redact now, without prompts (what the daily job runs)
smash log         # recent scans: file, status, suspicious / redacted / rejected counts
smash update      # install the latest release (checksum verified)
smash version
smash uninstall   # remove smash completely (asks first)
```

### First run

1. Choose a provider: **OpenAI** (default) or **Anthropic**.
2. Paste your API key. The input is hidden, and the key is saved encrypted.
3. smash previews what it found, for example:
   ```text
   SOURCE   FILES  TO SCAN   SUSPICIOUS LINES  SKIPPED (ACTIVE)
   claude   35     87.0 MB   1433              2
   codex    73     108.0 MB  4113              2
   shell    1      0.7 MB    255               0
   ```
4. Confirm to send those lines and redact. Answer `n` and nothing is sent or changed.
5. Accept the daily job if you want smash to keep things clean from now on.

The first run works through your whole backlog, which can take a few minutes. Daily runs only look at the previous day's new lines.

### What to expect

- **Costs are small.** You pay your provider only for the suspicious lines, typically well under 100 tokens each plus a short instruction per batch of up to 100 lines. The first run covers your backlog; daily runs send very little.

## Updating

```sh
smash update
```

This checks `https://keysmash.website` for a newer release. If there is one, it downloads the build for your platform, verifies its SHA-256 checksum and replaces `~/.local/bin/smash` in place. Your config, database and daily job are kept; the job uses the new version from its next run. Re-running the install command does the same.

## Configuration

`~/.smash/config` (mode 600):

```ini
PROVIDER=openai                # or: anthropic
OPENAI_API_KEY=enc:v1:…        # encrypted automatically
# OPENAI_MODEL=gpt-6-luna
# ANTHROPIC_API_KEY=…          # paste a plain key here; smash encrypts it on the next run
# ANTHROPIC_MODEL=claude-haiku-5-5
```

To switch providers, set `PROVIDER` and add that provider's key. A plain-text key pasted into the file is encrypted the next time smash runs.

## Background job

| | macOS | Linux |
| --- | --- | --- |
| Installed by | `smash` setup (you're asked) | `smash` setup (you're asked) |
| Runs | daily at 10:00; catches up after sleep | daily at 10:00; `Persistent=true` catches up after downtime |
| Files | `~/Library/LaunchAgents/com.smash.daily.plist` | `~/.config/systemd/user/smash.service`, `smash.timer` |
| Logs | `~/.smash/launchd.log` | `journalctl --user -u smash` |
| Run it now | `launchctl kickstart gui/$(id -u)/com.smash.daily` | `systemctl --user start smash.service` |

On Linux, user timers run while you're logged in. To also run while logged out: `loginctl enable-linger $USER`. Without systemd (containers, some WSL setups), setup prints a crontab line instead.

### Manual runs only

To keep smash but stop the daily job, remove the job and run `smash run` (or `smash`) yourself whenever you like.

**macOS**
```sh
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.smash.daily.plist
rm ~/Library/LaunchAgents/com.smash.daily.plist
```

**Linux**
```sh
systemctl --user disable --now smash.timer
rm ~/.config/systemd/user/smash.service ~/.config/systemd/user/smash.timer
systemctl --user daemon-reload
```

Running `smash` again later offers to reinstall the job.

## Uninstall

```sh
smash uninstall
```

This lists what it will remove and asks before deleting: the daily job, `~/.smash` (encrypted API key, database, logs) and `~/.local/bin/smash`. Lines that were already redacted stay redacted.

To do it by hand:

**macOS**
```sh
launchctl bootout gui/$(id -u) ~/Library/LaunchAgents/com.smash.daily.plist 2>/dev/null
rm -f ~/Library/LaunchAgents/com.smash.daily.plist
rm -rf ~/.smash ~/.local/bin/smash
```

**Linux**
```sh
systemctl --user disable --now smash.timer 2>/dev/null
rm -f ~/.config/systemd/user/smash.service ~/.config/systemd/user/smash.timer
systemctl --user daemon-reload
rm -rf ~/.smash ~/.local/bin/smash
```

## Platforms

macOS and Linux, on Apple Silicon / ARM64 and Intel / x86-64. Windows isn't supported natively; inside WSL, smash cleans WSL's own history and logs.

## Build from source

Builds and tests run in Docker:

```sh
docker compose run --rm go go test ./...
docker compose run --rm -e GOOS=darwin -e GOARCH=arm64 -e CGO_ENABLED=0 go go build -o bin/smash .
```

`AGENTS.md` describes the architecture and conventions for contributors.

## License

[MIT](LICENSE)
