# Claude Desktop Session Sync — Linux

**English** | [Tiếng Việt](README_VI.md)

A command-line tool for **local sessions created in the Code tab of Claude Desktop**, with CachyOS/Arch as the primary target. It copies missing sidebar records between accounts and organizations on **the same machine, under the same Linux user**. Transcripts are already shared locally, so switching to the CLI is not required to continue working.

**Version 0.1.1 status:** paths, schemas, transcripts and dry-run behavior were checked on CachyOS with `claude-desktop 3.6556.2-1`; writes, backups and undo were tested with fixtures. **Reopening and continuing a session in Desktop after switching accounts has not been verified.** This release is not a certification of UI compatibility. See the [verification report](docs/VERIFICATION.md).

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

4. **Fully quit Claude Desktop and all Claude workers/CLI processes.** Closing a window may leave the app running. Keep it stopped throughout sync/undo. The tool checks processes and refuses to write when it detects Claude; it does not kill processes automatically.
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

## Existing records and conflicts

- Only missing files are added to existing account/org directories. Each complete destination file is published atomically using a hard link, refusing an existing destination name.
- Records with the same filename but different contents are reported as a **conflict**; the tool does not select the newest version by timestamp. Multiple internal IDs pointing to the same transcript are also blocked to prevent duplication.
- Continuing work under account B updates the shared transcript, while account A's metadata may become stale. Version 0.1.1 **does not reconcile diverged metadata automatically**. Check conflicts before switching back. This is not continuous synchronization of all runtime state.
- A `deleted_<id>` marker in any bucket prevents adding a session with that ID or alias (`cliSessionId`, `unarchivedCliSessionId`, `priorCliSessionIds`, or the filename ID).
- Existing stale copies of tombstoned sessions are not cleaned up or moved. Tombstones are not propagated, and the worktree registry is not modified.
- The complete transcript is streamed, including large lines and inherited history. A session needs a matching ID and Desktop provenance. A fork without its own Desktop turn is accepted only when its metadata points to a proven Desktop parent, its transcript contains that parent's Desktop identity, and it also contains the child's own ID. Unrelated inherited history or a CLI entrypoint is insufficient. Missing/unproven transcripts and `.desktop-released.json` sidecars cause the session to be skipped; malformed transcripts or read errors stop planning with an explicit error.
- Desktop transcripts without a pointer are listed for investigation; the tool **does not invent IDs** or reconstruct metadata/worktree leases automatically.

These limits preserve the original data. Run `plan` before every apply operation.

## Backup and undo

Before adding any file, the tool writes a manifest containing the complete selected payloads and checksums to:

```text
<userData>/.linux-session-sync-backups/<backup-id>/manifest.json
```

Directories use mode `0700`, and files use `0600`. With `--copy all`, the backup includes secrets embedded in the record. Do not upload backups or full plan output to GitHub. Use `--summary` when you only need the operation list; without it, JSON output contains metadata payloads.

The backup contains **the records that will be added**, not all transcripts or the entire profile. Original records and transcripts are not modified. The manifest is persisted before the first addition; if an operation fails partway through, use the backup ID from the directory name to undo it.

```bash
claude-session-sync undo --backup BACKUP_ID
claude-session-sync undo --backup BACKUP_ID --apply --offline
```

Undo moves added records into quarantine within the same backup, preserving their contents. If the app has modified a record, undo refuses to remove it so those changes are retained. Do not run it while the app or another user is editing the data. Backups are not deleted automatically. Version 0.1.1 also supports undoing 0.1.0 backups.

## Build and test

Go 1.25+; no external dependencies:

```bash
go test -race -cover ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o claude-session-sync .
```

Build an Arch package from source with `makepkg --nodeps`. This requires `makepkg`, `fakeroot` and Go; `--nodeps` is only appropriate when the build tools are already installed, for example Go through mise. Do not run it as root.

References: the [original Windows repository](https://github.com/realperest/claude-desktop-session-sync) and [issue #74662](https://github.com/anthropics/claude-code/issues/74662). This is an independent Go implementation, not a renamed PowerShell script. It is not affiliated with Anthropic.
