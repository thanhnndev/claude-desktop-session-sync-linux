# Changelog

Changes to Claude Desktop Session Sync for Linux, newest first. Dates use Asia/Saigon. The tool targets local sessions created in the **Claude Desktop Code tab** on the same machine and Linux user.

## [0.3.2] — 2026-10-27

### Fixed

- Fixed `ambiguous transcript ID` aborting the entire sync when a Desktop conversation and a worktree metadata-only file share the same transcript filename.
- Validate Desktop provenance independently in every copy. Metadata-only stubs do not replace a proven conversation, and evidence is never combined across separate files.
- Accept byte-identical proven histories using streaming SHA-256. Different proven histories produce a warning and skip only the affected session; unrelated sessions continue. The tool does not choose by file size or modification time.
- Respect `.desktop-released.json` beside any copy, including a metadata-only stub, so duplicate handling cannot revive a released session.
- Retain backup/undo compatibility with 0.3.1 and all previously supported backup versions.

### Verification

- 97 passing test cases including subtests; race detector passed; 72.6% statement coverage; `go vet` passed.
- Regression coverage includes both copy modes, stubs before/after a history in directory ordering, identical and conflicting histories, independent fork proofs, parent stubs, release markers, stale plans, CLI-only rejection, transcript retention and backup/apply/undo.
- Built static Linux amd64 and arm64 binaries and an Arch x86_64 package; package checks passed. ARM64 was cross-compiled, not executed on physical hardware.
- Real-data dry run: 475 records, 213 proposed additions, 165 proposed updates, no missing/unproven transcripts and no warnings. Both reported files retained their checksums. This fix was not applied to live metadata by the automated checks.
- [GitHub CI for the release code](https://github.com/thanhnndev/claude-desktop-session-sync-linux/actions/runs/40450456386) passed.

### Upgrade and compatibility

- Replace the installed binary with 0.3.2, quit any already-open old sync menu with **Q**, then start it again. Running processes keep the code with which they started.
- No storage migration or transcript cleanup is required. Preview again before applying; existing backups remain available.
- Full user validation of session configuration, worktrees and MCP access across every target login remains outstanding. Different proven transcript histories are reported, not merged.

## [0.3.1] — 2026-10-25

### Fixed

- Protected unrelated user services, such as systemd, no longer prevent sync solely because `/proc/<pid>/exe` denies access and an accessible process name identifies an unrelated service.
- Claude/Electron-like or unknown identities, and unexpected inspection errors, continue to block writes. Unrelated services are not signalled.

### Verification

- 82 passing test cases including subtests, race detector and `go vet`; 72.1% statement coverage; Linux amd64/arm64 builds and Arch package checks passed.
- After the fix, a user-requested real apply added 267 records from 89 source records across four account/org buckets. Added bytes matched the write-ahead backup; transcripts were not rewritten. Desktop was opened for the user to validate continuation.
- Undo of 0.3.0 and earlier supported backups remains available.

## [0.3.0] — 2026-10-25

### Added and changed

- Bidirectional synchronization: all existing account/org buckets are sources and destinations by default. Add missing records and update older copies using file modification time, with `lastActivityAt` to break ties.
- Preserve the winning record's modification time to avoid repeated back-and-forth updates. Equally recent differing records are reported as conflicts.
- Explicit `--from` and `--to` select an authoritative one-way transfer.
- Propagate deletion markers for current/prior transcript aliases; quarantine matching stale metadata to prevent resurrection.
- Store original and replacement bytes, timestamps and checksums before writes; stage and atomically replace updates. Undo restores prior metadata and quarantined records, reverses additions/markers, and retains applied versions separately.
- Chat-mode updates retain an existing target's runtime configuration and permissions. All-mode copies retain the complete source bytes.
- Tests cover A-to-B-to-A changes, advancing transcript IDs, shared messages, deletions, interrupted operations, stale targets, conflicts and reversible backups.

## [0.2.0] — 2026-10-25

### Added

- English interactive terminal menu for previews, backup/sync in both copy modes, undo and quit.
- Retry, Force quit and Cancel when Claude is running; explicit `--force-quit` for non-interactive writes with `--apply --offline`.
- Process identity checks and a fixed process snapshot: TERM, a six-second wait, then KILL only for original surviving processes. Newly started Claude processes trigger another guard check.
- Concise interactive results and tests for menu routing, cancellation, retries, process classification and signal handling.

## [0.1.1] — 2026-10-25

### Fixed

- Stream complete JSONL transcripts instead of checking only the first 100 lines / 1 MiB. Handle large lines, missing final newlines, read errors and malformed tails explicitly.
- Verify Desktop forks with inherited parent history and no child turns using existing parent pointers and independent transcript provenance.
- Preserve the parent reference in chat mode. Reject unproven, conflicting, CLI-only or cyclic lineage; refuse symlinks and non-regular transcripts.
- Real-data dry run recognized all 89 source records, proposing 267 additions with no missing transcripts or warnings.

## [0.1.0] — 2026-10-25

### Added

- Initial offline Linux implementation focused on CachyOS/Arch and verified Desktop Code metadata/transcript locations.
- `doctor`, `plan`, `sync` and `undo`; full-record and minimal-chat copy modes; explicit source/target buckets and custom verified roots.
- Additive recovery of missing sidebar records, dry runs, process refusal, filesystem containment, locking, symlink checks and write-ahead backups. This initial release did not update existing records bidirectionally.
- Full-record mode preserves original bytes, including embedded configuration/secrets; login cookies, keyring data and separate account credentials are not swapped.
- English README, Vietnamese README with language navigation, verification report, Linux amd64/arm64 archives, Arch package and source archive.

## Known scope and limitations

- Run sync with Claude stopped before switching accounts. This is an offline operation, not an automatic watcher.
- Session records are reconciled; shared transcripts are never rewritten or merged. Global account settings, saved environments, worktree registries and login credentials are outside the transfer scope.
- A metadata backup contains the planned changes, not a full profile/transcript backup. Undo refuses to overwrite targets changed since sync.
- Anthropic's [account-bound preserved thinking](https://platform.claude.com/docs/en/build-with-claude/preserved-thinking#account-bound-thinking) can prevent an independent organization from reusing earlier protected reasoning. Sync does not change provider permissions or remove that restriction; keeping messages does not guarantee reuse of protected thinking.
- Filesystem verification does not certify every Desktop version, target organization, worktree, MCP connection or saved environment. See [the verification report](docs/VERIFICATION.md).

[0.3.2]: https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases/tag/v0.3.2
[0.3.1]: https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases/tag/v0.3.1
[0.3.0]: https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases/tag/v0.3.0
[0.2.0]: https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases/tag/v0.2.0
[0.1.1]: https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases/tag/v0.1.1
[0.1.0]: https://github.com/thanhnndev/claude-desktop-session-sync-linux/releases/tag/v0.1.0
