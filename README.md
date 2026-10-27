# Claude Desktop Session Sync — Linux

**English** | [Tiếng Việt](README_VI.md)
[Changelog](CHANGELOG.md) · [Releases](https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases)


A command-line tool for **local sessions created in the Code tab of Claude Desktop**, with CachyOS/Arch as the primary target. It synchronizes sidebar records in both directions between accounts and organizations on **the same machine, under the same Linux user**. Transcripts are already shared locally, so switching to the CLI is not required to continue working.

**Version 0.3.2 status:** paths, schemas, transcripts and dry-run behavior were checked on CachyOS with `claude-desktop 3.6556.2-1`; writes, backups and undo were tested with fixtures. **Reopening and continuing a session in Desktop after switching accounts has not been verified.** This release is not a certification of UI compatibility. See the [verification report](docs/VERIFICATION.md).

## Copy modes

| Option | Contents |
|---|---|
| `--copy all` **(default)** | Copies the session record byte for byte: configuration, permissions, MCP settings, state, transcript/worktree references and every other field. **Secrets embedded in the record are not filtered.** |
| `--copy chat` | Creates a minimal record for opening the conversation history, preserving IDs and folder/worktree information. Runtime configuration, MCP settings and previously granted permissions are not transferred; permission mode is set to `default`. |

`all` preserves **session configuration**. The tool does not read, transfer or swap Claude login cookies, keyring entries, browser profiles, `~/.claude/.credentials.json` or account tokens. Sign in and switch accounts inside Claude Desktop. MCP connections using the target account's OAuth may require signing in again.

The scope of `all` is the contents of `local_*.json`, **not the entire Claude profile**. Global settings and account-specific saved environments (`ccd-session-presets.json`, `mcp-user-tool-toggles.json`, extensions, settings/hooks) are not merged. References stored in the session are preserved; references to configuration belonging to the original account have not been verified under another account. There is no implicit filtering in `all`.

## Install on CachyOS/Arch

The binary is a static ELF executable. Running it does not require Python, Node, Go or root privileges. Claude Desktop must already be installed with a working Code tab. The machine used for verification had the `claude-desktop` package from the `cachyos` repository; this tool does not install or repackage Claude Desktop.

Download the `linux-amd64.tar.gz` archive and `SHA256SUMS` from Releases. Verify the downloaded archive before extracting it, then install the extracted binary:

```bash
sha256sum -c SHA256SUMS --ignore-missing
mkdir -p ~/.local/bin
install -m 755 claude-session-sync ~/.local/bin/claude-session-sync
~/.local/bin/claude-session-sync version
```

Alternatively, install the built `*.pkg.tar.zst` package with `sudo pacman -U <file.pkg.tar.zst>`. The ARM64 binary was cross-compiled but has not been executed on ARM64 hardware.

## English terminal interface, Force quit and Retry

Run `claude-session-sync` in a terminal, or use `claude-session-sync interactive`, to open the English menu. Choose a preview, backup-and-sync operation, or undo. Interactive previews and results show concise counts rather than full JSON payloads.

If Claude is running when you apply a sync or undo, choose:

- **R — Retry:** close Claude yourself, then check again.
- **F — Force quit:** stop the detected Claude Desktop/CLI/worker processes belonging to your Linux user, then check again and continue. This can interrupt active work. The tool sends TERM, waits up to 6 seconds, and uses KILL only for surviving processes from the original snapshot. It rechecks process identity before signalling and does not kill newly started Claude processes automatically.
- **C — Cancel:** leave the operation without applying a new sync.

The same prompt can be used with a direct command:

```bash
claude-session-sync sync --copy all --apply --offline --interactive
```

For explicit non-interactive process termination, use:

```bash
claude-session-sync sync --copy all --apply --offline --force-quit --summary
```

`--force-quit` requires `--apply --offline`; it is rejected for dry runs. Without that flag or choosing **F**, processes are never stopped automatically. Backups are created after Claude stops, before the first session record is written. Force quitting does not guarantee that unsaved work in Claude is persisted.

## Sign in to multiple accounts and continue working

1. In **Claude Desktop**, sign in to account A and open the **Code** tab. Repeat for account B and each organization you want to use. Signing in creates the corresponding directories; this tool only uses directories that already exist and does not create account identities.
2. Inspect the discovered data and account/org directories:

   ```bash
   claude-session-sync doctor --summary
   ```

   The `buckets` field lists `<account-uuid>/<org-uuid>`. These are directory IDs, not email addresses or an indication of the active account. The tool does not infer the current account.

3. Preview all operations; **no files are changed**:

   ```bash
   claude-session-sync plan --copy all --summary
   ```

   To copy history only:

   ```bash
   claude-session-sync plan --copy chat --summary
   ```

   To copy only from A to B, replace the placeholders below with IDs from `doctor`:

   ```bash
   claude-session-sync plan --from ACCOUNT_A_UUID/ORG_A_UUID --to ACCOUNT_B_UUID/ORG_B_UUID --copy all --summary
   ```

4. **Fully quit Claude Desktop and all Claude workers/CLI processes.** Closing a window may leave the app running. Keep it stopped throughout sync/undo. The tool checks processes and refuses to write while Claude is running. The interactive interface offers Force quit / Retry / Cancel; process termination requires choosing Force quit or explicitly supplying `--force-quit`.
5. Apply the same mode and scope you reviewed:

   ```bash
   claude-session-sync sync --copy all --apply --offline --summary
   ```

   Use `--copy chat` instead if desired, or add the same `--from`/`--to` options used in the preview. Without `--apply`, `sync` is also a dry run. An explicit `--dry-run` is supported and cannot be combined with `--apply`.

6. Open Claude Desktop, sign in to/select the target account and organization, then open **Code → Local/Recents** and select the session. Check its history, folder, branch and configuration before sending another message. Listing and continuing sessions under a different account must still be verified on your app version; use the [validation checklist](docs/VERIFICATION.md#kiem-chung-ui-con-thieu).

`claude --resume` and CLI import do not replace this workflow: the tool targets sidebar records for Desktop-created sessions.

## Verified paths and custom locations

The following paths were observed on the CachyOS machine used for verification:

```text
~/.config/Claude/claude-code-sessions/<account>/<org>/local_<id>.json
~/.claude/projects/<project>/<cli-session-id>.jsonl
```

`doctor` only accepts an existing root containing `claude-code-sessions`; it does not create guessed paths. It checks both `$XDG_CONFIG_HOME/Claude` and `~/.config/Claude`. If multiple distinct roots are found, or no suitable root exists, specify the locations explicitly:

```bash
claude-session-sync doctor --data-dir /verified/path/to/userData --projects-dir /verified/path/to/projects --summary
```

Other commands accept the same two options. Flatpak, Wine, Windows, macOS, SSH and WSL layouts are not converted automatically.

## Bidirectional synchronization, messages and deletions

The default sync considers **every existing account/org bucket as both a source and a destination**. It adds missing sessions and updates existing session records when another bucket has a newer version. Use it before switching accounts so the next account receives the latest transcript pointer and workflow state.

- The newest valid record is selected by filesystem modification time; `lastActivityAt` breaks timestamp ties. The winner's modification time is preserved on copies to prevent repeated back-and-forth updates. When equally recent records still differ, they are retained and reported as a conflict; choose an explicit source with `--from` to resolve it.
- `--from ACCOUNT/ORG --to ACCOUNT/ORG` limits the operation to a one-way transfer from the chosen source. That selection can replace a newer target version, so review the plan. Every replaced target version is backed up.
- `--copy all` copies the complete newest session record without redaction, including configuration and current/prior transcript IDs. Existing records are atomically replaced only after all previous bytes and planned new bytes have been durably backed up.
- `--copy chat` updates history and transcript/worktree identity while keeping each existing target's own runtime configuration and permissions. New target records use the minimal chat projection and default permissions.
- Message files under the shared local projects directory are **never overwritten, rewritten or merged**. Accounts use the same on-disk transcripts. Updating the sidebar record makes an account point to the current transcript, including after its CLI ID advances. All previous transcript files remain intact.
- Deletion markers are propagated to destination buckets. Every matching stale session record is moved out of the active session list with its complete bytes retained in backup/quarantine. Markers match filename IDs, `cliSessionId`, `unarchivedCliSessionId` and `priorCliSessionIds`, so deleted sessions do not reappear.
- Multiple distinct internal session IDs pointing to the same transcript are still reported rather than silently merged. Corrupt records are not spread. A complete transcript scan or verified Desktop parent lineage is required; missing/unproven transcripts and `.desktop-released.json` sidecars are skipped. Read errors and malformed transcripts stop planning explicitly.
- A transcript ID can appear in multiple project/worktree directories. Metadata-only copies do not block a proven Desktop history. Identical proven files are accepted; different proven histories are reported and only the affected session is skipped. Provenance is checked per file, never combined across copies. A release sidecar beside any copy blocks that session. Files are never moved, merged or deleted to resolve duplicates.
- Desktop transcripts without a sidebar pointer are listed for investigation. The tool does not invent IDs, rebuild worktree leases or modify the worktree registry.

Run sync again before switching back after working in another account. This is an offline operation, not a watcher while Claude is running. The terminal menu offers Force quit / Retry / Cancel. Cross-account continuation in the Desktop UI remains to be checked on the installed app version.

## Backup and undo

Before adding any file, the tool writes a manifest containing the complete selected payloads and checksums to:

```text
<userData>/.linux-session-sync-backups/<backup-id>/manifest.json
```

Directories use mode `0700`, and files use `0600`. With `--copy all`, the backup includes secrets embedded in the record. Do not upload backups or full plan output to GitHub. Use `--summary` when you only need the operation list; without it, JSON output contains metadata payloads.

The write-ahead backup contains **both original bytes and replacement bytes for all planned changes**, not all transcripts or the entire profile. It is persisted before the first change. Existing metadata can then be replaced atomically or quarantined; transcript files remain untouched. If an operation fails partway through, use the backup ID to undo completed changes while leaving unapplied actions alone.

```bash
claude-session-sync undo --backup BACKUP_ID
claude-session-sync undo --backup BACKUP_ID --apply --offline
```

Undo restores previous record versions, restores records quarantined by sync, and reverses propagated markers/additions. Applied versions are retained in the backup's `undone` quarantine. If the app has modified a target since sync, undo refuses to overwrite it so those changes are retained. Do not run it while the app or another user is editing the data. Backups are not deleted automatically. Version 0.3.2 also supports undoing 0.1.0, 0.1.1, 0.2.0, 0.3.0 and 0.3.1 backups.

## Build and test

Go 1.25+; no external dependencies:

```bash
go test -race -cover ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o claude-session-sync .
```

Build an Arch package from source with `makepkg --nodeps`. This requires `makepkg`, `fakeroot` and Go; `--nodeps` is only appropriate when the build tools are already installed, for example Go through mise. Do not run it as root.

References: the [original Windows repository](https://github.com/realperest/claude-desktop-session-sync) and [issue #74662](https://github.com/anthropics/claude-code/issues/74662). This is an independent Go implementation, not a renamed PowerShell script. It is not affiliated with Anthropic.
